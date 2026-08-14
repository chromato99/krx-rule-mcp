package evaluation

import (
	"context"
	"fmt"
	"testing"

	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	mcpserver "github.com/chromato99/krx-rule-mcp/internal/mcp"
)

type fixtureClient struct{}

func (fixtureClient) SearchRules(_ context.Context, input mcpserver.SearchRulesInput) (mcpserver.SearchRulesOutput, error) {
	switch input.Query {
	case "supported query":
		return mcpserver.SearchRulesOutput{
			Answerable: true,
			Answerability: searchindex.AnswerabilityDecision{
				Status:           searchindex.AnswerabilitySupported,
				EvidenceChunkIDs: []string{"rule-1#0"},
			},
			Mode: "bm25",
			Results: []mcpserver.SearchResultDTO{{
				ID: "rule-1", MatchedChunkID: "rule-1#0", ArticleID: "제1조",
				EvidenceMatches: []mcpserver.EvidenceMatchDTO{{
					ChunkID: "rule-1#0", Source: "body", ArticleID: "제1조", Searchable: true,
					Score: 1, BM25Score: 2, LexicalCoverage: 1,
				}},
			}},
		}, nil
	case "insufficient query":
		return mcpserver.SearchRulesOutput{
			Answerability: searchindex.AnswerabilityDecision{Status: searchindex.AnswerabilityInsufficient},
			Mode:          "bm25", Results: []mcpserver.SearchResultDTO{},
		}, nil
	default:
		return mcpserver.SearchRulesOutput{
			Answerability: searchindex.AnswerabilityDecision{Status: searchindex.AnswerabilityAmbiguous, Clarification: "대상을 지정하세요."},
			Mode:          "bm25", Results: []mcpserver.SearchResultDTO{},
		}, nil
	}
}

func (fixtureClient) GetContext(_ context.Context, input mcpserver.GetContextInput) (mcpserver.ContextOutput, error) {
	return mcpserver.ContextOutput{
		Document: mcpserver.DocumentDTO{ID: "rule-1"},
		Chunks:   []mcpserver.ChunkDTO{{ID: input.ChunkID, DocumentID: "rule-1", Source: "body", ArticleID: "제1조", Text: "제1조 직접 근거"}},
		Content:  "제1조 직접 근거",
	}, nil
}

