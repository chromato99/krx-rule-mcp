package index

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/chromato99/krx-rule-mcp/internal/model"
)

func TestRealDataSearchEvaluation(t *testing.T) {
	if os.Getenv("KRX_DATA_TEST") != "1" {
		t.Skip("set KRX_DATA_TEST=1 to run collected-data search evaluation")
	}

	dataRoot := dataTestRoot()
	repo, err := LoadRepository(dataRoot, dataTestBM25Path())
	if err != nil {
		t.Fatalf("load repository: %v", err)
	}
	if len(repo.Documents) < 80 {
		t.Fatalf("loaded %d documents, want at least 80", len(repo.Documents))
	}

	var bodyChunks, attachmentChunks int
	for _, c := range repo.Engine.Chunks() {
		switch c.Source {
		case "body":
			bodyChunks++
		case "attachment":
			attachmentChunks++
		}
	}
	if bodyChunks == 0 || attachmentChunks == 0 {
		t.Fatalf("expected body and attachment chunks, got body=%d attachment=%d", bodyChunks, attachmentChunks)
	}

	t.Run("notice-body-query", func(t *testing.T) {
		query, id := retrievableBodyQuery(t, repo, model.DocumentTypeNotice)
		results := repo.Engine.Search(SearchOptions{
			Query:  query,
			Limit:  5,
			Filter: Filter{DocumentType: model.DocumentTypeNotice},
		})
		requireResult(t, results, id, false)
	})

	t.Run("rule-body-query", func(t *testing.T) {
		query, id := retrievableBodyQuery(t, repo, model.DocumentTypeRule)
		results := repo.Engine.Search(SearchOptions{
			Query:  query,
			Limit:  5,
			Filter: Filter{DocumentType: model.DocumentTypeRule},
		})
		requireResult(t, results, id, false)
	})

	t.Run("attachment-only-notice-query", func(t *testing.T) {
		query, id, attachmentID := retrievableAttachmentQuery(t, repo, model.DocumentTypeNotice)
		results := repo.Engine.Search(SearchOptions{
			Query:  query,
			Limit:  5,
			Filter: Filter{DocumentType: model.DocumentTypeNotice},
		})
		requireAttachmentResult(t, results, id, attachmentID)
	})

	t.Run("attachment-only-rule-query", func(t *testing.T) {
		query, id, attachmentID := retrievableAttachmentQuery(t, repo, model.DocumentTypeRule)
		results := repo.Engine.Search(SearchOptions{
			Query:  query,
			Limit:  5,
			Filter: Filter{DocumentType: model.DocumentTypeRule},
		})
		requireAttachmentResult(t, results, id, attachmentID)
	})
}

func dataTestRoot() string {
	if dataRoot := os.Getenv("KRX_RULE_DATA_DIR"); dataRoot != "" {
		return dataRoot
	}
	return filepath.Join("..", "..", "data")
}

func dataTestBM25Path() string {
	if value := strings.TrimSpace(os.Getenv("KRX_INDEX_PATH")); value != "" {
		return resolveDataTestPath(value)
	}
	return resolveDataTestPath(DefaultBM25Path(dataTestIndexDir()))
}

func dataTestIndexDir() string {
	if value := strings.TrimSpace(os.Getenv("KRX_RULE_INDEX_DIR")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("KRX_INDEX_DIR")); value != "" {
		return value
	}
	return filepath.Join("..", "..", DefaultIndexDir)
}

func requireResult(t *testing.T, results []SearchResult, id string, wantAttachment bool) {
	t.Helper()
	for _, result := range results {
		if result.ID != id {
			continue
		}
		if wantAttachment && len(result.AttachmentMatches) == 0 {
			t.Fatalf("result %s did not include attachment matches: %#v", id, result)
		}
		return
	}
	t.Fatalf("missing result %s in %#v", id, results)
}

func requireAttachmentResult(t *testing.T, results []SearchResult, id, attachmentID string) {
	t.Helper()
	for _, result := range results {
		if result.ID != id {
			continue
		}
		for _, match := range result.AttachmentMatches {
			if match.ID == attachmentID {
				return
			}
		}
		t.Fatalf("result %s did not include attachment %s: %#v", id, attachmentID, result)
	}
	t.Fatalf("missing result %s for attachment %s in %#v", id, attachmentID, results)
}

