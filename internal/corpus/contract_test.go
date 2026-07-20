package corpus

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/model"
	"gopkg.in/yaml.v3"
)

func TestLoadAcceptsLegacyDocumentContentHash(t *testing.T) {
	root := t.TempDir()
	doc := contractDocument("legacy", "Legacy Rule")
	doc.ContentHash = model.HashText(doc.Title + "\n" + doc.Body)
	writeContractMarkdown(t, root, doc, false)
	if _, err := Load(root); err != nil {
		t.Fatalf("Load legacy corpus: %v", err)
	}
}

func TestLoadRejectsStaleLegacyHashEvenWithV2BodyHash(t *testing.T) {
	root := t.TempDir()
	doc := contractDocument("v2", "V2 Rule")
	doc.SchemaVersion = IndexSourceSchemaVersion
	doc.ContentHash = model.HashText("stale legacy payload")
	doc.BodyHash = model.HashText(doc.Body)
	writeContractMarkdown(t, root, doc, true)
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "content_hash_mismatch") {
		t.Fatalf("Load error = %v, want content_hash_mismatch", err)
	}
}

func TestLoadRejectsUnsafePublicSourceAndFileMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.Document)
		want   string
	}{
		{name: "file URL", mutate: func(doc *model.Document) { doc.SourceURL = "file:///home/private/source.html" }, want: "absolute HTTP(S)"},
		{name: "local path URL", mutate: func(doc *model.Document) { doc.SourceURL = "/home/private/source.html" }, want: "absolute HTTP(S)"},
		{name: "credential URL", mutate: func(doc *model.Document) { doc.SourceURL = "https://user:pass@example.test/rule" }, want: "without credentials"},
		{name: "document path filename", mutate: func(doc *model.Document) { doc.FileName = "../../private/source.html" }, want: "portable basename"},
		{name: "attachment local URL", mutate: func(doc *model.Document) {
			doc.Attachments = []model.Attachment{{ID: "unsafe-url", Status: model.AttachmentPending, SourceURL: "/home/private/file"}}
		}, want: "supported KRX endpoint"},
		{name: "attachment path filename", mutate: func(doc *model.Document) {
			doc.Attachments = []model.Attachment{{ID: "unsafe-file", Status: model.AttachmentPending, FileName: `C:\private\file.hwp`}}
		}, want: "portable basename"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			doc := contractDocument("public-metadata", "Public Metadata Rule")
			test.mutate(&doc)
			writeContractMarkdown(t, root, doc, true)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadAcceptsV2BodyHashWithoutLegacyHash(t *testing.T) {
	root := t.TempDir()
	doc := contractDocument("v2", "V2 Rule")
	doc.SchemaVersion = IndexSourceSchemaVersion
	doc.ContentHash = ""
	doc.BodyHash = model.HashText(doc.Body)
	writeContractMarkdown(t, root, doc, true)
	if _, err := Load(root); err != nil {
		t.Fatalf("Load v2 corpus: %v", err)
	}
}

func TestLoadAllowsEmptyNonSearchableBodyWithValidatedAttachmentFallback(t *testing.T) {
	root := t.TempDir()
	searchableDocument := false
	searchableAttachment := true
	doc := contractDocument("attachment-fallback", "Attachment Fallback Rule")
	doc.Body = ""
	doc.BodyHash = model.HashText("")
	doc.ConversionStatus = string(model.AttachmentConverted)
	doc.Searchable = &searchableDocument
	doc.QualityStatus = "warn"
	doc.QualityCodes = []string{"document_empty_body"}
	attachmentText := "검색 가능한 PDF 첨부 본문"
	doc.Attachments = []model.Attachment{{
		ID: "fallback-pdf", Title: "개정안 PDF", FileName: "fallback.pdf",
		Status: model.AttachmentConverted, Searchable: &searchableAttachment,
		TextPath: attachmentRelativePath(doc, "fallback.md"), ConvertedTextHash: model.HashText(attachmentText),
	}}
	writeContractAttachment(t, root, doc, "fallback.md", attachmentText)
	writeContractMarkdown(t, root, doc, true)
	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("Load attachment fallback: %v", err)
	}
	if len(loaded.Documents) != 1 || loaded.AttachmentTexts["fallback-pdf"] != attachmentText {
		t.Fatalf("loaded fallback corpus = %#v", loaded)
	}
}

func TestLoadRejectsEmptyBodyWithoutCompleteAttachmentFallbackContract(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.Document)
		want   string
	}{
		{name: "searchable omitted", mutate: func(doc *model.Document) { doc.Searchable = nil }, want: "explicit searchable=false"},
		{name: "searchable true", mutate: func(doc *model.Document) { value := true; doc.Searchable = &value }, want: "explicit searchable=false"},
		{name: "quality not warn", mutate: func(doc *model.Document) { doc.QualityStatus = "ok" }, want: "quality_status=warn"},
		{name: "quality code missing", mutate: func(doc *model.Document) { doc.QualityCodes = nil }, want: "document_empty_body quality code"},
		{name: "conversion failed", mutate: func(doc *model.Document) { doc.ConversionStatus = string(model.AttachmentFailed) }, want: "required_conversion_failed"},
		{name: "attachment absent", mutate: func(doc *model.Document) { doc.Attachments = nil }, want: "validated converted searchable attachment"},
		{name: "attachment not searchable", mutate: func(doc *model.Document) { value := false; doc.Attachments[0].Searchable = &value }, want: "validated converted searchable attachment"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			documentSearchable := false
			attachmentSearchable := true
			doc := contractDocument("empty-invalid", "Empty Invalid Rule")
			doc.Body = ""
			doc.BodyHash = model.HashText("")
			doc.ConversionStatus = string(model.AttachmentConverted)
			doc.Searchable = &documentSearchable
			doc.QualityStatus = "warn"
			doc.QualityCodes = []string{"document_empty_body"}
			attachmentText := "fallback text"
			doc.Attachments = []model.Attachment{{
				ID: "fallback", Status: model.AttachmentConverted, Searchable: &attachmentSearchable,
				TextPath: attachmentRelativePath(doc, "fallback.md"), ConvertedTextHash: model.HashText(attachmentText),
			}}
			test.mutate(&doc)
			if len(doc.Attachments) > 0 {
				writeContractAttachment(t, root, doc, "fallback.md", attachmentText)
			}
			writeContractMarkdown(t, root, doc, true)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadValidatesAndPopulatesSanitizedSourceProvenance(t *testing.T) {
	root := t.TempDir()
	doc := contractDocument("source", "Source Rule")
	doc.SchemaVersion = IndexSourceSchemaVersion
	source := "<html><body>official rule content</body></html>\n"
	sourceHash := model.HashText(source)
	contentPath, requestPath := sourceProvenancePaths(doc)
	doc.SourceContentHash = sourceHash
	doc.SourceContentPath = contentPath
	doc.SourceRequestPath = requestPath
	writeFile(t, filepath.Join(root, contentPath), source)
	requestJSON, err := json.Marshal(map[string]string{
		"endpoint":            "/out/regulation/regulationViewPop.do",
		"bookid":              "12345",
		"noformyn":            "N",
		"statehistoryid":      "67890",
		"source_content_hash": sourceHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, requestPath), string(requestJSON))
	writeContractMarkdown(t, root, doc, true)

	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Documents) != 1 {
		t.Fatalf("documents = %d", len(loaded.Documents))
	}
	request := loaded.Documents[0].SourceRequest
	if request.Endpoint != "/out/regulation/regulationViewPop.do" || request.BookID != "12345" || request.NoFormYN != "N" || request.StateHistoryID != "67890" || request.SourceContentHash != sourceHash {
		t.Fatalf("source request descriptor = %#v", request)
	}
	encoded, err := json.Marshal(loaded.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "source_request\"") || strings.Contains(string(encoded), "statehistoryid") {
		t.Fatalf("runtime source request leaked through internal document JSON: %s", encoded)
	}
}

func TestLoadRejectsUnsafeOrMalformedSourceRequestDescriptors(t *testing.T) {
	tests := []struct {
		name    string
		request func(string) []byte
		want    string
	}{
		{
			name: "recursive secret",
			request: func(hash string) []byte {
				return []byte(strings.ReplaceAll(`{"endpoint":"/out/regulation/regulationViewPop.do","bookid":"1","noformyn":"N","source_content_hash":"__HASH__","metadata":{"Authorization":"Bearer secret"}}`, "__HASH__", hash))
			},
			want: "secret field",
		},
		{
			name: "duplicate key",
			request: func(hash string) []byte {
				return []byte(strings.ReplaceAll(`{"endpoint":"/out/regulation/regulationViewPop.do","endpoint":"/out/regulation/regulationViewPop.do","bookid":"1","noformyn":"N","source_content_hash":"__HASH__"}`, "__HASH__", hash))
			},
			want: "duplicate field",
		},
		{
			name: "trailing value",
			request: func(hash string) []byte {
				return []byte(strings.ReplaceAll(`{"endpoint":"/out/regulation/regulationViewPop.do","bookid":"1","noformyn":"N","source_content_hash":"__HASH__"} {}`, "__HASH__", hash))
			},
			want: "trailing JSON value",
		},
		{
			name: "unknown field",
			request: func(hash string) []byte {
				return []byte(strings.ReplaceAll(`{"endpoint":"/out/regulation/regulationViewPop.do","bookid":"1","noformyn":"N","source_content_hash":"__HASH__","headers":"forbidden"}`, "__HASH__", hash))
			},
			want: "unknown field",
		},
		{
			name: "invalid UTF-8",
			request: func(string) []byte {
				return []byte{'{', '"', 0xff, '"', ':', '1', '}'}
			},
			want: "not valid UTF-8",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			doc := contractDocument("unsafe-source", "Unsafe Source Rule")
			doc.SchemaVersion = IndexSourceSchemaVersion
			source := "<html>official</html>"
			sourceHash := model.HashText(source)
			contentPath, requestPath := sourceProvenancePaths(doc)
			doc.SourceContentHash = sourceHash
			doc.SourceContentPath = contentPath
			doc.SourceRequestPath = requestPath
			writeFile(t, filepath.Join(root, contentPath), source)
			requestFile := filepath.Join(root, requestPath)
			if err := os.MkdirAll(filepath.Dir(requestFile), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(requestFile, test.request(sourceHash), 0o644); err != nil {
				t.Fatal(err)
			}
			writeContractMarkdown(t, root, doc, true)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadRejectsSourceContentHashMismatchAndOversizedRequest(t *testing.T) {
	t.Run("content hash", func(t *testing.T) {
		root := t.TempDir()
		doc := contractDocument("source-hash", "Source Hash Rule")
		doc.SchemaVersion = IndexSourceSchemaVersion
		contentPath, requestPath := sourceProvenancePaths(doc)
		doc.SourceContentHash = model.HashText("different")
		doc.SourceContentPath = contentPath
		doc.SourceRequestPath = requestPath
		writeFile(t, filepath.Join(root, contentPath), "actual")
		writeFile(t, filepath.Join(root, requestPath), `{}`)
		writeContractMarkdown(t, root, doc, true)
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "source_content_hash_mismatch") {
			t.Fatalf("Load error = %v", err)
		}
	})
	t.Run("request size", func(t *testing.T) {
		root := t.TempDir()
		doc := contractDocument("source-size", "Source Size Rule")
		doc.SchemaVersion = IndexSourceSchemaVersion
		source := "official"
		contentPath, requestPath := sourceProvenancePaths(doc)
		doc.SourceContentHash = model.HashText(source)
		doc.SourceContentPath = contentPath
		doc.SourceRequestPath = requestPath
		writeFile(t, filepath.Join(root, contentPath), source)
		writeFile(t, filepath.Join(root, requestPath), strings.Repeat(" ", maxSourceRequestBytes+1))
		writeContractMarkdown(t, root, doc, true)
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Fatalf("Load error = %v", err)
		}
	})
}

func TestLoadRejectsOversizedCorpusTextInputs(t *testing.T) {
	t.Run("document markdown", func(t *testing.T) {
		root := t.TempDir()
		path := writeContractMarkdown(t, root, contractDocument("large-document", "Large Document"), true)
		truncateFile(t, path, maxDocumentMarkdownBytes+1)
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Fatalf("Load error = %v, want document byte limit", err)
		}
	})

	t.Run("release manifest", func(t *testing.T) {
		root := t.TempDir()
		writeContractMarkdown(t, root, contractDocument("large-manifest", "Large Manifest"), true)
		manifestPath := filepath.Join(root, releaseManifestFile)
		writeFile(t, manifestPath, "{}")
		truncateFile(t, manifestPath, maxReleaseManifestBytes+1)
		if _, err := LoadWithOptions(root, LoadOptions{RequireManifest: true}); err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Fatalf("LoadWithOptions error = %v, want manifest byte limit", err)
		}
	})

	t.Run("document text", func(t *testing.T) {
		root := t.TempDir()
		doc := contractDocument("large-document-text", "Large Document Text")
		doc.TextPath = attachmentRelativePath(doc, "document.md")
		path := filepath.Join(root, doc.TextPath)
		writeFile(t, path, doc.Body)
		truncateFile(t, path, maxDocumentTextBytes+1)
		writeContractMarkdown(t, root, doc, true)
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Fatalf("Load error = %v, want document text byte limit", err)
		}
	})

	t.Run("attachment text", func(t *testing.T) {
		root := t.TempDir()
		doc := contractDocument("large-attachment-text", "Large Attachment Text")
		textPath := attachmentRelativePath(doc, "attachment.md")
		doc.Attachments = []model.Attachment{{
			ID: "large-attachment", Title: "Large Attachment", FileName: "attachment.hwp",
			Status: model.AttachmentConverted, TextPath: textPath,
			ConvertedTextHash: model.HashText("placeholder"),
		}}
		path := filepath.Join(root, textPath)
		writeFile(t, path, "placeholder")
		truncateFile(t, path, maxAttachmentTextBytes+1)
		writeContractMarkdown(t, root, doc, true)
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Fatalf("Load error = %v, want attachment text byte limit", err)
		}
	})
}

