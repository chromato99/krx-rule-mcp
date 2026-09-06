package evaluation

import (
	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	"testing"
)

func TestCandidateDiagnosticsDistinguishSplitOwnerEvidence(t *testing.T) {
	expectation := Expectation{TargetPolicy: "any", Targets: []Target{{DocumentID: "rule", ArticleID: "제4조", Evidence: &EvidenceExpectation{MustContainAll: []string{"신고 기한", "계약 수량"}}}}}
	candidates := []searchindex.ChunkCandidate{
		{DocumentID: "rule", ArticleID: "제4조", ChunkID: "a", Text: "신고 기한", BaselineRank: 1},
		{DocumentID: "rule", ArticleID: "제4조", ChunkID: "b", Text: "계약 수량", BaselineRank: 2},
	}
	if rank := candidatePolicyRank(expectation, candidates, func(c searchindex.ChunkCandidate) int { return c.BaselineRank }); rank != 0 {
		t.Fatal("single chunk unexpectedly satisfied the whole expectation")
	}
	if !candidateBundleMatches(expectation, candidates) {
		t.Fatal("split evidence reported as unavailable")
	}
	candidates[1].ArticleID = "제5조"
	if candidateBundleMatches(expectation, candidates) {
		t.Fatal("candidate diagnostic borrowed a different article")
	}
	expectation.EvidenceStatus = "supported"
	stages := classifyFailureStages(Case{Expectation: expectation}, CaseResult{EvidenceEligible: true, RetrievalContractValid: true, ReturnedEvidenceValid: true, EvidenceBundleAt5Matched: true, DocumentRank: 1, CandidateOwnerRank: 1})
	if len(stages) != 1 || stages[0] != "candidate-evidence-gap" {
		t.Fatalf("owner presence confused with complete evidence: %v", stages)
	}
}
