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
		indexDir    = flag.String("index-dir", envIndexDir(), "search index snapshot directory")
		indexPath   = flag.String("index", "", "BM25/core index snapshot path")
		vectorPath  = flag.String("vector-index", "", "optional vector snapshot path")
		vectorLimit = flag.Int("vector-limit", 0, "optional maximum number of chunks to embed")
		sampleQuery = flag.String("vector-sample-query", "", "optional | separated sample queries for vector smoke indexing")
		samplePer   = flag.Int("vector-sample-per-query", 16, "chunks per sample query")
		force       = flag.Bool("force", false, "rebuild snapshots even when they are current")
		check       = flag.Bool("check", false, "check index freshness without writing files")
		requireFull = flag.Bool("require-full-vector", false, "require a full-coverage vector snapshot when checking")
	)
	flag.Parse()

	if *indexPath == "" {
		*indexPath = envDefault("KRX_INDEX_PATH", searchindex.DefaultBM25Path(*indexDir))
	}
	if filepath.Base(filepath.Clean(*indexPath)) != searchindex.BM25SnapshotFile {
		fatal(fmt.Errorf("--index must name %s when generation publishing is enabled", searchindex.BM25SnapshotFile))
	}
	// Preserve the legacy --index flag as an index-directory selector. Files are
	// always published below generations/<content-id>; the root path is never
	// replaced independently of its vector companion.
	*indexDir = filepath.Dir(filepath.Clean(*indexPath))
	if strings.TrimSpace(*vectorPath) == "" {
		*vectorPath = strings.TrimSpace(os.Getenv("KRX_VECTOR_INDEX_PATH"))
	}
	vectorRequested := strings.TrimSpace(*vectorPath) != ""
	if *requireFull && !vectorRequested {
		fatal(fmt.Errorf("--require-full-vector requires --vector-index"))
	}

	var buildLock *searchindex.GenerationBuildLock
	var err error
	if !*check {
		buildLock, err = searchindex.AcquireGenerationBuildLock(*indexDir)
		if err != nil {
			fatal(err)
		}
		defer func() { _ = buildLock.Close() }()
	}

	snap, docs, err := searchindex.BuildReleaseSnapshot(*dataDir)
	if err != nil {
		fatal(err)
	}

	currentDescriptor, currentDir, currentErr := searchindex.ReadCurrentGeneration(*indexDir)
	currentIndexPath := ""
	currentVectorPath := ""
	indexCurrent := false
	if currentErr == nil {
		currentIndexPath = filepath.Join(currentDir, searchindex.BM25SnapshotFile)
		indexCurrent = currentDescriptor.IndexSourceHash == snap.IndexSourceHash &&
			currentDescriptor.IndexBuildHash == snap.IndexBuildHash &&
			currentDescriptor.CorpusReleaseHash == snap.CorpusReleaseHash &&
			bm25Current(currentIndexPath, snap)
	}
	vectorCurrent := true
	var embedder *searchindex.OpenAIEmbedder
	if vectorRequested {
		embedder, err = searchindex.NewDocumentEmbedderFromEnv()
		if err != nil {
			fatal(err)
		}
		vectorCurrent = false
		if indexCurrent && currentDescriptor.Vector != nil {
			currentVectorPath = filepath.Join(currentDir, searchindex.VectorSnapshotFile)
			vectorCurrent = vectorFreshWithPolicy(currentVectorPath, snap, embedder, *requireFull)
		}
	}

	if *check {
		if !indexCurrent {
			fmt.Fprintf(os.Stderr, "BM25 generation is stale, missing, or invalid under %s\n", *indexDir)
			os.Exit(1)
		}
		if vectorRequested && !vectorCurrent {
			fmt.Fprintf(os.Stderr, "vector generation is stale, missing, or invalid under %s\n", *indexDir)
			os.Exit(1)
		}
		fmt.Printf("index generation up to date %s\n", currentDescriptor.GenerationID)
		return
	}

	if !*force && indexCurrent && (!vectorRequested || vectorCurrent) {
		fmt.Printf("index generation up to date %s\n", currentDescriptor.GenerationID)
		return
	}

	build := searchindex.GenerationBuild{Snapshot: snap}
	if vectorRequested {
		selected := selectVectorChunks(snap.Chunks, splitQueries(*sampleQuery), *samplePer, *vectorLimit)
		if len(selected) == 0 {
			fatal(fmt.Errorf("vector selection produced no chunks"))
		}
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
		scope := searchindex.VectorScopeFull
		if len(selected) != len(snap.Chunks) {
			scope = searchindex.VectorScopeSample
		}
		build.IncludeVector = true
		build.Vectors = vectors
		build.VectorModel = model
		build.VectorDimensions = dimensions
		build.VectorOptions = searchindex.VectorWriteOptions{
			Scope:          scope,
			ModelRevision:  embedder.ModelRevision,
			QueryPrefix:    envDefaultPreserveSpace("KRX_EMBEDDING_QUERY_PREFIX", "query: "),
			DocumentPrefix: envDefaultPreserveSpace("KRX_EMBEDDING_DOCUMENT_PREFIX", "passage: "),
		}
	}
	published, err := buildLock.Publish(build)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("published index generation %s documents=%d chunks=%d", published.GenerationID, len(docs), len(snap.Chunks))
	if published.Vector != nil {
		fmt.Printf(" vectors=%d model=%s", published.Vector.Vectors, published.Vector.Model)
	}
	fmt.Println()
}