func TestLoadRejectsUnsafeSourceProvenancePaths(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, root string, doc *model.Document, contentPath string)
		want    string
	}{
		{
			name: "cross bundle",
			prepare: func(t *testing.T, root string, doc *model.Document, _ string) {
				doc.SourceContentPath = "ko/rules/other/raw/source.html"
				writeFile(t, filepath.Join(root, doc.SourceContentPath), "official")
			},
			want: "document bundle",
		},
		{
			name: "symlink",
			prepare: func(t *testing.T, root string, _ *model.Document, contentPath string) {
				target := filepath.Join(root, "outside-source.html")
				writeFile(t, target, "official")
				link := filepath.Join(root, contentPath)
				if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
			},
			want: "symlink",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			doc := contractDocument("source-path", "Source Path Rule")
			doc.SchemaVersion = IndexSourceSchemaVersion
			contentPath, requestPath := sourceProvenancePaths(doc)
			doc.SourceContentHash = model.HashText("official")
			doc.SourceContentPath = contentPath
			doc.SourceRequestPath = requestPath
			test.prepare(t, root, &doc, contentPath)
			request, err := json.Marshal(map[string]string{
				"endpoint":            "/out/regulation/regulationViewPop.do",
				"bookid":              "1",
				"noformyn":            "N",
				"source_content_hash": doc.SourceContentHash,
			})
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(root, requestPath), string(request))
			writeContractMarkdown(t, root, doc, true)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadRejectsDuplicateAttachmentID(t *testing.T) {
	root := t.TempDir()
	for i, title := range []string{"First Rule", "Second Rule"} {
		doc := contractDocument(string(rune('a'+i)), title)
		doc.Attachments = []model.Attachment{{
			ID:       "shared-attachment",
			Title:    "attachment",
			Status:   model.AttachmentConverted,
			TextPath: attachmentRelativePath(doc, "attachment.md"),
		}}
		writeContractAttachment(t, root, doc, "attachment.md", "converted text")
		writeContractMarkdown(t, root, doc, true)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "duplicate attachment id") {
		t.Fatalf("Load error = %v, want duplicate attachment id", err)
	}
}

func TestLoadRejectsDocumentAttachmentIDCollision(t *testing.T) {
	root := t.TempDir()
	first := contractDocument("shared-id", "First Rule")
	writeContractMarkdown(t, root, first, true)
	second := contractDocument("second", "Second Rule")
	second.Attachments = []model.Attachment{{
		ID:       first.ID,
		Title:    "attachment",
		Status:   model.AttachmentConverted,
		TextPath: attachmentRelativePath(second, "attachment.md"),
	}}
	writeContractAttachment(t, root, second, "attachment.md", "converted text")
	writeContractMarkdown(t, root, second, true)
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "duplicate attachment id") {
		t.Fatalf("Load error = %v, want global duplicate ID", err)
	}
}

