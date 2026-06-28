package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chromato99/krx-rule-mcp/internal/model"
)

func TestRealDataVectorSearchEvaluation(t *testing.T) {
	if os.Getenv("KRX_VECTOR_DATA_TEST") != "1" {
		t.Skip("set KRX_VECTOR_DATA_TEST=1 to run collected-data vector search evaluation")
	}

	indexPath := os.Getenv("KRX_INDEX_PATH")
	if indexPath == "" {
		indexPath = filepath.Join("..", "..", "data", "index", "bm25.krxidx")
	}
	vectorPath := os.Getenv("KRX_VECTOR_INDEX_PATH")
	if vectorPath == "" {
		vectorPath = filepath.Join("..", "..", "data", "index", "vectors.krxvec")
	}
	indexPath = resolveDataTestPath(indexPath)
	vectorPath = resolveDataTestPath(vectorPath)
	dataRoot := dataTestRoot()
	repo, err := LoadRepository(dataRoot, indexPath, vectorPath)
	if err != nil {
		t.Fatalf("load vector repository: %v", err)
	}

	chunks := repo.Engine.Chunks()
	if len(chunks) == 0 {
		t.Fatal("loaded no chunks")
	}
	storedVectors := 0
	for _, c := range chunks {
		if len(c.Vector) > 0 && len(c.Vector) != 384 {
			t.Fatalf("chunk %s vector dimension = %d, want 384", c.ID, len(c.Vector))
		}
		if len(c.Vector) > 0 {
			storedVectors++
		}
	}
	if !repo.Engine.HasVectors() {
		t.Fatal("loaded no stored vectors")
	}
	if storedVectors != len(chunks) {
		t.Fatalf("stored vectors = %d, want full chunk coverage %d", storedVectors, len(chunks))
	}

	embedder, enabled, err := NewEmbedderFromEnv()
	if err != nil {
		t.Fatalf("new embedder: %v", err)
	}
	if !enabled || embedder == nil {
		t.Fatal("embedding env is not enabled")
	}
	vectors, err := embedder.Embed(t.Context(), []string{"서울외환시장 거래시간 변경"})
	if err != nil {
		t.Fatalf("embed query: %v", err)
	}
	if len(vectors) != 1 || len(vectors[0]) != 384 {
		t.Fatalf("query vector shape = %d/%d, want 1/384", len(vectors), len(vectors[0]))
	}

	results := repo.Engine.Search(SearchOptions{
		Query:       "서울외환시장 거래시간 변경",
		QueryVector: vectors[0],
		Limit:       5,
		Filter:      Filter{DocumentType: model.DocumentTypeNotice},
	})
	requireResult(t, results, "210217910", true)
	if results[0].BM25Score == 0 || results[0].VectorScore == 0 {
		t.Fatalf("expected RRF result with BM25 and vector scores: %#v", results[0])
	}
}

func resolveDataTestPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	repoRelative := filepath.Join("..", "..", path)
	if _, err := os.Stat(repoRelative); err == nil {
		return repoRelative
	}
	return path
}