func TestRunScoresRetrievalEvidenceRefusalAndClarification(t *testing.T) {
	fixture := Fixture{
		SchemaVersion:  1,
		FixtureVersion: "rag-vtest",
		Source:         FixtureSource{SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", OriginalCases: 3},
		Cases: []Case{
			{
				ID: "supported", Group: "semantic", Split: "regression",
				Input:       CaseInput{Query: "supported query", Limit: 5},
				Expectation: Expectation{EvidenceStatus: "supported", ClaimRelation: "supports", TargetPolicy: "any", Targets: []Target{{DocumentID: "rule-1", ArticleID: "제1조", Evidence: &EvidenceExpectation{MustContainAll: []string{"직접", "근거"}}}}},
			},
			{
				ID: "insufficient", Group: "hard-negative", Split: "regression",
				Input: CaseInput{Query: "insufficient query", Limit: 5}, Expectation: Expectation{EvidenceStatus: "insufficient", ClaimRelation: "not_applicable", TargetPolicy: "any"},
			},
			{
				ID: "ambiguous", Group: "ambiguous", Split: "regression",
				Input: CaseInput{Query: "broad query", Limit: 5}, Expectation: Expectation{EvidenceStatus: "ambiguous", ClaimRelation: "not_applicable", TargetPolicy: "any", ClarificationRequired: true},
			},
		},
	}
	for index := 0; index < 117; index++ {
		fixture.Cases = append(fixture.Cases, Case{
			ID: fmt.Sprintf("insufficient-%03d", index), Group: "hard-negative", Split: "regression",
			Input: CaseInput{Query: "insufficient query", Limit: 5}, Expectation: Expectation{EvidenceStatus: "insufficient", ClaimRelation: "not_applicable", TargetPolicy: "any"},
		})
	}
	report, err := Run(context.Background(), fixture, fixtureClient{}, Provenance{EvaluatorVersion: EvaluatorVersion})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.Summary.DocumentHitAt5Rate != 1 || report.Summary.MRRAt5 != 1 {
		t.Fatalf("document metrics = %#v", report.Summary)
	}
	if report.Summary.EvidenceHitAt1Rate != 1 || report.Summary.EvidenceRecallAt3Rate != 1 {
		t.Fatalf("evidence metrics = %#v", report.Summary)
	}
	if report.Summary.InsufficientRefusalRate != 1 || report.Summary.AmbiguousClarificationRate != 1 {
		t.Fatalf("answerability metrics = %#v", report.Summary)
	}
	if report.Summary.ContextConsistencyRate != 1 || report.Summary.AutomaticPassed != 120 {
		t.Fatalf("contract metrics = %#v", report.Summary)
	}
}

func TestValidateFixtureRejectsDuplicateIDsAndUnanchoredEvidence(t *testing.T) {
	fixture := Fixture{
		SchemaVersion:  1,
		FixtureVersion: "rag-vtest",
		Source:         FixtureSource{SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", OriginalCases: 2},
		Cases: []Case{
			{ID: "duplicate", Group: "exact", Split: "regression", Input: CaseInput{Query: "one", Limit: 5}, Expectation: Expectation{EvidenceStatus: "supported", ClaimRelation: "supports", TargetPolicy: "any", Targets: []Target{{DocumentID: "rule-1", Evidence: &EvidenceExpectation{MustContainAny: []string{"x"}}}}}},
			{ID: "duplicate", Group: "exact", Split: "regression", Input: CaseInput{Query: "two", Limit: 5}, Expectation: Expectation{EvidenceStatus: "insufficient", ClaimRelation: "not_applicable", TargetPolicy: "any"}},
		},
	}
	for index := 0; index < 118; index++ {
		fixture.Cases = append(fixture.Cases, Case{
			ID: fmt.Sprintf("filler-%03d", index), Group: "hard-negative", Split: "regression",
			Input: CaseInput{Query: "filler", Limit: 5}, Expectation: Expectation{EvidenceStatus: "insufficient", ClaimRelation: "not_applicable", TargetPolicy: "any"},
		})
	}
	if err := ValidateFixture(fixture); err == nil {
		t.Fatal("ValidateFixture() accepted duplicate IDs and unanchored evidence")
	}
}

func TestContradictionRequiresReviewedPolarityPhrase(t *testing.T) {
	expectation := Expectation{
		EvidenceStatus: "supported", ClaimRelation: "contradicts", TargetPolicy: "any",
		Targets: []Target{{
			DocumentID: "rule-1", ArticleID: "제139조",
			Evidence: &EvidenceExpectation{
				MustContainAll:         []string{"위탁증거금", "사용"},
				RelationMustContainAny: []string{"이외에는 사용하지 못한다"},
			},
		}},
	}
	observed := mcpserver.EvidenceMatchDTO{ArticleID: "제139조"}
	contradiction := mcpserver.ContextOutput{
		Document: mcpserver.DocumentDTO{ID: "rule-1"},
		Content:  "위탁증거금은 정하는 방법 이외에는 사용하지 못한다.",
	}
	if !contextMatchesExpectation(expectation, observed, contradiction) {
		t.Fatal("reviewed contradiction phrase did not match")
	}
	opposite := contradiction
	opposite.Content = "회원은 위탁증거금을 자유롭게 사용할 수 있다."
	if contextMatchesExpectation(expectation, observed, opposite) {
		t.Fatal("opposite-polarity sentence matched a contradiction expectation")
	}
}

func TestValidateCaseRequiresContradictionPolarityEvidence(t *testing.T) {
	item := Case{
		ID: "false-premise", Group: "false-premise", Split: "regression",
		Input: CaseInput{Query: "자유롭게 사용할 수 있다"},
		Expectation: Expectation{
			EvidenceStatus: "supported", ClaimRelation: "contradicts", TargetPolicy: "any",
			Targets: []Target{
				{DocumentID: "rule-reviewed", ArticleID: "제1조", Evidence: &EvidenceExpectation{
					RelationMustContainAny: []string{"사용하지 못한다"},
				}},
				{DocumentID: "rule-unreviewed", ArticleID: "제2조", Evidence: &EvidenceExpectation{
					MustContainAny: []string{"사용"},
				}},
			},
		},
	}
	if err := validateCase(item); err == nil {
		t.Fatal("validateCase accepted contradicts without relation evidence")
	}
}

func TestExpansionTermsUseLexiconWhitespaceNormalization(t *testing.T) {
	search := mcpserver.SearchRulesOutput{QueryExpansion: &searchindex.DomainQueryExpansion{
		ExpandedQuery: "ETF 순자산 가치와 NAV 괴리",
		AppliedTerms:  []searchindex.DomainLexiconMatch{{ID: "etf_nav"}},
	}}
	expectation := QueryExpansionExpectation{
		RequiredEntryIDs: []string{"etf_nav"},
		RequiredTerms:    []string{"순자산가치"},
	}
	if !expansionMatches(expectation, search) {
		t.Fatal("spacing-only lexicon equivalent did not satisfy expansion expectation")
	}
}