func TestLoadRejectsInvalidStatusAndQualityCode(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.Document)
		want   string
	}{
		{
			name: "failed searchable document",
			mutate: func(doc *model.Document) {
				doc.ConversionStatus = "failed"
			},
			want: "invalid_status_combination",
		},
		{
			name: "failed quality searchable attachment",
			mutate: func(doc *model.Document) {
				doc.Attachments = []model.Attachment{{
					ID: "quality-fail", Status: model.AttachmentConverted, QualityStatus: "fail",
					TextPath: attachmentRelativePath(*doc, "quality.md"),
				}}
			},
			want: "invalid_status_combination",
		},
		{
			name: "deprecated quality code",
			mutate: func(doc *model.Document) {
				doc.QualityCodes = []string{"pdf_table_structure_lost"}
			},
			want: "unknown quality code",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			doc := contractDocument("status", "Status Rule")
			tc.mutate(&doc)
			if len(doc.Attachments) > 0 {
				writeContractAttachment(t, root, doc, "quality.md", "quality text")
			}
			writeContractMarkdown(t, root, doc, true)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadRejectsMissingOrFailedDocumentPreservation(t *testing.T) {
	for _, status := range []string{"missing", "failed"} {
		t.Run(status, func(t *testing.T) {
			root := t.TempDir()
			doc := contractDocument("preservation", "Preservation Rule")
			doc.PreservationStatus = status
			writeContractMarkdown(t, root, doc, true)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "required_source_missing") {
				t.Fatalf("Load error = %v, want required_source_missing", err)
			}
		})
	}
}

func TestReleaseManifestVerification(t *testing.T) {
	root := t.TempDir()
	doc := contractDocument("release", "Release Rule")
	doc.SchemaVersion = IndexSourceSchemaVersion
	path := writeContractMarkdown(t, root, doc, true)
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	indexHash, err := IndexSourceHash(loaded.Documents, loaded.AttachmentTexts)
	if err != nil {
		t.Fatal(err)
	}
	frontmatter, err := readFrontmatterMapping(path)
	if err != nil {
		t.Fatal(err)
	}
	relativePath, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	manifestDocument := make(map[string]any, len(frontmatter)+1)
	for key, value := range frontmatter {
		manifestDocument[key] = value
	}
	manifestDocument["path"] = filepath.ToSlash(relativePath)
	payload := map[string]any{
		"schema_version":    IndexSourceSchemaVersion,
		"version":           "test",
		"generated_at":      "2026-07-10T00:00:00Z",
		"release_profile":   map[string]any{"version": 1, "default": "strict", "allowed_failure_ids": []any{}},
		"documents":         []any{manifestDocument},
		"index_source_hash": indexHash,
	}
	payload["release_hash"], err = releaseHash(payload)
	if err != nil {
		t.Fatal(err)
	}
	writeManifestFixture(t, root, payload)
	verified, err := LoadWithOptions(root, LoadOptions{RequireManifest: true})
	if err != nil {
		t.Fatalf("LoadWithOptions release: %v", err)
	}
	if verified.ReleaseHash != payload["release_hash"] {
		t.Fatalf("release hash = %q, want %q", verified.ReleaseHash, payload["release_hash"])
	}

	originalTitle := manifestDocument["title"]
	manifestDocument["title"] = "tampered manifest title"
	refreshReleaseHash(t, payload)
	writeManifestFixture(t, root, payload)
	if _, err := LoadWithOptions(root, LoadOptions{RequireManifest: true}); err == nil || !strings.Contains(err.Error(), "manifest_metadata_mismatch") {
		t.Fatalf("metadata parity error = %v", err)
	}
	manifestDocument["title"] = originalTitle
	payload["documents"] = []any{}
	refreshReleaseHash(t, payload)
	writeManifestFixture(t, root, payload)
	if _, err := LoadWithOptions(root, LoadOptions{RequireManifest: true}); err == nil || !strings.Contains(err.Error(), "document count") {
		t.Fatalf("missing manifest entry error = %v", err)
	}
	extra := map[string]any{"schema_version": IndexSourceSchemaVersion, "id": "extra", "path": "ko/rules/extra/index.md"}
	payload["documents"] = []any{manifestDocument, extra}
	refreshReleaseHash(t, payload)
	writeManifestFixture(t, root, payload)
	if _, err := LoadWithOptions(root, LoadOptions{RequireManifest: true}); err == nil || !strings.Contains(err.Error(), "document count") {
		t.Fatalf("extra manifest entry error = %v", err)
	}
	payload["documents"] = []any{manifestDocument}
	refreshReleaseHash(t, payload)

	payload["generated_at"] = "2099-01-01T00:00:00Z"
	writeManifestFixture(t, root, payload)
	if _, err := LoadWithOptions(root, LoadOptions{RequireManifest: true}); err != nil {
		t.Fatalf("operational generated_at changed release hash: %v", err)
	}

	payload["index_source_hash"] = strings.Repeat("a", 64)
	writeManifestFixture(t, root, payload)
	if _, err := LoadWithOptions(root, LoadOptions{RequireManifest: true}); err == nil || !strings.Contains(err.Error(), "index_source_hash_mismatch") {
		t.Fatalf("index hash error = %v", err)
	}

	payload["index_source_hash"] = indexHash
	payload["release_hash"] = strings.Repeat("b", 64)
	writeManifestFixture(t, root, payload)
	if _, err := LoadWithOptions(root, LoadOptions{RequireManifest: true}); err == nil || !strings.Contains(err.Error(), "release_hash_mismatch") {
		t.Fatalf("release hash error = %v", err)
	}
}

func TestStrictManifestParityAllowsCleanedRefreshFailureFields(t *testing.T) {
	root := t.TempDir()
	doc := contractDocument("refresh-parity", "Refresh Parity Rule")
	doc.SchemaVersion = IndexSourceSchemaVersion
	attachmentText := "converted attachment text"
	doc.Attachments = []model.Attachment{{
		ID:                     "refresh-parity-attachment",
		Title:                  "Refresh Parity Attachment",
		FileName:               "attachment.pdf",
		Status:                 model.AttachmentConverted,
		PreservationStatus:     "preserved",
		TextPath:               attachmentRelativePath(doc, "attachment.md"),
		ConvertedTextHash:      model.HashText(attachmentText),
		ConvertedTextChars:     int64(len(attachmentText)),
		ConvertedNonSpaceChars: int64(len(strings.ReplaceAll(attachmentText, " ", ""))),
	}}
	writeContractAttachment(t, root, doc, "attachment.md", attachmentText)
	path := writeContractMarkdown(t, root, doc, true)
	frontmatter, err := readFrontmatterMapping(path)
	if err != nil {
		t.Fatal(err)
	}
	attachments, ok := frontmatter["attachments"].([]any)
	if !ok || len(attachments) != 1 {
		t.Fatalf("frontmatter attachments = %#v", frontmatter["attachments"])
	}
	frontmatterAttachment, ok := attachments[0].(map[string]any)
	if !ok {
		t.Fatalf("frontmatter attachment = %#v", attachments[0])
	}
	frontmatterAttachment["last_refresh_error"] = "temporary upstream failure"
	frontmatterAttachment["last_refresh_failed_at"] = "2026-07-01T00:00:00Z"
	writeContractFrontmatterMapping(t, path, frontmatter, doc.Body)

	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("Load legacy frontmatter: %v", err)
	}
	indexHash, err := IndexSourceHash(loaded.Documents, loaded.AttachmentTexts)
	if err != nil {
		t.Fatal(err)
	}
	relativePath, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	manifestDocument := make(map[string]any, len(frontmatter)+1)
	for key, value := range frontmatter {
		manifestDocument[key] = value
	}
	manifestDocument["path"] = filepath.ToSlash(relativePath)
	manifestAttachment := manifestDocument["attachments"].([]any)[0].(map[string]any)
	delete(manifestAttachment, "last_refresh_error")
	delete(manifestAttachment, "last_refresh_failed_at")
	payload := map[string]any{
		"schema_version":    IndexSourceSchemaVersion,
		"version":           "cleaned-refresh-metadata",
		"release_profile":   map[string]any{"version": 1, "default": "strict", "allowed_failure_ids": []any{}},
		"documents":         []any{manifestDocument},
		"index_source_hash": indexHash,
	}
	refreshReleaseHash(t, payload)
	writeManifestFixture(t, root, payload)

	if _, err := LoadWithOptions(root, LoadOptions{RequireManifest: true}); err != nil {
		t.Fatalf("LoadWithOptions old frontmatter and cleaned manifest: %v", err)
	}

	manifestAttachment["last_checked_at"] = "2026-07-02T00:00:00Z"
	refreshReleaseHash(t, payload)
	writeManifestFixture(t, root, payload)
	if _, err := LoadWithOptions(root, LoadOptions{RequireManifest: true}); err == nil || !strings.Contains(err.Error(), "manifest_metadata_mismatch") {
		t.Fatalf("non-refresh operational metadata mismatch error = %v, want manifest_metadata_mismatch", err)
	}
}

