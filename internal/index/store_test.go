package index

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/model"
	"gopkg.in/yaml.v3"
)

func TestGoGeneratedSnapshotLoadsIntoRepository(t *testing.T) {
	root := t.TempDir()
	doc := model.Document{
		ID:           "rule-1",
		Title:        "코스닥시장 상장규정",
		SourceURL:    "https://example.test/rule",
		CollectedAt:  time.Now().UTC(),
		ContentHash:  "hash-rule-1",
		DocumentType: model.DocumentTypeRule,
		Body:         "상장신청인은 신규상장 심사를 신청할 수 있다.",
	}
	writeIndexTestDocument(t, root, doc)
	writeTestIndexSnapshot(t, root)
	repo, err := LoadRepository(root, filepath.Join(root, "index", "bm25.krxidx"))
	if err != nil {
		t.Fatalf("load repository: %v", err)
	}
	results := repo.Engine.Search(SearchOptions{Query: "신규상장 심사", Limit: 1})
	if len(results) != 1 || results[0].ID != "rule-1" {
		t.Fatalf("unexpected search results: %#v", results)
	}
}

func TestLoadSnapshotRejectsUnsupportedSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bm25.krxidx")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write text snapshot: %v", err)
	}
	if _, err := LoadSnapshot(path); err == nil {
		t.Fatal("LoadSnapshot succeeded for unsupported snapshot")
	}
}

func TestVectorSnapshotLoadsIntoRepository(t *testing.T) {
	t.Setenv("KRX_EMBEDDING_MODEL", "test-model")
	t.Setenv("KRX_EMBEDDING_DIMENSIONS", "2")
	root := t.TempDir()
	doc := model.Document{
		ID:           "rule-1",
		Title:        "상장규정",
		SourceURL:    "https://example.test/rule",
		CollectedAt:  time.Now().UTC(),
		ContentHash:  "hash-rule-1",
		DocumentType: model.DocumentTypeRule,
		Body:         "상장 심사",
	}
	writeIndexTestDocument(t, root, doc)
	writeTestIndexSnapshot(t, root)
	vectorPath := filepath.Join(root, "index", "vectors.krxvec")
	writeTestVectorSnapshot(t, root, vectorPath, map[string][]float64{"rule-1#0": {1, 0}})
	repo, err := LoadRepository(root, filepath.Join(root, "index", "bm25.krxidx"), vectorPath)
	if err != nil {
		t.Fatalf("load repository: %v", err)
	}
	if !repo.Engine.HasVectors() {
		t.Fatal("repository did not load vectors")
	}
	results := repo.Engine.Search(SearchOptions{QueryVector: []float64{1, 0}, Limit: 1})
	if len(results) != 1 || results[0].ID != "rule-1" {
		t.Fatalf("unexpected vector results: %#v", results)
	}
}

func TestVectorSnapshotIgnoresStaleCorpus(t *testing.T) {
	t.Setenv("KRX_EMBEDDING_MODEL", "test-model")
	t.Setenv("KRX_EMBEDDING_DIMENSIONS", "2")
	root := t.TempDir()
	doc := model.Document{
		ID:           "rule-1",
		Title:        "상장규정",
		SourceURL:    "https://example.test/rule",
		CollectedAt:  time.Now().UTC(),
		ContentHash:  "hash-current",
		DocumentType: model.DocumentTypeRule,
		Body:         "상장 심사",
	}
	writeIndexTestDocument(t, root, doc)
	stale := doc
	stale.ContentHash = "hash-stale"
	vectorPath := filepath.Join(root, "index", "vectors.krxvec")
	writeIndexTestDocument(t, root, stale)
	writeTestVectorSnapshot(t, root, vectorPath, map[string][]float64{"rule-1#0": {1, 0}})
	writeIndexTestDocument(t, root, doc)
	vectors, reason, err := LoadVectorMap(vectorPath, []model.Document{doc}, loadAttachments(root, []model.Document{doc}))
	if err != nil {
		t.Fatalf("load vector map: %v", err)
	}
	if reason != "corpus_hash_mismatch" && reason != "document_hash_mismatch" {
		t.Fatalf("reason = %q, want corpus or document mismatch", reason)
	}
	if len(vectors) != 0 {
		t.Fatalf("loaded stale vectors: %#v", vectors)
	}
}