func bm25Current(path string, snap searchindex.Snapshot) bool {
	current, err := searchindex.LoadSnapshot(path)
	if err != nil {
		return false
	}
	return current.Version == snap.Version &&
		current.IndexSourceHash == snap.IndexSourceHash &&
		current.IndexBuildHash == snap.IndexBuildHash &&
		current.CorpusReleaseHash == snap.CorpusReleaseHash &&
		current.IndexerVersion == snap.IndexerVersion
}

func vectorFresh(path string, snap searchindex.Snapshot, embedder *searchindex.OpenAIEmbedder) bool {
	return vectorFreshWithPolicy(path, snap, embedder, false)
}

func vectorFreshWithPolicy(path string, snap searchindex.Snapshot, embedder *searchindex.OpenAIEmbedder, requireFull bool) bool {
	vector, err := searchindex.LoadVectorSnapshot(path)
	if err != nil {
		return false
	}
	if vector.Version != searchindex.VectorSnapshotFormatVersion || vector.IndexSourceHash != snap.IndexSourceHash || vector.IndexBuildHash != snap.IndexBuildHash || vector.CorpusReleaseHash != snap.CorpusReleaseHash ||
		vector.Model != embedder.Model || vector.ModelRevision != embedder.ModelRevision || vector.Dimensions != embedder.Dimensions ||
		vector.QueryPrefix != envDefaultPreserveSpace("KRX_EMBEDDING_QUERY_PREFIX", "query: ") ||
		vector.DocumentPrefix != envDefaultPreserveSpace("KRX_EMBEDDING_DOCUMENT_PREFIX", "passage: ") {
		return false
	}
	expectedIDs := make([]string, 0, len(snap.Chunks))
	vectorMap := make(map[string][]float64, len(vector.Vectors))
	for _, chunk := range snap.Chunks {
		expectedIDs = append(expectedIDs, chunk.ID)
	}
	for _, item := range vector.Vectors {
		vectorMap[item.ChunkID] = item.Vector
	}
	if requireFull && vector.Scope != searchindex.VectorScopeFull {
		return false
	}
	if err := searchindex.ValidateVectorMap(vectorMap, expectedIDs, vector.Dimensions, requireFull || vector.Scope == searchindex.VectorScopeFull); err != nil {
		return false
	}
	expectedMetadata := searchindex.BuildVectorMetadata(snap, vectorMap, vector.Model, vector.Dimensions, searchindex.VectorWriteOptions{
		Scope:          vector.Scope,
		ModelRevision:  vector.ModelRevision,
		QueryPrefix:    vector.QueryPrefix,
		DocumentPrefix: vector.DocumentPrefix,
		GenerationID:   vector.GenerationID,
	})
	metadata, err := searchindex.LoadVectorMetadata(searchindex.VectorMetadataPath(path))
	if err != nil {
		return false
	}
	return metadata.Version == searchindex.VectorMetadataFormatVersion &&
		metadata.GenerationID == vector.GenerationID &&
		metadata.IndexSourceHash == snap.IndexSourceHash &&
		metadata.IndexBuildHash == snap.IndexBuildHash &&
		metadata.CorpusReleaseHash == snap.CorpusReleaseHash &&
		metadata.Model == embedder.Model &&
		metadata.ModelRevision == embedder.ModelRevision &&
		metadata.Dimensions == embedder.Dimensions &&
		metadata.Scope == vector.Scope &&
		metadata.ExpectedChunkCount == len(snap.Chunks) &&
		metadata.StoredVectorCount == len(vector.Vectors) &&
		metadata.ChunkIDSetHash == vector.ChunkIDSetHash &&
		metadata.StoredChunkIDSetHash == expectedMetadata.StoredChunkIDSetHash &&
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

func envIndexDir() string {
	if value := strings.TrimSpace(os.Getenv("KRX_RULE_INDEX_DIR")); value != "" {
		return value
	}
	return envDefault("KRX_INDEX_DIR", searchindex.DefaultIndexDir)
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
	if !ok {
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