func TestReleaseHashIgnoresRefreshFailureOperationalFieldsRecursively(t *testing.T) {
	payload := func(refreshError, refreshFailedAt string) map[string]any {
		return map[string]any{
			"schema_version": IndexSourceSchemaVersion,
			"documents": []any{
				map[string]any{
					"id": "rule",
					"attachments": []any{
						map[string]any{
							"id":                     "attachment",
							"status":                 "converted",
							"last_refresh_error":     refreshError,
							"last_refresh_failed_at": refreshFailedAt,
						},
					},
				},
			},
			"attachment_log": []any{
				map[string]any{
					"id": "attachment",
					"refresh": map[string]any{
						"last_refresh_error":     refreshError,
						"last_refresh_failed_at": refreshFailedAt,
					},
				},
			},
		}
	}

	first, err := releaseHash(payload("first failure", "2026-07-01T00:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	secondPayload := payload("second failure", "2026-07-02T00:00:00Z")
	second, err := releaseHash(secondPayload)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("refresh failure fields changed release hash: %s != %s", first, second)
	}

	secondPayload["attachment_log"].([]any)[0].(map[string]any)["id"] = "different-attachment"
	substantive, err := releaseHash(secondPayload)
	if err != nil {
		t.Fatal(err)
	}
	if first == substantive {
		t.Fatalf("substantive nested field did not change release hash: %s", first)
	}
}

func TestStrictLoadAcceptsLegacyV2RefreshFailureReleaseHash(t *testing.T) {
	root := t.TempDir()
	doc := contractDocument("legacy-release", "Legacy Release Rule")
	doc.SchemaVersion = IndexSourceSchemaVersion
	path := writeContractMarkdown(t, root, doc, true)
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	indexHash, err := IndexSourceHash(loaded.Documents, loaded.AttachmentTexts)
	if err != nil {
		t.Fatal(err)
	}
	frontmatter, err := readFrontmatterMapping(path)
	if err != nil {
		t.Fatal(err)
	}
	relativePath, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	manifestDocument := make(map[string]any, len(frontmatter)+1)
	for key, value := range frontmatter {
		manifestDocument[key] = value
	}
	manifestDocument["path"] = filepath.ToSlash(relativePath)
	refreshRecord := map[string]any{
		"id":                     "historical-refresh",
		"status":                 "converted",
		"last_refresh_error":     "temporary upstream failure",
		"last_refresh_failed_at": "2026-07-01T00:00:00Z",
	}
	payload := map[string]any{
		"schema_version":    IndexSourceSchemaVersion,
		"version":           "legacy-v2",
		"generated_at":      "2026-07-01T00:00:00Z",
		"release_profile":   map[string]any{"version": 1, "default": "strict", "allowed_failure_ids": []any{}},
		"documents":         []any{manifestDocument},
		"attachment_log":    []any{refreshRecord},
		"index_source_hash": indexHash,
	}
	legacyHash := legacyRefreshReleaseHashFixture(t, payload)
	payload["release_hash"] = legacyHash
	currentHash, err := releaseHash(payload)
	if err != nil {
		t.Fatal(err)
	}
	if currentHash == legacyHash {
		t.Fatalf("current release hash still includes legacy refresh failure fields: %s", currentHash)
	}
	writeManifestFixture(t, root, payload)

	verified, err := LoadWithOptions(root, LoadOptions{RequireManifest: true})
	if err != nil {
		t.Fatalf("LoadWithOptions legacy v2 release: %v", err)
	}
	if verified.ReleaseHash != legacyHash {
		t.Fatalf("release hash = %q, want declared legacy hash %q", verified.ReleaseHash, legacyHash)
	}

	refreshRecord["status"] = "failed"
	writeManifestFixture(t, root, payload)
	if _, err := LoadWithOptions(root, LoadOptions{RequireManifest: true}); err == nil || !strings.Contains(err.Error(), "release_hash_mismatch") {
		t.Fatalf("substantively tampered legacy release error = %v, want release_hash_mismatch", err)
	}
}

func TestReleaseModeRequiresManifest(t *testing.T) {
	root := t.TempDir()
	writeContractMarkdown(t, root, contractDocument("required", "Required Manifest Rule"), true)
	if _, err := LoadWithOptions(root, LoadOptions{RequireManifest: true}); err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("LoadWithOptions error = %v, want required manifest", err)
	}
}

