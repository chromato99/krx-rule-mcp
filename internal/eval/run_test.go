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
