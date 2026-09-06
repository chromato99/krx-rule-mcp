package evaluation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	"github.com/chromato99/krx-rule-mcp/internal/model"
)

func TestAuditFixtureGroundingRejectsStaleAndFilteredTargets(t *testing.T) {
	doc := model.Document{
		ID: "rule-1", Title: "규정", Language: model.LanguageKorean,
		DocumentType: model.DocumentTypeRule, Category: "시장규정", EffectiveDate: "2026-01-01",
		Body: "**제1조(의무)** 회원은 신고하여야 한다.", Searchable: boolPointer(true),
	}
	english := model.Document{
		ID: "rule-en", Title: "English Rule", Language: model.LanguageEnglish,
		DocumentType: model.DocumentTypeRule, EffectiveDate: "2026-01-01", Searchable: boolPointer(true),
		Body: "§10. Use of Fund\n\nCHAPTER 3. USE OF FUND\n\nA settlement default permits the fund to compensate the losses.\n\n§11. Reports",
	}
	repo := &searchindex.Repository{
		Documents: map[string]model.Document{doc.ID: doc, english.ID: english},
		Engine:    searchindex.BuildWithAttachments([]model.Document{doc, english}, nil, nil),
	}
	fixture := Fixture{Cases: []Case{
		{
			ID: "valid", Input: CaseInput{Language: "ko", DocumentType: "rule"},
			Expectation: Expectation{Targets: []Target{{
				DocumentID: "rule-1", ArticleID: "제1조",
				Evidence: &EvidenceExpectation{MustContainAll: []string{"신고하여야 한다"}},
			}}},
		},
		{
			ID: "valid-document-evidence", Input: CaseInput{Language: "ko", DocumentType: "rule"},
			Expectation: Expectation{Targets: []Target{{
				DocumentID: "rule-1", Evidence: &EvidenceExpectation{MustContainAll: []string{"신고하여야 한다"}},
			}}},
		},
		{
			ID: "valid-raw-article-fallback", Input: CaseInput{Language: "en", DocumentType: "rule"},
			Expectation: Expectation{Targets: []Target{{
				DocumentID: "rule-en", ArticleID: "§10",
				Evidence: &EvidenceExpectation{MustContainAll: []string{"settlement default", "compensate the losses"}},
			}}},
		},
		{
			ID: "missing-document", Expectation: Expectation{Targets: []Target{{DocumentID: "missing"}}},
		},
		{
			ID: "wrong-language", Input: CaseInput{Language: "en"},
			Expectation: Expectation{Targets: []Target{{DocumentID: "rule-1"}}},
		},
		{
			ID: "missing-phrase", Expectation: Expectation{Targets: []Target{{
				DocumentID: "rule-1", ArticleID: "제1조",
				Evidence: &EvidenceExpectation{MustContainAll: []string{"존재하지 않는 문구"}},
			}}},
		},
	}}

	issues := AuditFixtureGrounding(fixture, repo)
	if len(issues) != 3 {
		t.Fatalf("issues = %#v, want 3", issues)
	}
	joined := issues[0].String() + "\n" + issues[1].String() + "\n" + issues[2].String()
	for _, want := range []string{"missing-document", "wrong-language", "missing-phrase"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("issues %q do not contain %q", joined, want)
		}
	}
}

func TestActualFixtureTargetsAreGroundedInCorpus(t *testing.T) {
	if os.Getenv("KRX_DATA_TEST") != "1" {
		t.Skip("set KRX_DATA_TEST=1 to audit the golden fixture against the collected corpus")
	}
	fixture, _, err := LoadFixture(filepath.Join("..", "..", "eval", "fixtures", "retrieval.json"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	dataRoot := strings.TrimSpace(os.Getenv("KRX_RULE_DATA_DIR"))
	if dataRoot == "" {
		dataRoot = filepath.Join("..", "..", "..", "krx-rule-markdown", "data")
	}
	indexDir := strings.TrimSpace(os.Getenv("KRX_RULE_INDEX_DIR"))
	if indexDir == "" {
		indexDir = filepath.Join("..", "..", "index")
	}
	repo, err := searchindex.LoadRepositoryGeneration(dataRoot, indexDir, searchindex.RepositoryLoadOptions{})
	if err != nil {
		t.Fatalf("LoadRepositoryGeneration: %v", err)
	}
	if err := ValidateFixtureGrounding(fixture, repo); err != nil {
		t.Fatal(err)
	}
}

func boolPointer(value bool) *bool {
	return &value
}