func TestStrictReleaseProfileValidatesFailureAllowlist(t *testing.T) {
	searchableFalse := false
	failedPreserved := model.Attachment{
		ID:                 "failed-preserved",
		Status:             model.AttachmentFailed,
		PreservationStatus: "preserved",
		Searchable:         &searchableFalse,
	}
	documentWith := func(attachment model.Attachment) []model.Document {
		return []model.Document{{ID: "rule", Attachments: []model.Attachment{attachment}}}
	}
	payload := func(ids []any) map[string]any {
		return map[string]any{"release_profile": map[string]any{
			"version":             float64(1),
			"default":             "strict",
			"allowed_failure_ids": ids,
		}}
	}
	tests := []struct {
		name string
		docs []model.Document
		ids  []any
		want string
	}{
		{name: "valid", docs: documentWith(failedPreserved), ids: []any{"failed-preserved"}},
		{name: "non-string", docs: documentWith(failedPreserved), ids: []any{123.0}, want: "non-empty attachment ID strings"},
		{name: "empty", docs: documentWith(failedPreserved), ids: []any{""}, want: "non-empty attachment ID strings"},
		{name: "duplicate", docs: documentWith(failedPreserved), ids: []any{"failed-preserved", "failed-preserved"}, want: "duplicate"},
		{name: "unknown", docs: documentWith(failedPreserved), ids: []any{"unknown"}, want: "unknown attachment"},
		{name: "failed omitted", docs: documentWith(failedPreserved), ids: []any{}, want: "not in allowed_failure_ids"},
		{name: "not failed", docs: documentWith(model.Attachment{ID: "converted", Status: model.AttachmentConverted, PreservationStatus: "preserved", Searchable: &searchableFalse}), ids: []any{"converted"}, want: "must be failed, preserved"},
		{name: "not preserved", docs: documentWith(model.Attachment{ID: "failed", Status: model.AttachmentFailed, PreservationStatus: "failed", Searchable: &searchableFalse}), ids: []any{"failed"}, want: "must be failed, preserved"},
		{name: "searchable", docs: documentWith(model.Attachment{ID: "failed", Status: model.AttachmentFailed, PreservationStatus: "preserved"}), ids: []any{"failed"}, want: "searchable=false"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateStrictReleaseProfile(payload(test.ids), test.docs)
			if test.want == "" && err != nil {
				t.Fatalf("validateStrictReleaseProfile() error = %v", err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("validateStrictReleaseProfile() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadRejectsMissingConvertedText(t *testing.T) {
	root := t.TempDir()
	doc := contractDocument("missing", "Missing Text Rule")
	doc.Attachments = []model.Attachment{{
		ID:       "missing-attachment",
		Title:    "missing",
		Status:   model.AttachmentConverted,
		TextPath: attachmentRelativePath(doc, "missing.md"),
	}}
	writeContractMarkdown(t, root, doc, true)
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "text_path") {
		t.Fatalf("Load error = %v, want missing converted text", err)
	}
}

func TestLoadRejectsUnsafeAttachmentPaths(t *testing.T) {
	tests := []struct {
		name    string
		path    func(root string, doc model.Document) string
		prepare func(t *testing.T, root string, doc model.Document, path string)
		want    string
	}{
		{
			name: "absolute",
			path: func(root string, _ model.Document) string { return filepath.Join(root, "outside.md") },
			want: "absolute path",
		},
		{
			name: "parent",
			path: func(_ string, doc model.Document) string {
				return attachmentRelativePath(doc, "nested") + "/../outside.md"
			},
			want: "parent traversal",
		},
		{
			name: "cross bundle",
			path: func(_ string, _ model.Document) string { return "ko/rules/other/attachments/text.md" },
			prepare: func(t *testing.T, root string, _ model.Document, path string) {
				writeFile(t, filepath.Join(root, path), "other text")
			},
			want: "document bundle",
		},
		{
			name: "symlink",
			path: func(_ string, doc model.Document) string { return attachmentRelativePath(doc, "link.md") },
			prepare: func(t *testing.T, root string, doc model.Document, path string) {
				target := filepath.Join(root, "outside.md")
				writeFile(t, target, "outside")
				link := filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
			},
			want: "symlink",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			doc := contractDocument("unsafe", "Unsafe Rule")
			path := tc.path(root, doc)
			doc.Attachments = []model.Attachment{{
				ID:       "unsafe-attachment",
				Title:    "unsafe",
				Status:   model.AttachmentConverted,
				TextPath: path,
			}}
			if tc.prepare != nil {
				tc.prepare(t, root, doc, path)
			}
			writeContractMarkdown(t, root, doc, true)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadValidatesDocumentAndAttachmentAssets(t *testing.T) {
	root := t.TempDir()
	doc := contractDocument("asset-valid", "Asset Valid Rule")
	documentImage := testPNG(2, 3)
	documentAsset := testHTMLAsset(doc, "document-image", "inline/chart.png", documentImage, 2, 3)
	doc.Assets = []model.Asset{documentAsset}
	doc.Body = "Rule body\n\n![KRX 규정 이미지](assets/inline/chart.png)"
	doc.BodyHash = model.HashText(doc.Body)

	attachmentText := "첨부 본문\n\n![hwp:BinData/BIN0001.png](../assets/attachment-image.png)"
	attachmentImage := testPNG(4, 5)
	attachmentPath := assetRelativePath(doc, "attachment-image.png")
	searchable := true
	assetSearchable := false
	doc.Attachments = []model.Attachment{{
		ID: "asset-attachment", Title: "Asset Attachment", FileName: "attachment.hwp",
		Status: model.AttachmentConverted, Searchable: &searchable,
		TextPath: attachmentRelativePath(doc, "attachment.md"), ConvertedTextHash: model.HashText(attachmentText),
		Assets: []model.Asset{{
			ID: "attachment-image", SourceKind: assetSourceHWPBinData,
			SourceAnchor: "hwp:BinData/BIN0001.png", Path: attachmentPath,
			MIMEType: "image/png", RawFileHash: model.HashBytes(attachmentImage), Size: int64(len(attachmentImage)),
			Width: 4, Height: 5, PreservationStatus: "preserved", Searchable: &assetSearchable,
			QualityCodes: []string{"image_content_unindexed"},
		}},
	}}
	writeBytes(t, filepath.Join(root, documentAsset.Path), documentImage)
	writeBytes(t, filepath.Join(root, attachmentPath), attachmentImage)
	writeContractAttachment(t, root, doc, "attachment.md", attachmentText)
	writeContractMarkdown(t, root, doc, true)

	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.Documents[0]
	if len(got.Assets) != 1 || got.Assets[0].ReferencePath != "assets/inline/chart.png" {
		t.Fatalf("document assets = %#v", got.Assets)
	}
	if len(got.Attachments) != 1 || len(got.Attachments[0].Assets) != 1 || got.Attachments[0].Assets[0].ReferencePath != "../assets/attachment-image.png" {
		t.Fatalf("attachment assets = %#v", got.Attachments)
	}
}

func TestAssetMetadataDoesNotChangeIndexSourceHash(t *testing.T) {
	doc := contractDocument("asset-hash", "Asset Hash Rule")
	without, err := IndexSourceHash([]model.Document{doc}, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	doc.Assets = []model.Asset{testHTMLAsset(doc, "asset", "inline/chart.png", testPNG(2, 3), 2, 3)}
	with, err := IndexSourceHash([]model.Document{doc}, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if with != without {
		t.Fatalf("asset metadata changed index_source_hash: %s != %s", with, without)
	}
}

func TestLoadRejectsInvalidAssetContracts(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*model.Document, *model.Asset, *[]byte)
		prepare func(*testing.T, string, model.Document, model.Asset, []byte)
		want    string
	}{
		{name: "global id collision", mutate: func(doc *model.Document, asset *model.Asset, _ *[]byte) { asset.ID = doc.ID }, want: "duplicate_asset_id"},
		{name: "searchable omitted", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) { asset.Searchable = nil }, want: "searchable=false must be explicit"},
		{name: "searchable true", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) { value := true; asset.Searchable = &value }, want: "searchable=false"},
		{name: "parent path", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) { asset.Path = "../escape.png" }, want: "parent traversal"},
		{name: "cross bundle", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) { asset.Path = "ko/rules/other/assets/chart.png" }, prepare: func(t *testing.T, root string, _ model.Document, asset model.Asset, data []byte) {
			writeBytes(t, filepath.Join(root, asset.Path), data)
		}, want: "document bundle"},
		{name: "symlink", prepare: func(t *testing.T, root string, _ model.Document, asset model.Asset, data []byte) {
			target := filepath.Join(root, "outside.png")
			writeBytes(t, target, data)
			link := filepath.Join(root, asset.Path)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
		}, want: "symlink"},
		{name: "hash mismatch", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) { asset.RawFileHash = strings.Repeat("a", 64) }, want: "raw_file_hash_mismatch"},
		{name: "size mismatch", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) { asset.Size++ }, want: "asset size mismatch"},
		{name: "MIME mismatch", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) { asset.MIMEType = "image/gif" }, want: "MIME/signature mismatch"},
		{name: "dimension mismatch", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) { asset.Width++ }, want: "dimensions mismatch"},
		{name: "unsupported signature", mutate: func(_ *model.Document, asset *model.Asset, data *[]byte) {
			*data = []byte("not-an-image")
			asset.RawFileHash = model.HashBytes(*data)
			asset.Size = int64(len(*data))
		}, want: "unsupported or mismatched image signature"},
		{name: "dimension bound", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) { asset.Width = maxAssetDimension + 1 }, want: "dimensions exceed"},
		{name: "invalid source kind", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) { asset.SourceKind = "pdf_image" }, want: "invalid asset source_kind"},
		{name: "missing html URL", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) { asset.SourceURL = "" }, want: "source_url is required"},
		{name: "cross origin html URL", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) {
			asset.SourceURL = "https://evil.test/dataFile/law/img/chart.png"
			asset.SourceAnchor = "html-img:" + asset.SourceURL
		}, want: "source origin"},
		{name: "outside html image path", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) {
			asset.SourceURL = "https://example.test/private/chart.png"
			asset.SourceAnchor = "html-img:" + asset.SourceURL
		}, want: "/dataFile/law/img/"},
		{name: "anchor URL mismatch", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) {
			asset.SourceAnchor = "html-img:https://example.test/dataFile/law/img/other.png"
		}, want: "must equal html-img"},
		{name: "missing unindexed code", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) { asset.QualityCodes = nil }, want: "image_content_unindexed"},
		{name: "preserved error", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) { asset.Error = "unexpected" }, want: "must not contain error"},
		{name: "failed exposes metadata", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) {
			asset.PreservationStatus = "failed"
			asset.Error = "download failed"
			asset.QualityCodes = []string{"image_content_unindexed", "inline_image_missing"}
		}, want: "must not expose file metadata"},
		{name: "failed missing error", mutate: func(_ *model.Document, asset *model.Asset, _ *[]byte) {
			asset.PreservationStatus = "failed"
			asset.Path = ""
			asset.MIMEType = ""
			asset.RawFileHash = ""
			asset.Size = 0
			asset.Width = 0
			asset.Height = 0
			asset.QualityCodes = []string{"image_content_unindexed", "inline_image_missing"}
		}, want: "requires error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			doc := contractDocument("asset-invalid", "Asset Invalid Rule")
			data := testPNG(2, 3)
			asset := testHTMLAsset(doc, "asset-invalid-image", "inline/chart.png", data, 2, 3)
			if test.mutate != nil {
				test.mutate(&doc, &asset, &data)
			}
			doc.Assets = []model.Asset{asset}
			if test.prepare != nil {
				test.prepare(t, root, doc, asset, data)
			} else if asset.Path != "" && !filepath.IsAbs(asset.Path) && !strings.Contains(filepath.ToSlash(asset.Path), "../") {
				writeBytes(t, filepath.Join(root, asset.Path), data)
			}
			writeContractMarkdown(t, root, doc, true)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadRejectsInvalidHWPAssetSource(t *testing.T) {
	tests := []struct {
		name   string
		anchor string
		url    string
		want   string
	}{
		{name: "source URL", anchor: "hwp:BinData/BIN0001.png", url: "https://example.test/dataFile/law/img/a.png", want: "source_url must be empty"},
		{name: "absolute stream", anchor: "hwp:BinData//etc/passwd", want: "invalid stream path"},
		{name: "parent stream", anchor: "hwp:BinData/../secret", want: "invalid stream path"},
		{name: "wrong prefix", anchor: "hwp:BodyText/image.png", want: "must identify hwp:BinData"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			doc := contractDocument("hwp-asset", "HWP Asset Rule")
			data := testPNG(2, 3)
			searchable := false
			asset := model.Asset{
				ID: "hwp-image", SourceKind: assetSourceHWPBinData, SourceAnchor: test.anchor, SourceURL: test.url,
				Path: assetRelativePath(doc, "hwp.png"), MIMEType: "image/png", RawFileHash: model.HashBytes(data), Size: int64(len(data)), Width: 2, Height: 3,
				PreservationStatus: "preserved", Searchable: &searchable, QualityCodes: []string{"image_content_unindexed"},
			}
			doc.Assets = []model.Asset{asset}
			writeBytes(t, filepath.Join(root, asset.Path), data)
			writeContractMarkdown(t, root, doc, true)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestIndexSourceHashCanonicalAndMetadataSensitive(t *testing.T) {
	collected := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	first := model.Document{
		ID: "rule", Title: "Caf\u00e9", Category: "Category", SourceURL: "https://example.test/rule",
		CollectedAt: collected, DocumentType: model.DocumentTypeRule, Language: model.LanguageEnglish,
		Body: "  line 1\r\nCafe\u0301  ",
	}
	second := first
	second.Title = "Cafe\u0301"
	second.Body = "line 1\nCaf\u00e9"
	firstHash, err := IndexSourceHash([]model.Document{first}, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := IndexSourceHash([]model.Document{second}, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("canonical hashes differ: %s != %s", firstHash, secondHash)
	}
	second.Category = "Changed"
	changedHash, err := IndexSourceHash([]model.Document{second}, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if changedHash == firstHash {
		t.Fatal("index source hash did not change with category")
	}
}

func TestSharedCorpusContractV2Fixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "corpus_contract_v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Documents     []model.Document `json:"documents"`
		NegativeCases []struct {
			Name      string `json:"name"`
			Entity    string `json:"entity"`
			Overrides struct {
				PreservationStatus string `json:"preservation_status"`
			} `json:"overrides"`
			ExpectedError string `json:"expected_error"`
		} `json:"negative_cases"`
		AssetFixture struct {
			FileBase64    string `json:"file_base64"`
			NegativeCases []struct {
				Name          string         `json:"name"`
				Remove        []string       `json:"remove"`
				Overrides     map[string]any `json:"overrides"`
				ExpectedError string         `json:"expected_error"`
			} `json:"negative_cases"`
		} `json:"asset_fixture"`
		Expected struct {
			CanonicalBody            string `json:"canonical_body"`
			BodyHash                 string `json:"body_hash"`
			LegacyContentHash        string `json:"legacy_content_hash"`
			IndexSourceCanonicalJSON string `json:"index_source_canonical_json"`
			IndexSourceHash          string `json:"index_source_hash"`
		} `json:"expected"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	var textFixture struct {
		Documents []struct {
			Attachments []struct {
				ID            string `json:"id"`
				ConvertedText string `json:"converted_text"`
			} `json:"attachments"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(data, &textFixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Documents) != 1 || len(textFixture.Documents) != 1 {
		t.Fatalf("unexpected fixture document count")
	}
	doc := fixture.Documents[0]
	if got := model.CanonicalText(doc.Body); got != fixture.Expected.CanonicalBody {
		t.Fatalf("canonical body = %q, want %q", got, fixture.Expected.CanonicalBody)
	}
	if got := model.HashText(doc.Body); got != fixture.Expected.BodyHash {
		t.Fatalf("body hash = %s, want %s", got, fixture.Expected.BodyHash)
	}
	if got := model.HashText(doc.Title + "\n" + doc.Body); got != fixture.Expected.LegacyContentHash {
		t.Fatalf("legacy content hash = %s, want %s", got, fixture.Expected.LegacyContentHash)
	}
	attachmentTexts := map[string]string{}
	for _, attachment := range textFixture.Documents[0].Attachments {
		attachmentTexts[attachment.ID] = attachment.ConvertedText
	}
	canonical, err := CanonicalIndexSource(fixture.Documents, attachmentTexts)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != fixture.Expected.IndexSourceCanonicalJSON {
		t.Fatalf("canonical index source mismatch\n got: %s\nwant: %s", canonical, fixture.Expected.IndexSourceCanonicalJSON)
	}
	hash, err := IndexSourceHash(fixture.Documents, attachmentTexts)
	if err != nil {
		t.Fatal(err)
	}
	if hash != fixture.Expected.IndexSourceHash {
		t.Fatalf("index source hash = %s, want %s", hash, fixture.Expected.IndexSourceHash)
	}
	if len(doc.Assets) != 1 {
		t.Fatalf("shared fixture assets = %#v", doc.Assets)
	}
	assetBytes, err := base64.StdEncoding.DecodeString(fixture.AssetFixture.FileBase64)
	if err != nil {
		t.Fatal(err)
	}
	validAssetRoot := t.TempDir()
	validAssetDocument := contractDocument("rule-1", "Café 규정")
	validAssetDocument.Assets = []model.Asset{doc.Assets[0]}
	writeBytes(t, filepath.Join(validAssetRoot, doc.Assets[0].Path), assetBytes)
	writeContractMarkdown(t, validAssetRoot, validAssetDocument, true)
	if _, err := Load(validAssetRoot); err != nil {
		t.Fatalf("Load shared valid asset fixture: %v", err)
	}
	for _, negative := range fixture.AssetFixture.NegativeCases {
		t.Run(negative.Name, func(t *testing.T) {
			root := t.TempDir()
			testDoc := contractDocument("rule-1", "Café 규정")
			asset := doc.Assets[0]
			for _, field := range negative.Remove {
				if field == "searchable" {
					asset.Searchable = nil
				}
			}
			for field, raw := range negative.Overrides {
				switch field {
				case "path":
					asset.Path, _ = raw.(string)
				case "raw_file_hash":
					asset.RawFileHash, _ = raw.(string)
				case "mime_type":
					asset.MIMEType, _ = raw.(string)
				case "source_anchor":
					asset.SourceAnchor, _ = raw.(string)
				case "source_url":
					asset.SourceURL, _ = raw.(string)
				case "error":
					asset.Error, _ = raw.(string)
				case "quality_codes":
					asset.QualityCodes = nil
					for _, item := range raw.([]any) {
						if value, ok := item.(string); ok {
							asset.QualityCodes = append(asset.QualityCodes, value)
						}
					}
				}
			}
			testDoc.Assets = []model.Asset{asset}
			if !strings.Contains(filepath.ToSlash(asset.Path), "../") {
				writeBytes(t, filepath.Join(root, asset.Path), assetBytes)
			}
			writeContractMarkdown(t, root, testDoc, true)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), negative.ExpectedError) {
				t.Fatalf("Load shared asset negative error = %v, want %q", err, negative.ExpectedError)
			}
		})
	}
	for _, negative := range fixture.NegativeCases {
		t.Run(negative.Name, func(t *testing.T) {
			if negative.Entity != "document" || negative.Overrides.PreservationStatus == "" {
				t.Fatalf("unsupported shared negative fixture: %#v", negative)
			}
			root := t.TempDir()
			doc := contractDocument("fixture-negative", "Fixture Negative Rule")
			doc.PreservationStatus = negative.Overrides.PreservationStatus
			writeContractMarkdown(t, root, doc, true)
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), negative.ExpectedError) {
				t.Fatalf("Load error = %v, want %q", err, negative.ExpectedError)
			}
		})
	}
}

func contractDocument(id, title string) model.Document {
	body := "Rule body"
	return model.Document{
		ID:           id,
		Title:        title,
		SourceURL:    "https://example.test/" + id,
		CollectedAt:  time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC),
		DocumentType: model.DocumentTypeRule,
		Language:     model.LanguageKorean,
		Body:         body,
		BodyHash:     model.HashText(body),
	}
}

