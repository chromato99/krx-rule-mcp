package corpus

import (
	"bytes"
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

func RenderMarkdown(doc model.Document) ([]byte, error) {
	meta := doc
	meta.Body = ""
	meta.Path = ""
	meta.Language = model.NormalizeLanguage(meta.Language)
	if meta.ContentHash == "" {
		meta.ContentHash = model.HashText(doc.Body)
	}
	if meta.CollectedAt.IsZero() {
		return nil, fmt.Errorf("collected_at is required")
	}
	buf := bytes.NewBuffer(nil)
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(buf)
	enc.SetIndent(2)
	if err := enc.Encode(meta); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	buf.WriteString("---\n\n")
	buf.WriteString(strings.TrimSpace(doc.Body))
	buf.WriteString("\n")
	return buf.Bytes(), nil
}

func WriteDocument(root string, doc model.Document) (string, error) {
	dir := documentBundleDir(root, doc)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "index.md")
	data, err := RenderMarkdown(doc)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func LoadDocuments(root string) ([]model.Document, error) {
	var docs []model.Document
	seen := make(map[string]struct{})
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
			docs = append(docs, doc)
		}
	}
	return docs, nil
}

func documentBundleDir(root string, doc model.Document) string {
	folder := "rules"
	if doc.DocumentType == model.DocumentTypeNotice {
		folder = "notices"
	}
	return filepath.Join(root, model.NormalizeLanguage(doc.Language), folder, model.Slug(doc.Title))
}

func documentPaths(base string) ([]string, error) {
	var out []string
	flat, err := filepath.Glob(filepath.Join(base, "*.md"))
	if err != nil {
		return nil, err
	}
	out = append(out, flat...)
	bundles, err := filepath.Glob(filepath.Join(base, "*", "index.md"))
	if err != nil {
		return nil, err
	}
	out = append(out, bundles...)
	return out, nil
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
		{language: model.LanguageKorean, path: filepath.Join(root, "rules")},
		{language: model.LanguageKorean, path: filepath.Join(root, "notices")},
	}
}