func retrievableBodyQuery(t *testing.T, repo *Repository, documentType model.DocumentType) (string, string) {
	t.Helper()
	for _, doc := range sortedRealDataDocuments(repo) {
		if doc.DocumentType != documentType || doc.Language != model.LanguageKorean || len(strings.TrimSpace(doc.Body)) < 200 {
			continue
		}
		for _, query := range candidateRealDataQueries(doc.Body) {
			results := repo.Engine.Search(SearchOptions{
				Query:  query,
				Limit:  5,
				Filter: Filter{DocumentType: documentType},
			})
			if resultContains(results, doc.ID, "") {
				t.Logf("%s body query %q matched %s (%s)", documentType, query, doc.ID, doc.Title)
				return query, doc.ID
			}
		}
	}
	t.Fatalf("could not find retrievable %s body query in collected corpus", documentType)
	return "", ""
}

func retrievableAttachmentQuery(t *testing.T, repo *Repository, documentType model.DocumentType) (string, string, string) {
	t.Helper()
	for _, doc := range sortedRealDataDocuments(repo) {
		if doc.DocumentType != documentType || doc.Language != model.LanguageKorean {
			continue
		}
		for _, attachment := range doc.Attachments {
			if attachment.Status != model.AttachmentConverted {
				continue
			}
			attachmentDoc, ok := repo.Attachments[attachment.ID]
			if !ok || len(strings.TrimSpace(attachmentDoc.Text)) < 200 {
				continue
			}
			text := attachment.Title + "\n" + attachmentDoc.Text
			for _, query := range candidateRealDataQueries(text) {
				results := repo.Engine.Search(SearchOptions{
					Query:  query,
					Limit:  5,
					Filter: Filter{DocumentType: documentType},
				})
				if resultContains(results, doc.ID, attachment.ID) {
					t.Logf("%s attachment query %q matched %s / %s", documentType, query, doc.ID, attachment.ID)
					return query, doc.ID, attachment.ID
				}
			}
		}
	}
	t.Fatalf("could not find retrievable %s attachment query in collected corpus", documentType)
	return "", "", ""
}

func sortedRealDataDocuments(repo *Repository) []model.Document {
	docs := make([]model.Document, 0, len(repo.Documents))
	for _, doc := range repo.Documents {
		docs = append(docs, doc)
	}
	sort.Slice(docs, func(i, j int) bool {
		if docs[i].DocumentType != docs[j].DocumentType {
			return docs[i].DocumentType < docs[j].DocumentType
		}
		if docs[i].PublishedDate != docs[j].PublishedDate {
			return docs[i].PublishedDate > docs[j].PublishedDate
		}
		return docs[i].ID < docs[j].ID
	})
	return docs
}

func resultContains(results []SearchResult, id, attachmentID string) bool {
	for _, result := range results {
		if result.ID != id {
			continue
		}
		if attachmentID == "" {
			return true
		}
		for _, match := range result.AttachmentMatches {
			if match.ID == attachmentID {
				return true
			}
		}
	}
	return false
}

var realDataQueryTokenRE = regexp.MustCompile(`[0-9A-Za-z가-힣]+`)

func candidateRealDataQueries(text string) []string {
	rawTokens := realDataQueryTokenRE.FindAllString(text, -1)
	tokens := make([]string, 0, len(rawTokens))
	for _, token := range rawTokens {
		token = strings.TrimSpace(token)
		if len([]rune(token)) < 2 || isLowSignalRealDataToken(token) {
			continue
		}
		tokens = append(tokens, token)
	}

	const maxQueries = 80
	var queries []string
	for _, size := range []int{6, 5, 4, 3} {
		if len(tokens) < size {
			continue
		}
		for start := 0; start+size <= len(tokens) && len(queries) < maxQueries; start += size {
			queries = append(queries, strings.Join(tokens[start:start+size], " "))
		}
	}
	return queries
}

func isLowSignalRealDataToken(token string) bool {
	switch strings.ToLower(token) {
	case "document", "attachments", "attachment", "index", "status", "converted",
		"source", "title", "file", "규정", "시행세칙", "개정", "예고", "거래소",
		"한국거래소", "현행", "개정안", "비고", "생략", "같음":
		return true
	default:
		return false
	}
}
