package corpus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chromato99/krx-rule-mcp/internal/model"
	"gopkg.in/yaml.v3"
)

var errFrontmatter = errors.New("missing YAML frontmatter")

func ParseMarkdown(data []byte) (model.Document, error) {
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
	body := strings.TrimSpace(rest[idx+len("\n---"):])
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
	doc.Language = model.NormalizeLanguage(doc.Language)
	return doc, nil
}

func LoadDocuments(root string) ([]model.Document, error) {
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
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			doc, err := ParseMarkdown(data)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if doc.Language == "" {
				doc.Language = item.language
			}
			doc.Language = model.NormalizeLanguage(doc.Language)
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
	language string
	path     string
}

func documentRoots(root string) []documentRoot {
	return []documentRoot{
		{language: model.LanguageKorean, path: filepath.Join(root, model.LanguageKorean, "rules")},
		{language: model.LanguageKorean, path: filepath.Join(root, model.LanguageKorean, "notices")},
		{language: model.LanguageEnglish, path: filepath.Join(root, model.LanguageEnglish, "rules")},
		{language: model.LanguageEnglish, path: filepath.Join(root, model.LanguageEnglish, "notices")},
	}
}
