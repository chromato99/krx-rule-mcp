package main

import (
	evaluation "github.com/chromato99/krx-rule-mcp/internal/eval"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRepositoryBaselinePreservesQuestionContracts(t *testing.T) {
	baseline, err := loadCodeBaseline(filepath.Join("..", "..", "eval", "baselines", "rag-retrieval-before.json"))
	if err != nil {
		t.Fatal(err)
	}
	fixture, _, err := evaluation.LoadFixture(filepath.Join("..", "..", "eval", "golden", "rag-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.FixtureContractHash(fixture) != baseline.CaseSetSHA256 {
		t.Fatal("question/target contracts changed without a new comparison baseline")
	}
	for _, item := range fixture.Cases {
		if item.Split == "holdout" {
			t.Fatal("consumed cases still labelled holdout")
		}
	}
}

func TestDiagnosticFiltersCannotUseGate(t *testing.T) {
	if err := validateEvalOptions("validation", "", true); err == nil {
		t.Fatal("split can bypass full gate")
	}
	if err := validateEvalOptions("", "audit-", true); err == nil {
		t.Fatal("case filter can bypass full gate")
	}
	if err := validateEvalOptions("validation", "audit-", false); err == nil {
		t.Fatal("conflicting filters accepted")
	}
	if err := validateEvalOptions("validation", "", false); err != nil {
		t.Fatal(err)
	}
}

func gateReport() (evaluation.Report, *codeBaseline) {
	summary := evaluation.Summary{Cases: 1, EvidenceEligible: 1, EvidenceBundleHitAt5: 1, DocumentEligible: 1, DocumentHitAt5: 1, DocumentHitAt5Rate: 1, ContextChecks: 1, ContextConsistencyRate: 1}
	digest := strings.Repeat("a", 64)
	return evaluation.Report{
		Provenance: evaluation.Provenance{EvaluatorVersion: evaluation.EvaluatorVersion, CaseSetSHA256: digest, CorpusReleaseHash: digest, IndexGeneration: digest, LexiconDigest: digest, RetrievalCandidateLimit: 120,
			Embedding: &evaluation.EmbeddingIdentity{Model: "vendor/model", Dimensions: 384, InputFormat: "text-v1", Scope: "full", ExpectedChunkCount: 100, StoredVectorCount: 100, Coverage: 1, ArtifactDigest: digest, MetadataDigest: digest}},
		Summary: summary, LanguageSummaries: map[string]evaluation.Summary{"ko": summary, "en": summary},
		Cases: []evaluation.CaseResult{{ID: "case", RetrievalContractValid: true, ReturnedEvidenceValid: true}},
	}, &codeBaseline{EvaluatorVersion: evaluation.EvaluatorVersion, CaseSetSHA256: digest, CorpusReleaseHash: digest, IndexGeneration: digest, EmbeddingArtifactDigest: digest, LexiconDigest: digest, RetrievalCandidateLimit: 120, Languages: map[string]baselineLanguage{"ko": {DocumentEligible: 1, DocumentHitAt5: 1, EvidenceEligible: 1, EvidenceBundleHitAt5: 1}, "en": {DocumentEligible: 1, DocumentHitAt5: 1, EvidenceEligible: 1, EvidenceBundleHitAt5: 1}}}
}

func TestRetrievalGatePreservesCoverageAndSourceContracts(t *testing.T) {
	report, baseline := gateReport()
	if failures := qualityGateFailures(report, baseline); len(failures) != 0 {
		t.Fatal(failures)
	}
	for _, test := range []struct {
		name string
		edit func(*evaluation.Report)
		want string
	}{
		{"evidence", func(r *evaluation.Report) {
			s := r.LanguageSummaries["ko"]
			s.EvidenceBundleHitAt5 = 0
			r.LanguageSummaries["ko"] = s
		}, "ko returned evidence bundle Hit@5 regressed"},
		{"document", func(r *evaluation.Report) {
			s := r.LanguageSummaries["en"]
			s.DocumentHitAt5 = 0
			r.LanguageSummaries["en"] = s
		}, "en document Hit@5 regressed"},
		{"context", func(r *evaluation.Report) { r.Cases[0].ReturnedEvidenceValid = false }, "retrieval/context contract failure: case"},
		{"count", func(r *evaluation.Report) { r.Cases[0].RetrievalContractValid = false }, "retrieval/context contract failure: case"},
		{"labels", func(r *evaluation.Report) { r.Provenance.CaseSetSHA256 = strings.Repeat("d", 64) }, "baseline question/target contract mismatch"},
		{"index", func(r *evaluation.Report) { r.Provenance.IndexGeneration = strings.Repeat("d", 64) }, "baseline evaluator/corpus/index/embedding/lexicon/candidate-budget mismatch"},
		{"filters", func(r *evaluation.Report) { r.Summary.FilterLeaks = 1 }, "filter leaks are non-zero"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, b := gateReport()
			test.edit(&r)
			if failures := qualityGateFailures(r, b); !slices.Contains(failures, test.want) {
				t.Fatalf("failures=%v want=%s", failures, test.want)
			}
		})
	}
	// Related results for an unanswerable question are candidates, not accepted answers.
	report.Summary.NegativeCasesWithCandidates = 1
	report.Summary.AmbiguousCasesWithCandidates = 1
	if failures := qualityGateFailures(report, baseline); len(failures) != 0 {
		t.Fatal(failures)
	}
}

func TestEmbeddingIntegrityFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*evaluation.EmbeddingIdentity)
		want string
	}{
		{"model", func(v *evaluation.EmbeddingIdentity) { v.Model = "" }, "embedding model identity is missing"},
		{"dimensions", func(v *evaluation.EmbeddingIdentity) { v.Dimensions = 0 }, "embedding dimensions must be positive"},
		{"scope", func(v *evaluation.EmbeddingIdentity) { v.Scope = "sample" }, "embedding vector scope is not full"},
		{"coverage", func(v *evaluation.EmbeddingIdentity) { v.StoredVectorCount-- }, "embedding vector coverage is incomplete"},
		{"digest", func(v *evaluation.EmbeddingIdentity) { v.MetadataDigest = "" }, "embedding artifact provenance is incomplete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, _ := gateReport()
			test.edit(r.Provenance.Embedding)
			if failures := embeddingIntegrityFailures(r.Provenance.Embedding); !slices.Contains(failures, test.want) {
				t.Fatal(failures)
			}
		})
	}
}
