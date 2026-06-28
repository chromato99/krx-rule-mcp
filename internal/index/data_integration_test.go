package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chromato99/krx-rule-mcp/internal/model"
)

func TestRealDataSearchEvaluation(t *testing.T) {
	if os.Getenv("KRX_DATA_TEST") != "1" {
		t.Skip("set KRX_DATA_TEST=1 to run collected-data search evaluation")
	}

	dataRoot := dataTestRoot()
	repo, err := LoadRepository(dataRoot, filepath.Join(dataRoot, "index", "bm25.krxidx"))
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
		results := repo.Engine.Search(SearchOptions{
			Query:  "기초자산조기인수도부거래 현물가격 기준 정비",
			Limit:  5,
			Filter: Filter{DocumentType: model.DocumentTypeNotice},
		})
		requireResult(t, results, "210217910", false)
	})

	t.Run("rule-body-query", func(t *testing.T) {
		results := repo.Engine.Search(SearchOptions{
			Query:  "선물스프레드증거금률",
			Limit:  5,
			Filter: Filter{DocumentType: model.DocumentTypeRule},
		})
		requireResult(t, results, "210210269", false)
	})

	t.Run("attachment-only-notice-query", func(t *testing.T) {
		results := repo.Engine.Search(SearchOptions{
			Query:  "서울외환시장 거래시간 변경",
			Limit:  5,
			Filter: Filter{DocumentType: model.DocumentTypeNotice},
		})
		requireResult(t, results, "210217910", true)
	})

	t.Run("attachment-only-disclosure-query", func(t *testing.T) {
		results := repo.Engine.Search(SearchOptions{
			Query:  "자본금 전액이 잠식된 때",
			Limit:  5,
			Filter: Filter{DocumentType: model.DocumentTypeNotice},
		})
		requireResult(t, results, "210217080", true)
	})
}

func dataTestRoot() string {
	if dataRoot := os.Getenv("KRX_RULE_DATA_DIR"); dataRoot != "" {
		return dataRoot
	}
	return filepath.Join("..", "..", "data")
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
