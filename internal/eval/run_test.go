package evaluation

import (
	"context"
	"fmt"
	"testing"

	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	mcpserver "github.com/chromato99/krx-rule-mcp/internal/mcp"
)

type fixtureClient struct{}

type multiTargetClient struct{}

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
			Candidates: []searchindex.ChunkCandidate{{
				ChunkID: "rule-1#0", DocumentID: "rule-1", ArticleID: "제1조", Text: "제1조 직접 근거",
				BM25Rank: 2, VectorRank: 4, BaselineRank: 3, FinalRank: 1, RerankerRank: 1, Reranked: true,
			}},
			RerankerCandidateCount: 1,
			RerankerElapsedMillis:  5,
			RerankerAdopted:        true,
			Results: []mcpserver.SearchResultDTO{{
				ID: "rule-1",
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

func (multiTargetClient) SearchRules(_ context.Context, _ mcpserver.SearchRulesInput) (mcpserver.SearchRulesOutput, error) {
	return mcpserver.SearchRulesOutput{
		Answerable: true,
		Answerability: searchindex.AnswerabilityDecision{
			Status:           searchindex.AnswerabilitySupported,
			EvidenceChunkIDs: []string{"rule-1#0", "rule-2#0"},
		},
		Mode: "bm25",
		Results: []mcpserver.SearchResultDTO{
			{ID: "rule-1", EvidenceMatches: []mcpserver.EvidenceMatchDTO{{ChunkID: "rule-1#0", Source: "body", ArticleID: "제1조"}}},
			{ID: "rule-2", EvidenceMatches: []mcpserver.EvidenceMatchDTO{{ChunkID: "rule-2#0", Source: "body", ArticleID: "제2조"}}},
		},
	}, nil
}

func (multiTargetClient) GetContext(_ context.Context, input mcpserver.GetContextInput) (mcpserver.ContextOutput, error) {
	switch input.ChunkID {
	case "rule-1#0":
		return mcpserver.ContextOutput{
			Document: mcpserver.DocumentDTO{ID: "rule-1"},
			Chunks:   []mcpserver.ChunkDTO{{ID: input.ChunkID, DocumentID: "rule-1", ArticleID: "제1조", Text: "첫 번째 직접 근거"}},
			Content:  "첫 번째 직접 근거",
		}, nil
	case "rule-2#0":
		return mcpserver.ContextOutput{
			Document: mcpserver.DocumentDTO{ID: "rule-2"},
			Chunks:   []mcpserver.ChunkDTO{{ID: input.ChunkID, DocumentID: "rule-2", ArticleID: "제2조", Text: "두 번째 직접 근거"}},
			Content:  "두 번째 직접 근거",
		}, nil
	default:
		return mcpserver.ContextOutput{}, fmt.Errorf("unknown chunk %q", input.ChunkID)
	}
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
		split := "regression"
		if index >= 77 && index < 97 {
			split = "development"
		} else if index >= 97 {
			split = "holdout"
		}
		language := ""
		if index >= 107 {
			language = "en"
		}
		fixture.Cases = append(fixture.Cases, Case{
			ID: fmt.Sprintf("insufficient-%03d", index), Group: "hard-negative", Split: split,
			Input: CaseInput{Query: "insufficient query", Language: language, Limit: 5}, Expectation: Expectation{EvidenceStatus: "insufficient", ClaimRelation: "not_applicable", TargetPolicy: "any"},
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
	if report.Summary.CandidateEvidenceRecall64Rate != 1 || report.Summary.RerankerPoolHitRate != 1 || report.Summary.P95RerankerLatencyMillis != 5 || report.Summary.RerankerAttempted != 1 || report.Summary.RerankerAdopted != 1 {
		t.Fatalf("candidate/reranker metrics = %#v", report.Summary)
	}
	if report.Summary.InsufficientRefusalRate != 1 || report.Summary.AmbiguousClarificationRate != 1 {
		t.Fatalf("answerability metrics = %#v", report.Summary)
	}
	if report.Summary.ContextConsistencyRate != 1 || report.Summary.AutomaticPassed != 120 {
		t.Fatalf("contract metrics = %#v", report.Summary)
	}
	if len(report.SplitSummaries) != 3 || report.SplitSummaries["holdout"].Cases != 20 {
		t.Fatalf("split summaries = %#v", report.SplitSummaries)
	}
	if len(report.LanguageSummaries) != 2 || report.LanguageSummaries["en"].Cases != 10 {
		t.Fatalf("language summaries = %#v", report.LanguageSummaries)
	}
	if report.LanguageSplitSummaries["en"]["holdout"].Cases != 10 || report.LanguageSplitSummaries["unspecified"]["regression"].Cases == 0 {
		t.Fatalf("language split summaries = %#v", report.LanguageSplitSummaries)
	}
	subset, err := RunCasePrefix(context.Background(), fixture, "supported", fixtureClient{}, Provenance{})
	if err != nil || subset.Summary.Cases != 1 || subset.Cases[0].ID != "supported" {
		t.Fatalf("case-prefix report = %#v error=%v", subset, err)
	}
}

func TestValidateFixtureRejectsDuplicateIDs(t *testing.T) {
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
		split := "regression"
		if index >= 78 && index < 98 {
			split = "development"
		} else if index >= 98 {
			split = "holdout"
		}
		fixture.Cases = append(fixture.Cases, Case{
			ID: fmt.Sprintf("filler-%03d", index), Group: "hard-negative", Split: split,
			Input: CaseInput{Query: "filler", Limit: 5}, Expectation: Expectation{EvidenceStatus: "insufficient", ClaimRelation: "not_applicable", TargetPolicy: "any"},
		})
	}
	if err := ValidateFixture(fixture); err == nil {
		t.Fatal("ValidateFixture() accepted duplicate IDs and unanchored evidence")
	}
}

func TestEvidenceTextMatchesNormalizesWhitespace(t *testing.T) {
	expectation := EvidenceExpectation{
		MustContainAll:         []string{"ten (10) years", "dispute mediation request"},
		RelationMustContainAny: []string{"must not use"},
	}
	text := "TEN  (10)\n years from the dispute\tmediation request; the member must   not use it."
	if !evidenceTextMatches(expectation, text) {
		t.Fatal("evidence matcher did not normalize case and Unicode whitespace")
	}
	if !claimRelationEvidenceMatches("contradicts", expectation, text) {
		t.Fatal("relation matcher did not normalize case and Unicode whitespace")
	}
}

func TestEvaluateCaseAccumulatesTargetsAcrossEvidenceContexts(t *testing.T) {
	tests := []struct {
		name         string
		policy       string
		atLeast      int
		targets      []Target
		documentRank int
		evidenceRank int
	}{
		{
			name:   "any target keeps document and evidence ranks separate",
			policy: "any",
			targets: []Target{
				{DocumentID: "rule-2", ArticleID: "제2조", Evidence: &EvidenceExpectation{MustContainAll: []string{"두 번째"}}},
			},
			documentRank: 2,
			evidenceRank: 1,
		},
		{
			name:   "all targets across documents",
			policy: "all",
			targets: []Target{
				{DocumentID: "rule-1", ArticleID: "제1조", Evidence: &EvidenceExpectation{MustContainAll: []string{"첫 번째"}}},
				{DocumentID: "rule-2", ArticleID: "제2조", Evidence: &EvidenceExpectation{MustContainAll: []string{"두 번째"}}},
			},
			documentRank: 2,
			evidenceRank: 1,
		},
		{
			name:    "at least two of three targets",
			policy:  "at_least",
			atLeast: 2,
			targets: []Target{
				{DocumentID: "rule-1", ArticleID: "제1조"},
				{DocumentID: "rule-2", ArticleID: "제2조"},
				{DocumentID: "rule-3", ArticleID: "제3조"},
			},
			documentRank: 2,
			evidenceRank: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := Case{
				ID: "multi-target", Group: "multi-target", Split: "development",
				Input: CaseInput{Query: "복수 근거"},
				Expectation: Expectation{
					EvidenceStatus: "supported", ClaimRelation: "supports", TargetPolicy: test.policy,
					AtLeast: test.atLeast, Targets: test.targets,
				},
			}
			result := evaluateCase(context.Background(), item, multiTargetClient{})
			if result.DocumentRank != test.documentRank || result.EvidenceRank != test.evidenceRank || !result.AutomaticPassed {
				t.Fatalf("multi-target result = %#v", result)
			}
		})
	}
}

func TestValidateCaseRejectsDuplicateTargetTuple(t *testing.T) {
	item := Case{
		ID: "duplicate-target", Group: "multi-target", Split: "development",
		Input: CaseInput{Query: "복수 근거"},
		Expectation: Expectation{
			EvidenceStatus: "supported", ClaimRelation: "supports", TargetPolicy: "all",
			Targets: []Target{
				{DocumentID: "rule-1", ArticleID: "제1조"},
				{DocumentID: "rule-1", ArticleID: "제1조"},
			},
		},
	}
	if err := validateCase(item); err == nil {
		t.Fatal("validateCase accepted a duplicate target tuple")
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