func writeContractMarkdown(t *testing.T, root string, doc model.Document, v2 bool) string {
	t.Helper()
	path := filepath.Join(root, doc.Language, "rules", model.Slug(doc.Title), "index.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := doc
	meta.Body = ""
	meta.Path = ""
	if !v2 {
		meta.BodyHash = ""
	}
	var out bytes.Buffer
	out.WriteString("---\n")
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(meta); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	out.WriteString("---\n\n")
	out.WriteString(doc.Body)
	out.WriteString("\n")
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeContractFrontmatterMapping(t *testing.T, path string, frontmatter map[string]any, body string) {
	t.Helper()
	var out bytes.Buffer
	out.WriteString("---\n")
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(frontmatter); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	out.WriteString("---\n\n")
	out.WriteString(body)
	out.WriteString("\n")
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func attachmentRelativePath(doc model.Document, name string) string {
	return filepath.ToSlash(filepath.Join(doc.Language, "rules", model.Slug(doc.Title), "attachments", name))
}

func assetRelativePath(doc model.Document, name string) string {
	return filepath.ToSlash(filepath.Join(doc.Language, "rules", model.Slug(doc.Title), "assets", name))
}

func testHTMLAsset(doc model.Document, id, name string, data []byte, width, height int64) model.Asset {
	searchable := false
	sourceURL := "https://example.test/dataFile/law/img/" + filepath.Base(name)
	return model.Asset{
		ID: id, SourceKind: assetSourceHTMLInline, SourceAnchor: "html-img:" + sourceURL, SourceURL: sourceURL,
		Path: assetRelativePath(doc, name), MIMEType: "image/png", RawFileHash: model.HashBytes(data),
		Size: int64(len(data)), Width: width, Height: height, PreservationStatus: "preserved", Searchable: &searchable,
		QualityCodes: []string{"image_content_unindexed"},
	}
}

func testPNG(width, height uint32) []byte {
	data := []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
		0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	binary.BigEndian.PutUint32(data[16:20], width)
	binary.BigEndian.PutUint32(data[20:24], height)
	return data
}

func sourceProvenancePaths(doc model.Document) (string, string) {
	bundle := filepath.Join(doc.Language, "rules", model.Slug(doc.Title), "raw")
	return filepath.ToSlash(filepath.Join(bundle, "source.html")), filepath.ToSlash(filepath.Join(bundle, "request.json"))
}

func writeContractAttachment(t *testing.T, root string, doc model.Document, name, content string) {
	t.Helper()
	writeFile(t, filepath.Join(root, attachmentRelativePath(doc, name)), content)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func truncateFile(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
}

func writeBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeManifestFixture(t *testing.T, root string, payload map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(root, releaseManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func refreshReleaseHash(t *testing.T, payload map[string]any) {
	t.Helper()
	hash, err := releaseHash(payload)
	if err != nil {
		t.Fatal(err)
	}
	payload["release_hash"] = hash
}

func legacyRefreshReleaseHashFixture(t *testing.T, payload map[string]any) string {
	t.Helper()
	legacyOperationalFields := map[string]struct{}{
		"release_hash":         {},
		"generated_at":         {},
		"last_checked_at":      {},
		"source_response_hash": {},
	}
	var scrub func(any) any
	scrub = func(value any) any {
		switch typed := value.(type) {
		case map[string]any:
			out := make(map[string]any, len(typed))
			for key, item := range typed {
				if _, excluded := legacyOperationalFields[key]; excluded {
					continue
				}
				out[key] = scrub(item)
			}
			return out
		case []any:
			out := make([]any, len(typed))
			for i, item := range typed {
				out[i] = scrub(item)
			}
			return out
		default:
			return typed
		}
	}
	hash, err := canonicalJSONHash(scrub(payload))
	if err != nil {
		t.Fatal(err)
	}
	return hash
}
