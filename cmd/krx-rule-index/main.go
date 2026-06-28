package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
)

func main() {
	var (
		dataDir     = flag.String("data-dir", envDataDir(), "KRX rule Markdown corpus directory")
		indexPath   = flag.String("index", "", "BM25/core index snapshot path")
		vectorPath  = flag.String("vector-index", "", "optional vector snapshot path")
		vectorLimit = flag.Int("vector-limit", 0, "optional maximum number of chunks to embed")
		sampleQuery = flag.String("vector-sample-query", "", "optional | separated sample queries for vector smoke indexing")
		samplePer   = flag.Int("vector-sample-per-query", 16, "chunks per sample query")
		force       = flag.Bool("force", false, "rebuild snapshots even when they are current")
		check       = flag.Bool("check", false, "check index freshness without writing files")
	)
	flag.Parse()

	if *indexPath == "" {
		*indexPath = envDefault("KRX_INDEX_PATH", filepath.Join(*dataDir, "index", "bm25.krxidx"))
	}

	snap, docs, err := searchindex.BuildSnapshot(*dataDir)
	if err != nil {
		fatal(err)
	}

	indexCurrent := bm25Current(*indexPath, snap)
	vectorRequested := strings.TrimSpace(*vectorPath) != ""
	vectorCurrent := true
	if vectorRequested {
		embedder, err := searchindex.NewDocumentEmbedderFromEnv()
		if err != nil {
			fatal(err)
		}
		vectorCurrent = vectorFresh(*vectorPath, snap, embedder)
	}

	if *check {
		if !indexCurrent {
			fmt.Fprintf(os.Stderr, "BM25 index is stale or missing: %s\n", *indexPath)
			os.Exit(1)
		}
		if vectorRequested && !vectorCurrent {
			fmt.Fprintf(os.Stderr, "vector index is stale or missing: %s\n", *vectorPath)
			os.Exit(1)
		}
		fmt.Println("index up to date")
		return
	}

	if *force || !indexCurrent {
		if err := searchindex.WriteSnapshot(*indexPath, snap); err != nil {
			fatal(err)
		}
		fmt.Printf("wrote BM25 index %s documents=%d chunks=%d\n", *indexPath, len(docs), len(snap.Chunks))
	} else {
		fmt.Printf("BM25 index up to date %s\n", *indexPath)
	}

	if !vectorRequested {
		return
	}
	embedder, err := searchindex.NewDocumentEmbedderFromEnv()
	if err != nil {
		fatal(err)
	}
	if !*force && vectorCurrent {
		fmt.Printf("vector index up to date %s\n", *vectorPath)
		return
	}
	selected := selectVectorChunks(snap.Chunks, splitQueries(*sampleQuery), *samplePer, *vectorLimit)
	vectors, err := searchindex.EmbedSnapshotChunks(context.Background(), selected, embedder)
	if err != nil {
		fatal(err)
	}
	model, dimensions := embedder.EmbeddingInfo()
	if dimensions == 0 && len(vectors) > 0 {
		for _, vector := range vectors {
			dimensions = len(vector)
			break
		}
	}
	if err := searchindex.WriteVectorSnapshot(*vectorPath, snap, vectors, model, dimensions); err != nil {
		fatal(err)
	}
	if err := searchindex.WriteVectorMetadata(searchindex.VectorMetadataPath(*vectorPath), searchindex.VectorMetadata{
		CorpusHash:     snap.CorpusHash,
		Model:          model,
		Dimensions:     dimensions,
		QueryPrefix:    envDefaultPreserveSpace("KRX_EMBEDDING_QUERY_PREFIX", "query: "),
		DocumentPrefix: envDefaultPreserveSpace("KRX_EMBEDDING_DOCUMENT_PREFIX", "passage: "),
	}); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote vector index %s vectors=%d model=%s dimensions=%d\n", *vectorPath, len(vectors), model, dimensions)
}

func bm25Current(path string, snap searchindex.Snapshot) bool {
	current, err := searchindex.LoadSnapshot(path)
	if err != nil {
		return false
	}
	return current.CorpusHash == snap.CorpusHash
}

func vectorFresh(path string, snap searchindex.Snapshot, embedder *searchindex.OpenAIEmbedder) bool {
	vector, err := searchindex.LoadVectorSnapshot(path)
	if err != nil {
		return false
	}
	if vector.CorpusHash != snap.CorpusHash || vector.Model != embedder.Model || vector.Dimensions != embedder.Dimensions {
		return false
	}
	metadata, err := searchindex.LoadVectorMetadata(searchindex.VectorMetadataPath(path))
	if err != nil {
		return false
	}
	return metadata.CorpusHash == snap.CorpusHash &&
		metadata.Model == embedder.Model &&
		metadata.Dimensions == embedder.Dimensions &&
		metadata.DocumentPrefix == envDefaultPreserveSpace("KRX_EMBEDDING_DOCUMENT_PREFIX", "passage: ") &&
		metadata.QueryPrefix == envDefaultPreserveSpace("KRX_EMBEDDING_QUERY_PREFIX", "query: ")
}

func selectVectorChunks(chunks []searchindex.SnapshotChunk, queries []string, perQuery, limit int) []searchindex.SnapshotChunk {
	selected := chunks
	if len(queries) > 0 {
		selected = nil
		seen := map[string]struct{}{}
		if perQuery <= 0 {
			perQuery = 16
		}
		for _, query := range queries {
			queryTokens := searchindex.Tokenize(query)
			type scored struct {
				score int
				order int
				chunk searchindex.SnapshotChunk
			}
			var scoredChunks []scored
			for order, chunk := range chunks {
				tokenSet := map[string]struct{}{}
				for _, token := range chunk.Tokens {
					tokenSet[token] = struct{}{}
				}
				score := 0
				for _, token := range queryTokens {
					if _, ok := tokenSet[token]; ok {
						score++
					}
				}
				if score > 0 {
					scoredChunks = append(scoredChunks, scored{score: score, order: order, chunk: chunk})
				}
			}
			sort.Slice(scoredChunks, func(i, j int) bool {
				if scoredChunks[i].score == scoredChunks[j].score {
					return scoredChunks[i].order < scoredChunks[j].order
				}
				return scoredChunks[i].score > scoredChunks[j].score
			})
			addedForQuery := 0
			for _, item := range scoredChunks {
				if addedForQuery >= perQuery {
					break
				}
				if _, ok := seen[item.chunk.ID]; ok {
					continue
				}
				seen[item.chunk.ID] = struct{}{}
				selected = append(selected, item.chunk)
				addedForQuery++
			}
		}
	}
	if limit > 0 && len(selected) > limit {
		return selected[:limit]
	}
	return selected
}

func splitQueries(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, "|") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func envDataDir() string {
	if value := strings.TrimSpace(os.Getenv("KRX_RULE_DATA_DIR")); value != "" {
		return value
	}
	return envDefault("KRX_DATA_DIR", "data")
}

func envDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envDefaultPreserveSpace(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	return value
}

func fatal(err error) {
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
