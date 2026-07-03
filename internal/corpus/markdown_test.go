package corpus

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

func TestParseRenderedMarkdown(t *testing.T) {
	doc := model.Document{
		ID:            "210207961",
		Title:         "코스닥시장 상장규정",
		Category:      "업무규정 / 코스닥시장규정",
		SourceURL:     "https://rule.krx.co.kr/out/regulation/regulationViewPop.do",
		EffectiveDate: "2026-07-01",
		PublishedDate: "2026-05-13",
		CollectedAt:   time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC),
		DocumentType:  model.DocumentTypeRule,
		Language:      model.LanguageKorean,
		Body:          "# 제1조\n\n이 규정은 상장에 관하여 필요한 사항을 정한다.",
	}
	data := renderTestMarkdown(t, doc)
	if !strings.HasPrefix(string(data), "---\n") {
		t.Fatalf("frontmatter missing:\n%s", data)
	}
	parsed, err := ParseMarkdown(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ID != doc.ID || parsed.Title != doc.Title {
		t.Fatalf("parsed metadata mismatch: %#v", parsed)
	}
	if parsed.Language != model.LanguageKorean {
		t.Fatalf("language = %q, want ko", parsed.Language)
	}
	if !strings.Contains(parsed.Body, "제1조") {
		t.Fatalf("body not preserved: %q", parsed.Body)
	}
	if parsed.ContentHash == "" {
		t.Fatal("content_hash should be generated")
	}
}

func TestWriteDocumentUsesLanguageDirectory(t *testing.T) {
	root := t.TempDir()
	doc := model.Document{
		ID:           "rule-1-en",
		Title:        "KOSPI Market Listing Regulation",
		SourceURL:    "https://example.test/rule",
		CollectedAt:  time.Now().UTC(),
		ContentHash:  "hash",
		DocumentType: model.DocumentTypeRule,
		Language:     model.LanguageEnglish,
		SourceID:     "rule-1",
		Body:         "Article 1 Purpose",
	}
	path := writeTestDocument(t, root, doc)
	want := filepath.Join(root, "en", "rules", "kospi-market-listing-regulation", "index.md")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	loaded, err := LoadDocuments(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Language != model.LanguageEnglish || loaded[0].SourceID != "rule-1" {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestLoadDocumentsIgnoresBundleAttachmentMarkdown(t *testing.T) {
	root := t.TempDir()
	doc := model.Document{
		ID:           "rule-1",
		Title:        "Bundle Rule",
		SourceURL:    "https://example.test/rule",
		CollectedAt:  time.Now().UTC(),
		ContentHash:  "hash",
		DocumentType: model.DocumentTypeRule,
		Language:     model.LanguageEnglish,
		Body:         "Article 1 Purpose",
	}
	path := writeTestDocument(t, root, doc)
	attachmentPath := filepath.Join(filepath.Dir(path), "attachments", "appendix.md")
	if err := os.MkdirAll(filepath.Dir(attachmentPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attachmentPath, []byte("converted attachment"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDocuments(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d documents, want 1: %#v", len(loaded), loaded)
	}
	if loaded[0].ID != "rule-1" {
		t.Fatalf("loaded wrong document: %#v", loaded[0])
	}
}

func TestLoadDocumentsRejectsDuplicateIDs(t *testing.T) {
	root := t.TempDir()
	first := model.Document{
		ID:           "rule-1",
		Title:        "First Rule",
		SourceURL:    "https://example.test/first",
		CollectedAt:  time.Now().UTC(),
		ContentHash:  "first",
		DocumentType: model.DocumentTypeRule,
		Language:     model.LanguageKorean,
		Body:         "first body",
	}
	second := first
	second.Title = "Second Rule"
	second.SourceURL = "https://example.test/second"
	second.ContentHash = "second"
	second.Body = "second body"
	writeTestDocument(t, root, first)
	writeTestDocument(t, root, second)
	if _, err := LoadDocuments(root); err == nil || !strings.Contains(err.Error(), "duplicate document id") {
		t.Fatalf("LoadDocuments error = %v, want duplicate document id", err)
	}
}

func writeTestDocument(t *testing.T, root string, doc model.Document) string {
	t.Helper()
	folder := "rules"
	if doc.DocumentType == model.DocumentTypeNotice {
		folder = "notices"
	}
	path := filepath.Join(root, model.NormalizeLanguage(doc.Language), folder, model.Slug(doc.Title), "index.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, renderTestMarkdown(t, doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func renderTestMarkdown(t *testing.T, doc model.Document) []byte {
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
