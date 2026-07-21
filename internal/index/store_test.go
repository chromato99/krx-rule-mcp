package index

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/corpus"
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

func TestSnapshotRoundTripPreservesStructuralAnchor(t *testing.T) {
	root := t.TempDir()
	doc := model.Document{
		ID: "rule-anchor", Title: "구조 규정", SourceURL: "https://example.test/anchor",
		CollectedAt: time.Now().UTC(), DocumentType: model.DocumentTypeRule,
		Body: "제1장 총칙\n\n**제11조의2(구조 앵커)**① 기준을 정한다.\n\n1. 고유앵커문구",
	}
	writeIndexTestDocument(t, root, doc)
	snap := writeTestIndexSnapshot(t, root)
	loaded, err := LoadSnapshot(filepath.Join(root, "index", "bm25.krxidx"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != indexSnapshotFormatVersion || len(loaded.Chunks) != len(snap.Chunks) {
		t.Fatalf("snapshot round trip mismatch: %#v", loaded)
	}
	found := false
	for _, chunk := range loaded.Chunks {
		if strings.Contains(chunk.Text, "고유앵커문구") {
			found = true
			wantPath := []string{"제1장 총칙", "제11조의2(구조 앵커)", "①", "1."}
			if chunk.ArticleID != "제11조의2" || !stringSlicesEqual(chunk.HeadingPath, wantPath) {
				t.Fatalf("round-tripped anchor = %q %#v", chunk.ArticleID, chunk.HeadingPath)
			}
		}
	}
	if !found {
		t.Fatal("anchored chunk not found")
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
	t.Setenv("KRX_EMBEDDING_MODEL_REVISION", "test-revision")
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
	t.Setenv("KRX_EMBEDDING_MODEL_REVISION", "test-revision")
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
	stale.Body = "stale listing review body"
	vectorPath := filepath.Join(root, "index", "vectors.krxvec")
	writeIndexTestDocument(t, root, stale)
	writeTestVectorSnapshot(t, root, vectorPath, map[string][]float64{"rule-1#0": {1, 0}})
	writeIndexTestDocument(t, root, doc)
	loadedCorpus, err := corpus.Load(root)
	if err != nil {
		t.Fatalf("load current corpus: %v", err)
	}
	vectors, reason, err := LoadVectorMap(vectorPath, loadedCorpus.Documents, attachmentDocuments(loadedCorpus.Documents, loadedCorpus.AttachmentTexts))
	if err != nil {
		t.Fatalf("load vector map: %v", err)
	}
	if reason != "index_source_hash_mismatch" && reason != "document_hash_mismatch" {
		t.Fatalf("reason = %q, want index source or document mismatch", reason)
	}
	if len(vectors) != 0 {
		t.Fatalf("loaded stale vectors: %#v", vectors)
	}
}

func TestVectorSnapshotIgnoresEmbeddingConfigMismatch(t *testing.T) {
	t.Setenv("KRX_EMBEDDING_MODEL", "other-model")
	t.Setenv("KRX_EMBEDDING_MODEL_REVISION", "test-revision")
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

func TestVectorSnapshotIgnoresModelRevisionMismatch(t *testing.T) {
	t.Setenv("KRX_EMBEDDING_MODEL", "test-model")
	t.Setenv("KRX_EMBEDDING_MODEL_REVISION", "other-revision")
	t.Setenv("KRX_EMBEDDING_DIMENSIONS", "2")
	root := t.TempDir()
	doc := model.Document{
		ID: "rule-1", Title: "상장규정", SourceURL: "https://example.test/rule",
		CollectedAt: time.Now().UTC(), DocumentType: model.DocumentTypeRule, Body: "상장 심사",
	}
	writeIndexTestDocument(t, root, doc)
	writeTestIndexSnapshot(t, root)
	vectorPath := filepath.Join(root, "index", "vectors.krxvec")
	writeTestVectorSnapshot(t, root, vectorPath, map[string][]float64{"rule-1#0": {1, 0}})
	repo, err := LoadRepository(root, filepath.Join(root, "index", "bm25.krxidx"), vectorPath)
	if err != nil {
		t.Fatalf("load repository: %v", err)
	}
	if repo.Engine.HasVectors() || len(repo.VectorIndexes) != 1 || repo.VectorIndexes[0].RejectedReason != "embedding_model_revision_mismatch" {
		t.Fatalf("unexpected vector status: %#v", repo.VectorIndexes)
	}
}

func TestVectorDisabledDoesNotReadConfiguredFile(t *testing.T) {
	root := t.TempDir()
	doc := model.Document{
		ID: "rule-1", Title: "상장규정", SourceURL: "https://example.test/rule",
		CollectedAt: time.Now().UTC(), DocumentType: model.DocumentTypeRule, Body: "상장 심사",
	}
	writeIndexTestDocument(t, root, doc)
	writeTestIndexSnapshot(t, root)
	unreadableVectorPath := filepath.Join(root, "index", "vector-directory")
	if err := os.MkdirAll(unreadableVectorPath, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := LoadRepositoryWithOptions(root, filepath.Join(root, "index", "bm25.krxidx"), RepositoryLoadOptions{
		VectorEnabled: false, VectorIndexPaths: []string{unreadableVectorPath},
	})
	if err != nil {
		t.Fatalf("disabled vector load: %v", err)
	}
	if repo.Engine.HasVectors() || len(repo.VectorIndexes) != 0 {
		t.Fatalf("disabled vector file was inspected: %#v", repo.VectorIndexes)
	}
}

func TestMalformedOptionalVectorFallsBackButRequiredFails(t *testing.T) {
	root := t.TempDir()
	doc := model.Document{
		ID: "rule-1", Title: "상장규정", SourceURL: "https://example.test/rule",
		CollectedAt: time.Now().UTC(), DocumentType: model.DocumentTypeRule, Body: "상장 심사",
	}
	writeIndexTestDocument(t, root, doc)
	writeTestIndexSnapshot(t, root)
	vectorPath := filepath.Join(root, "index", "malformed.krxvec")
	if err := os.WriteFile(vectorPath, []byte("not a vector snapshot"), 0o644); err != nil {
		t.Fatal(err)
	}
	options := RepositoryLoadOptions{VectorEnabled: true, VectorIndexPaths: []string{vectorPath}}
	repo, err := LoadRepositoryWithOptions(root, filepath.Join(root, "index", "bm25.krxidx"), options)
	if err != nil {
		t.Fatalf("optional vector should fall back: %v", err)
	}
	if repo.Engine.HasVectors() || len(repo.VectorIndexes) != 1 || !strings.HasPrefix(repo.VectorIndexes[0].RejectedReason, "load_failed:") {
		t.Fatalf("unexpected optional vector status: %#v", repo.VectorIndexes)
	}
	options.RequireVector = true
	if _, err := LoadRepositoryWithOptions(root, filepath.Join(root, "index", "bm25.krxidx"), options); err == nil {
		t.Fatal("required malformed vector unexpectedly loaded")
	}
}

func TestRequiredVectorRejectsPartialCoverage(t *testing.T) {
	t.Setenv("KRX_EMBEDDING_MODEL", "test-model")
	t.Setenv("KRX_EMBEDDING_MODEL_REVISION", "test-revision")
	t.Setenv("KRX_EMBEDDING_DIMENSIONS", "2")
	root := t.TempDir()
	for _, doc := range []model.Document{
		{ID: "rule-1", Title: "상장규정", SourceURL: "https://example.test/one", CollectedAt: time.Now().UTC(), DocumentType: model.DocumentTypeRule, Body: "상장 심사"},
		{ID: "rule-2", Title: "청산규정", SourceURL: "https://example.test/two", CollectedAt: time.Now().UTC(), DocumentType: model.DocumentTypeRule, Body: "청산 결제"},
	} {
		writeIndexTestDocument(t, root, doc)
	}
	snap := writeTestIndexSnapshot(t, root)
	vectorPath := filepath.Join(root, "index", "partial.krxvec")
	writeTestVectorSnapshot(t, root, vectorPath, map[string][]float64{snap.Chunks[0].ID: {1, 0}})
	if _, err := LoadRepositoryWithOptions(root, filepath.Join(root, "index", "bm25.krxidx"), RepositoryLoadOptions{
		VectorEnabled: true, RequireVector: true, VectorIndexPaths: []string{vectorPath},
	}); err == nil || !strings.Contains(err.Error(), "required vector snapshot") {
		t.Fatalf("required partial vector error = %v", err)
	}
}

func TestWriteFullVectorRejectsPartialCoverage(t *testing.T) {
	root := t.TempDir()
	for _, doc := range []model.Document{
		{ID: "rule-1", Title: "상장규정", SourceURL: "https://example.test/one", CollectedAt: time.Now().UTC(), DocumentType: model.DocumentTypeRule, Body: "상장 심사"},
		{ID: "rule-2", Title: "청산규정", SourceURL: "https://example.test/two", CollectedAt: time.Now().UTC(), DocumentType: model.DocumentTypeRule, Body: "청산 결제"},
	} {
		writeIndexTestDocument(t, root, doc)
	}
	snap, _, err := BuildSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	err = WriteVectorSnapshot(filepath.Join(root, "index", "partial.krxvec"), snap,
		map[string][]float64{snap.Chunks[0].ID: {1, 0}}, "test-model", 2,
		VectorWriteOptions{Scope: VectorScopeFull})
	if err == nil || !strings.Contains(err.Error(), "full vector coverage") {
		t.Fatalf("WriteVectorSnapshot error = %v, want full coverage failure", err)
	}
}

func TestVectorCoverageCountsUniqueChunks(t *testing.T) {
	t.Setenv("KRX_EMBEDDING_MODEL", "test-model")
	t.Setenv("KRX_EMBEDDING_MODEL_REVISION", "test-revision")
	t.Setenv("KRX_EMBEDDING_DIMENSIONS", "2")
	root := t.TempDir()
	doc := model.Document{ID: "rule-1", Title: "상장규정", SourceURL: "https://example.test/rule", CollectedAt: time.Now().UTC(), DocumentType: model.DocumentTypeRule, Body: "상장 심사"}
	writeIndexTestDocument(t, root, doc)
	writeTestIndexSnapshot(t, root)
	vectorPath := filepath.Join(root, "index", "vectors.krxvec")
	writeTestVectorSnapshot(t, root, vectorPath, map[string][]float64{"rule-1#0": {1, 0}})
	repo, err := LoadRepositoryWithOptions(root, filepath.Join(root, "index", "bm25.krxidx"), RepositoryLoadOptions{
		VectorEnabled: true, VectorIndexPaths: []string{vectorPath, vectorPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if repo.VectorCoverage != 1 || repo.VectorScope != VectorScopeFull {
		t.Fatalf("coverage=%v scope=%q", repo.VectorCoverage, repo.VectorScope)
	}
}

func TestWriteSnapshotIsAtomicForConcurrentReaders(t *testing.T) {
	root := t.TempDir()
	doc := model.Document{ID: "rule-1", Title: "상장규정", SourceURL: "https://example.test/rule", CollectedAt: time.Now().UTC(), DocumentType: model.DocumentTypeRule, Body: "상장 심사"}
	writeIndexTestDocument(t, root, doc)
	snap, _, err := BuildSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "index", "bm25.krxidx")
	if err := WriteSnapshot(path, snap); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	errors := make(chan error, 4)
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					loaded, err := LoadSnapshot(path)
					if err != nil {
						errors <- err
						return
					}
					if loaded.IndexBuildHash != snap.IndexBuildHash {
						errors <- fmt.Errorf("unexpected build hash %q", loaded.IndexBuildHash)
						return
					}
				}
			}
		}()
	}
	for i := range 25 {
		updated := snap
		updated.GeneratedAt = time.Now().UTC().Add(time.Duration(i) * time.Nanosecond).Format(time.RFC3339Nano)
		if err := WriteSnapshot(path, updated); err != nil {
			close(stop)
			readers.Wait()
			t.Fatal(err)
		}
	}
	close(stop)
	readers.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
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
	meta.BodyHash = model.HashText(doc.Body)
	meta.ContentHash = model.HashText(doc.Title + "\n" + doc.Body)
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
	options := VectorWriteOptions{
		ModelRevision:  "test-revision",
		QueryPrefix:    "query: ",
		DocumentPrefix: "passage: ",
	}
	if err := WriteVectorSnapshot(path, snap, vectors, "test-model", 2, options); err != nil {
		t.Fatalf("write vector snapshot: %v", err)
	}
	if err := WriteVectorMetadata(VectorMetadataPath(path), BuildVectorMetadata(snap, vectors, "test-model", 2, options)); err != nil {
		t.Fatalf("write vector metadata: %v", err)
	}
}
