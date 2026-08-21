package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	evaluation "github.com/chromato99/krx-rule-mcp/internal/eval"
)

func TestRepositoryEnglishBaselineMatchesFixture(t *testing.T) {
	baseline, err := loadEnglishNonRegressionBaseline(filepath.Join("..", "..", "eval", "baselines", "rag-v1-e5-english.json"))
	if err != nil {
		t.Fatalf("loadEnglishNonRegressionBaseline: %v", err)
	}
	_, fixtureHash, err := evaluation.LoadFixture(filepath.Join("..", "..", "eval", "golden", "rag-v1.json"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if baseline.FixtureSHA256 != fixtureHash {
		t.Fatalf("baseline fixture sha256 = %s, want %s", baseline.FixtureSHA256, fixtureHash)
	}
}

func TestSplitEvaluationCannotUseReleaseGate(t *testing.T) {
	err := validateEvalOptions("holdout", "", true)
	if err == nil || !strings.Contains(err.Error(), "diagnostic filters") {
		t.Fatalf("validateEvalOptions() error = %v", err)
	}
	if err := validateEvalOptions("holdout", "", false); err != nil {
		t.Fatalf("diagnostic split rejected: %v", err)
	}
	if err := validateEvalOptions("", "audit-d-", false); err != nil {
		t.Fatalf("diagnostic case prefix rejected: %v", err)
	}
	if err := validateEvalOptions("holdout", "audit-d-", false); err == nil {
		t.Fatal("split and case prefix were accepted together")
	}
}

func TestQualityGateThresholds(t *testing.T) {
	passing, baseline := passingQualityGateReport()
	if failures := qualityGateFailures(passing, baseline); len(failures) != 0 {
		t.Fatalf("threshold values should pass: %v", failures)
	}
	tooSmall := passing
	tooSmall.Summary.Cases = 119
	if failures := qualityGateFailures(tooSmall, baseline); !slices.Contains(failures, "evaluation cases < 120") {
		t.Fatalf("case-count failures = %v", failures)
	}

	koreanRegression, baseline := passingQualityGateReport()
	korean := koreanRegression.LanguageSummaries["ko"]
	korean.MRRAt5 = 0.899
	koreanRegression.LanguageSummaries["ko"] = korean
	if failures := qualityGateFailures(koreanRegression, baseline); !slices.Contains(failures, "Korean MRR@5 < 0.90") {
		t.Fatalf("Korean failures = %v", failures)
	}

	semanticRegression, baseline := passingQualityGateReport()
	for index := range semanticRegression.Cases {
		if semanticRegression.Cases[index].Group == "semantic" {
			semanticRegression.Cases[index].EvidenceRank = 2
		}
	}
	if failures := qualityGateFailures(semanticRegression, baseline); !slices.Contains(failures, "semantic evidence Hit@1 < 90%") {
		t.Fatalf("semantic failures = %v", failures)
	}

	contradictionRegression, baseline := passingQualityGateReport()
	contradictionRegression.Cases = make([]evaluation.CaseResult, 10)
	for index := range contradictionRegression.Cases {
		contradictionRegression.Cases[index] = evaluation.CaseResult{
			Language: "ko", ExpectedStatus: "supported", ClaimRelation: "contradicts", EvidenceRank: 1,
		}
	}
	contradictionRegression.Cases[8].EvidenceRank = 2
	contradictionRegression.Cases[9].EvidenceRank = 2
	if failures := qualityGateFailures(contradictionRegression, baseline); !slices.Contains(failures, "Korean contradiction target-evidence Hit@1 < 90%") {
		t.Fatalf("contradiction failures = %v", failures)
	}

	holdoutRegression, baseline := passingQualityGateReport()
	holdout := holdoutRegression.LanguageSplitSummaries["ko"]["holdout"]
	holdout.EvidenceRecallAt3Rate = 0.94
	holdoutRegression.LanguageSplitSummaries["ko"]["holdout"] = holdout
	if failures := qualityGateFailures(holdoutRegression, baseline); !slices.Contains(failures, "Korean holdout evidence Recall@3 < 95%") {
		t.Fatalf("holdout failures = %v", failures)
	}

	englishRegression, baseline := passingQualityGateReport()
	english := englishRegression.LanguageSummaries["en"]
	english.EvidenceHitAt1--
	englishRegression.LanguageSummaries["en"] = english
	if failures := qualityGateFailures(englishRegression, baseline); !slices.Contains(failures, "English evidence Hit@1 regressed: current=9 baseline=10") {
		t.Fatalf("English holdout failures = %v", failures)
	}

	qwenRegression, baseline := passingQualityGateReport()
	qwenRegression.Provenance.Embedding.Model = "Qwen/Qwen3-Embedding-0.6B"
	if failures := qualityGateFailures(qwenRegression, baseline); !slices.Contains(failures, "release embedding is not the pinned full-coverage multilingual-e5-small contract") {
		t.Fatalf("embedding failures = %v", failures)
	}
}

func passingQualityGateReport() (evaluation.Report, *englishNonRegressionBaseline) {
	korean := evaluation.Summary{
		Cases: 150, DocumentEligible: 100, EvidenceEligible: 90, InsufficientCases: 30, AmbiguousCases: 20,
		DocumentHitAt5Rate: 0.95, MRRAt5: 0.90, EvidenceHitAt1Rate: 0.90, EvidenceRecallAt3Rate: 0.95,
		CandidateEvidenceRecall64Rate: 0.95, StatusAccuracy: 0.95, InsufficientRefusalRate: 0.95,
		FalseSupportedRate: 0.05, AmbiguousClarificationRate: 0.90, ContextChecks: 100, ContextConsistent: 100,
		ContextConsistencyRate: 1, HWPAttachmentChecks: 1, HWPAttachmentPassed: 1,
	}
	koreanHoldout := korean
	koreanHoldout.Cases = 30
	koreanHoldout.DocumentEligible = 15
	koreanHoldout.EvidenceEligible = 15
	koreanHoldout.InsufficientCases = 10
	koreanHoldout.AmbiguousCases = 5
	english := evaluation.Summary{
		Cases: 10, DocumentEligible: 10, DocumentHitAt5: 10, DocumentHitAt5Rate: 1, MRRAt5: 1,
		EvidenceEligible: 10, EvidenceHitAt1: 10, EvidenceHitAt1Rate: 1, EvidenceRecallAt3: 10,
		EvidenceRecallAt3Rate: 1, CandidateEvidenceRecall64: 10, CandidateEvidenceRecall64Rate: 1,
		StatusCorrect: 10, StatusAccuracy: 1, ContextChecks: 10, ContextConsistent: 10,
		ContextConsistencyRate: 1, EnglishCanonicalChecks: 10, EnglishCanonicalPassed: 10,
	}
	report := evaluation.Report{
		Provenance: evaluation.Provenance{
			FixtureSHA256: "fixture-sha",
			Embedding: &evaluation.EmbeddingIdentity{
				Model: "intfloat/multilingual-e5-small", Revision: "614241f622f53c4eeff9890bdc4f31cfecc418b3",
				Dimensions: 384, QueryPrefix: "query: ", DocumentPrefix: "passage: ", InputFormat: "text-v1",
				Scope: "full", Coverage: 1,
			},
		},
		Summary:           evaluation.Summary{Cases: 170, FilterLeaks: 0, ContextChecks: 110, ContextConsistent: 110, ContextConsistencyRate: 1, HWPAttachmentChecks: 1, HWPAttachmentPassed: 1},
		LanguageSummaries: map[string]evaluation.Summary{"ko": korean, "en": english},
		LanguageSplitSummaries: map[string]map[string]evaluation.Summary{
			"ko": {"holdout": koreanHoldout},
			"en": {"holdout": english},
		},
		Slices: map[string]evaluation.SliceMetrics{},
		Cases: []evaluation.CaseResult{
			{Group: "semantic", Language: "ko", EvidenceEligible: true, EvidenceRank: 1},
			{Group: "semantic-variant", Language: "ko", EvidenceEligible: true, EvidenceRank: 1},
			{Language: "ko", ExpectedStatus: "supported", ClaimRelation: "contradicts", EvidenceRank: 1},
		},
	}
	baseline := &englishNonRegressionBaseline{
		SchemaVersion: 1, FixtureSHA256: "fixture-sha",
		Embedding: englishEmbeddingContract{
			Model: "intfloat/multilingual-e5-small", Revision: "614241f622f53c4eeff9890bdc4f31cfecc418b3",
			Dimensions: 384, QueryPrefix: "query: ", DocumentPrefix: "passage: ", InputFormat: "text-v1",
		},
		Overall: englishBaselineMetricsFromSummary(english),
		Holdout: englishBaselineMetricsFromSummary(english),
	}
	return report, baseline
}

func TestVCSRevisionUsesExplicitReleaseRevision(t *testing.T) {
	t.Setenv("KRX_RULE_SERVER_COMMIT", "fd7cc25eb26af6dc75ad3d5ac08b3ba7bc7ee836+dirty")
	if got := vcsRevision(); got != "fd7cc25eb26af6dc75ad3d5ac08b3ba7bc7ee836+dirty" {
		t.Fatalf("vcsRevision() = %q", got)
	}
}