func TestVectorSnapshotIgnoresEmbeddingConfigMismatch(t *testing.T) {
	t.Setenv("KRX_EMBEDDING_MODEL", "other-model")
	t.Setenv("KRX_EMBEDDING_DIMENSIONS", "2")
	root := t.TempDir()
	doc := model.Document{
		ID:           "rule-1",
		Title:        "상장규정",
		SourceURL:    "https://example.test/rule",
		CollectedAt:  time.Now().UTC(),
		ContentHash:  "hash-rule-1",
		DocumentType: model.DocumentTypeRule,
		Body:         "상장 심사",
	}
	writeIndexTestDocument(t, root, doc)
	writeTestIndexSnapshot(t, root)
	vectorPath := filepath.Join(root, "index", "vectors.krxvec")
	writeTestVectorSnapshot(t, root, vectorPath, map[string][]float64{"rule-1#0": {1, 0}})
	repo, err := LoadRepository(root, filepath.Join(root, "index", "bm25.krxidx"), vectorPath)
	if err != nil {
		t.Fatalf("load repository: %v", err)
	}
	if repo.Engine.HasVectors() {
		t.Fatal("repository loaded vectors with mismatched embedding model")
	}
	if len(repo.VectorIndexes) != 1 || repo.VectorIndexes[0].RejectedReason != "embedding_model_mismatch" {
		t.Fatalf("unexpected vector status: %#v", repo.VectorIndexes)
	}
}

func writeIndexTestDocument(t *testing.T, root string, doc model.Document) string {
	t.Helper()
	folder := "rules"
	if doc.DocumentType == model.DocumentTypeNotice {
		folder = "notices"
	}
	path := filepath.Join(root, model.NormalizeLanguage(doc.Language), folder, model.Slug(doc.Title), "index.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, renderIndexTestMarkdown(t, doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func renderIndexTestMarkdown(t *testing.T, doc model.Document) []byte {
	t.Helper()
	meta := doc
	meta.Body = ""
	meta.Path = ""
	meta.Language = model.NormalizeLanguage(meta.Language)
	if meta.ContentHash == "" {
		meta.ContentHash = model.HashText(doc.Body)
	}
	var buf bytes.Buffer
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(meta); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	buf.WriteString("---\n\n")
	buf.WriteString(strings.TrimSpace(doc.Body))
	buf.WriteString("\n")
	return buf.Bytes()
}

func writeTestIndexSnapshot(t *testing.T, root string) Snapshot {
	t.Helper()
	snap, _, err := BuildSnapshot(root)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	if err := WriteSnapshot(filepath.Join(root, "index", "bm25.krxidx"), snap); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	return snap
}

func writeTestVectorSnapshot(t *testing.T, root, path string, vectors map[string][]float64) {
	t.Helper()
	snap, _, err := BuildSnapshot(root)
	if err != nil {
		t.Fatalf("build vector snapshot source: %v", err)
	}
	if err := WriteVectorSnapshot(path, snap, vectors, "test-model", 2); err != nil {
		t.Fatalf("write vector snapshot: %v", err)
	}
	if err := WriteVectorMetadata(VectorMetadataPath(path), VectorMetadata{
		CorpusHash:     snap.CorpusHash,
		Model:          "test-model",
		Dimensions:     2,
		QueryPrefix:    "query: ",
		DocumentPrefix: "passage: ",
	}); err != nil {
		t.Fatalf("write vector metadata: %v", err)
	}
}
