package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/corpus"
	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	"github.com/chromato99/krx-rule-mcp/internal/model"
)

func TestSelectVectorChunksSamplesByQuery(t *testing.T) {
	chunks := []searchindex.SnapshotChunk{
		{ID: "a", Tokens: []string{"상장", "심사"}},
		{ID: "b", Tokens: []string{"공시", "규정"}},
		{ID: "c", Tokens: []string{"상장", "규정"}},
	}
	selected := selectVectorChunks(chunks, []string{"상장 심사"}, 1, 0)
	if len(selected) != 1 || selected[0].ID != "a" {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestSplitQueries(t *testing.T) {
	got := splitQueries("상장 심사 | 공시 | ")
	if len(got) != 2 || got[0] != "상장 심사" || got[1] != "공시" {
		t.Fatalf("queries = %#v", got)
	}
}

func TestBM25CurrentUsesCorpusHash(t *testing.T) {
	root := writeCommandTestCorpus(t)
	snap, _, err := searchindex.BuildSnapshot(root)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	path := filepath.Join(root, "index", "bm25.krxidx")
	if err := searchindex.WriteSnapshot(path, snap); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	if !bm25Current(path, snap) {
		t.Fatal("expected BM25 index to be current")
	}
	snap.CorpusHash = "stale"
	if bm25Current(path, snap) {
		t.Fatal("expected BM25 index to be stale")
	}
}

func TestBM25CurrentDetectsConvertedAttachmentTextChange(t *testing.T) {
	root := t.TempDir()
	attachmentTextPath := filepath.Join("ko", "rules", "상장규정", "attachments", "별표.md")
	doc := model.Document{
		ID:           "rule-1",
		Title:        "상장규정",
		SourceURL:    "https://example.test/rule",
		CollectedAt:  time.Now().UTC(),
		ContentHash:  "hash-rule-1",
		DocumentType: model.DocumentTypeRule,
		Body:         "상장 심사",
		Attachments: []model.Attachment{{
			ID:          "att-1",
			Title:       "별표",
			FileName:    "별표.hwp",
			Status:      model.AttachmentConverted,
			TextPath:    attachmentTextPath,
			ContentHash: "raw-hash",
		}},
	}
	if _, err := corpus.WriteDocument(root, doc); err != nil {
		t.Fatalf("write document: %v", err)
	}
	fullTextPath := filepath.Join(root, attachmentTextPath)
	if err := os.MkdirAll(filepath.Dir(fullTextPath), 0o755); err != nil {
		t.Fatalf("mkdir attachment dir: %v", err)
	}
	if err := os.WriteFile(fullTextPath, []byte("old formula"), 0o644); err != nil {
		t.Fatalf("write attachment text: %v", err)
	}
	snap, _, err := searchindex.BuildSnapshot(root)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	path := filepath.Join(root, "index", "bm25.krxidx")
	if err := searchindex.WriteSnapshot(path, snap); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	if err := os.WriteFile(fullTextPath, []byte("new formula with hwp equation"), 0o644); err != nil {
		t.Fatalf("rewrite attachment text: %v", err)
	}
	updated, _, err := searchindex.BuildSnapshot(root)
	if err != nil {
		t.Fatalf("rebuild snapshot: %v", err)
	}
	if bm25Current(path, updated) {
		t.Fatal("expected BM25 index to be stale after converted attachment text changed")
	}
}

func TestVectorFreshIncludesPrefixMetadata(t *testing.T) {
	root := writeCommandTestCorpus(t)
	snap, _, err := searchindex.BuildSnapshot(root)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	path := filepath.Join(root, "index", "vectors.krxvec")
	if err := searchindex.WriteVectorSnapshot(path, snap, map[string][]float64{"rule-1#0": {1, 0}}, "test-model", 2); err != nil {
		t.Fatalf("write vector snapshot: %v", err)
	}
	if err := searchindex.WriteVectorMetadata(searchindex.VectorMetadataPath(path), searchindex.VectorMetadata{
		CorpusHash:     snap.CorpusHash,
		Model:          "test-model",
		Dimensions:     2,
		QueryPrefix:    "query: ",
		DocumentPrefix: "passage: ",
	}); err != nil {
		t.Fatalf("write vector metadata: %v", err)
	}
	embedder := &searchindex.OpenAIEmbedder{Model: "test-model", Dimensions: 2}
	if !vectorFresh(path, snap, embedder) {
		t.Fatal("expected vector index to be fresh")
	}
	t.Setenv("KRX_EMBEDDING_DOCUMENT_PREFIX", "doc: ")
	if vectorFresh(path, snap, embedder) {
		t.Fatal("expected vector index to be stale after prefix change")
	}
}

func writeCommandTestCorpus(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	doc := model.Document{
		ID:           "rule-1",
		Title:        "상장규정",
		SourceURL:    "https://example.test/rule",
		CollectedAt:  time.Now().UTC(),
		ContentHash:  "hash-rule-1",
		DocumentType: model.DocumentTypeRule,
		Body:         "상장 심사와 공시 의무",
	}
	if _, err := corpus.WriteDocument(root, doc); err != nil {
		t.Fatalf("write document: %v", err)
	}
	return root
}
