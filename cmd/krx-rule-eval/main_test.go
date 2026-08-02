package main

import (
	"slices"
	"testing"

	evaluation "github.com/chromato99/krx-rule-mcp/internal/eval"
)

func TestQualityGateThresholds(t *testing.T) {
	passing := evaluation.Report{Summary: evaluation.Summary{
		Cases:                      120,
		DocumentHitAt5Rate:         0.95,
		MRRAt5:                     0.90,
		EvidenceHitAt1Rate:         0.90,
		EvidenceRecallAt3Rate:      0.95,
		ManualEvidenceEligible:     1,
		ManualEvidenceHitAt1Rate:   0.90,
		InsufficientRefusalRate:    0.95,
		FalseSupportedRate:         0.05,
		AmbiguousClarificationRate: 0.90,
		ContextConsistencyRate:     1,
		P95LatencyMillis:           299.999,
		HWPAttachmentChecks:        1,
		HWPAttachmentPassed:        1,
		EnglishCanonicalChecks:     1,
		EnglishCanonicalPassed:     1,
	}}
	if failures := qualityGateFailures(passing); len(failures) != 0 {
		t.Fatalf("threshold values should pass: %v", failures)
	}
	tooSmall := passing
	tooSmall.Summary.Cases = 119
	if failures := qualityGateFailures(tooSmall); !slices.Contains(failures, "evaluation cases < 120") {
		t.Fatalf("case-count failures = %v", failures)
	}

	tests := []struct {
		name string
		want string
		edit func(*evaluation.Summary)
	}{
		{name: "mrr", want: "MRR@5 < 0.90", edit: func(summary *evaluation.Summary) { summary.MRRAt5 = 0.899 }},
		{name: "evidence recall", want: "evidence Recall@3 < 95%", edit: func(summary *evaluation.Summary) { summary.EvidenceRecallAt3Rate = 0.949 }},
		{name: "latency", want: "p95 latency >= 300ms", edit: func(summary *evaluation.Summary) { summary.P95LatencyMillis = 300 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := passing
			test.edit(&report.Summary)
			if failures := qualityGateFailures(report); !slices.Contains(failures, test.want) {
				t.Fatalf("failures = %v, want %q", failures, test.want)
			}
		})
	}
	semanticRegression := passing
	semanticRegression.Slices = map[string]evaluation.SliceMetrics{
		"semantic": {EvidenceEligible: 10, EvidenceHitAt1Rate: 0.89},
	}
	if failures := qualityGateFailures(semanticRegression); !slices.Contains(failures, "semantic evidence Hit@1 < 90%") {
		t.Fatalf("semantic failures = %v", failures)
	}

	falsePremiseRegression := passing
	falsePremiseRegression.Cases = make([]evaluation.CaseResult, 10)
	for index := range falsePremiseRegression.Cases {
		falsePremiseRegression.Cases[index] = evaluation.CaseResult{Group: "false-premise", EvidenceRank: 1}
	}
	falsePremiseRegression.Cases[8].EvidenceRank = 2
	falsePremiseRegression.Cases[9].EvidenceRank = 2
	if failures := qualityGateFailures(falsePremiseRegression); !slices.Contains(failures, "false-premise evidence Hit@1 < 90%") {
		t.Fatalf("false-premise failures = %v", failures)
	}
}

func TestVCSRevisionUsesExplicitReleaseRevision(t *testing.T) {
	t.Setenv("KRX_RULE_SERVER_COMMIT", "fd7cc25eb26af6dc75ad3d5ac08b3ba7bc7ee836+dirty")
	if got := vcsRevision(); got != "fd7cc25eb26af6dc75ad3d5ac08b3ba7bc7ee836+dirty" {
		t.Fatalf("vcsRevision() = %q", got)
	}
}
