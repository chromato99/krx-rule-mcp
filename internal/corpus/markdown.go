package corpus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/chromato99/krx-rule-mcp/internal/model"
	"gopkg.in/yaml.v3"
)

var errFrontmatter = errors.New("missing YAML frontmatter")

const maxDocumentMarkdownBytes int64 = 16 << 20

func ParseMarkdown(data []byte) (model.Document, error) {
	if !utf8.Valid(data) {
		return model.Document{}, fmt.Errorf("document is not valid UTF-8")
	}
	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		return model.Document{}, errFrontmatter
	}
	rest := text[len("---\n"):]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return model.Document{}, errFrontmatter
	}
	front := rest[:idx]
	body := model.CanonicalText(rest[idx+len("\n---"):])
	var doc model.Document
	if err := yaml.Unmarshal([]byte(front), &doc); err != nil {
		return model.Document{}, err
	}
	doc.Body = body
	if doc.ID == "" {
		return model.Document{}, fmt.Errorf("id is required")
	}
	if doc.Title == "" {
		return model.Document{}, fmt.Errorf("title is required")
	}
	if doc.DocumentType == "" {
		return model.Document{}, fmt.Errorf("document_type is required")
	}
	doc.Language = strings.ToLower(strings.TrimSpace(doc.Language))
	if doc.Language != model.LanguageKorean && doc.Language != model.LanguageEnglish {
		return model.Document{}, fmt.Errorf("language must be ko or en")
	}
	if doc.DocumentType != model.DocumentTypeRule && doc.DocumentType != model.DocumentTypeNotice {
		return model.Document{}, fmt.Errorf("document_type must be rule or notice")
	}
	return doc, nil
}

func LoadDocuments(root string) ([]model.Document, error) {
	loaded, err := Load(root)
	if err != nil {
		return nil, err
	}
	return loaded.Documents, nil
}

// loadDocumentMetadata reads document entrypoints. Full corpus contract
// validation, including attachment files and hashes, is performed by Load.
func loadDocumentMetadata(root string) ([]model.Document, error) {
	var docs []model.Document
	seen := make(map[string]struct{})
	seenIDs := make(map[string]string)
	for _, item := range documentRoots(root) {
		base := item.path
		if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
			continue
		}
		paths, err := documentPaths(base)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			data, err := readFileBounded(path, maxDocumentMarkdownBytes)
			if err != nil {
				return nil, fmt.Errorf("%s: read document: %w", path, err)
			}
			doc, err := ParseMarkdown(data)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if doc.Language != item.language {
				return nil, fmt.Errorf("%s: language %q does not match directory language %q", path, doc.Language, item.language)
			}
			if doc.DocumentType != item.documentType {
				return nil, fmt.Errorf("%s: document_type %q does not match directory type %q", path, doc.DocumentType, item.documentType)
			}
			doc.Path = path
			if previousPath, ok := seenIDs[doc.ID]; ok {
				return nil, fmt.Errorf("duplicate document id %q in %s and %s", doc.ID, previousPath, path)
			}
			seenIDs[doc.ID] = path
			docs = append(docs, doc)
		}
	}
	return docs, nil
}

func documentPaths(base string) ([]string, error) {
	return filepath.Glob(filepath.Join(base, "*", "index.md"))
}

type documentRoot struct {
	language     string
	documentType model.DocumentType
	path         string
}

func documentRoots(root string) []documentRoot {
	return []documentRoot{
		{language: model.LanguageKorean, documentType: model.DocumentTypeRule, path: filepath.Join(root, model.LanguageKorean, "rules")},
		{language: model.LanguageKorean, documentType: model.DocumentTypeNotice, path: filepath.Join(root, model.LanguageKorean, "notices")},
		{language: model.LanguageEnglish, documentType: model.DocumentTypeRule, path: filepath.Join(root, model.LanguageEnglish, "rules")},
		{language: model.LanguageEnglish, documentType: model.DocumentTypeNotice, path: filepath.Join(root, model.LanguageEnglish, "notices")},
	}
}
