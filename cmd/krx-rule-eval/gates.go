package main

import (
	"encoding/json"
	"fmt"
	evaluation "github.com/chromato99/krx-rule-mcp/internal/eval"
	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	"os"
)

type baselineLanguage struct {
	DocumentEligible     int `json:"document_eligible"`
	DocumentHitAt5       int `json:"document_hit_at_5"`
	EvidenceEligible     int `json:"evidence_eligible"`
	EvidenceBundleHitAt5 int `json:"evidence_bundle_hit_at_5"`
}

type codeBaseline struct {
	SchemaVersion           int                         `json:"schema_version"`
	EvaluatorVersion        string                      `json:"evaluator_version"`
	Reference               string                      `json:"reference"`
	CaseSetSHA256           string                      `json:"case_set_sha256"`
	FixtureSHA256           string                      `json:"fixture_sha256"`
	CorpusReleaseHash       string                      `json:"corpus_release_hash"`
	IndexGeneration         string                      `json:"index_generation"`
	EmbeddingArtifactDigest string                      `json:"embedding_artifact_digest"`
	LexiconDigest           string                      `json:"lexicon_digest"`
	RetrievalCandidateLimit int                         `json:"retrieval_candidate_limit"`
	Languages               map[string]baselineLanguage `json:"languages"`
}

func loadCodeBaseline(path string) (*codeBaseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var baseline codeBaseline
	if err := json.Unmarshal(data, &baseline); err != nil {
		return nil, err
	}
	if baseline.SchemaVersion != 2 || baseline.EvaluatorVersion != evaluation.EvaluatorVersion ||
		!isSHA256Hex(baseline.CaseSetSHA256) || !isSHA256Hex(baseline.CorpusReleaseHash) ||
		!isSHA256Hex(baseline.IndexGeneration) || !isSHA256Hex(baseline.EmbeddingArtifactDigest) ||
		!isSHA256Hex(baseline.LexiconDigest) || baseline.RetrievalCandidateLimit <= 0 || len(baseline.Languages) == 0 {
		return nil, fmt.Errorf("invalid retrieval comparison baseline")
	}
	for _, language := range baseline.Languages {
		if language.DocumentEligible <= 0 || language.DocumentHitAt5 <= 0 || language.DocumentHitAt5 > language.DocumentEligible ||
			language.EvidenceEligible <= 0 || language.EvidenceBundleHitAt5 <= 0 || language.EvidenceBundleHitAt5 > language.EvidenceEligible {
			return nil, fmt.Errorf("invalid retrieval baseline counts")
		}
	}
	return &baseline, nil
}

// This gate checks MCP retrieval and source integrity. It makes no claim about
// a caller LLM's answers, refusals, or clarification decisions.
func qualityGateFailures(report evaluation.Report, baseline *codeBaseline) []string {
	failures := embeddingIntegrityFailures(report.Provenance.Embedding)
	if report.Summary.Cases == 0 || len(report.Cases) == 0 {
		failures = append(failures, "evaluation cases missing")
	}
	if report.Summary.FilterLeaks != 0 {
		failures = append(failures, "filter leaks are non-zero")
	}
	if report.Summary.ContextChecks == 0 || report.Summary.ContextConsistencyRate < 1 {
		failures = append(failures, "context consistency < 100% or no context checks")
	}
	for _, item := range report.Cases {
		if !item.RetrievalContractValid || !item.ReturnedEvidenceValid {
			failures = append(failures, "retrieval/context contract failure: "+item.ID)
		}
	}
	if baseline == nil || report.Provenance.CaseSetSHA256 != baseline.CaseSetSHA256 {
		return append(failures, "baseline question/target contract mismatch")
	}
	if report.Provenance.EvaluatorVersion != baseline.EvaluatorVersion ||
		report.Provenance.CorpusReleaseHash != baseline.CorpusReleaseHash || report.Provenance.IndexGeneration != baseline.IndexGeneration ||
		report.Provenance.LexiconDigest != baseline.LexiconDigest || report.Provenance.RetrievalCandidateLimit != baseline.RetrievalCandidateLimit ||
		report.Provenance.Embedding == nil || report.Provenance.Embedding.ArtifactDigest != baseline.EmbeddingArtifactDigest {
		failures = append(failures, "baseline evaluator/corpus/index/embedding/lexicon/candidate-budget mismatch")
	}
	if len(report.LanguageSummaries) != len(baseline.Languages) {
		failures = append(failures, "baseline language coverage mismatch")
	}
	for name, previous := range baseline.Languages {
		current, ok := report.LanguageSummaries[name]
		if !ok || current.EvidenceEligible != previous.EvidenceEligible || current.DocumentEligible != previous.DocumentEligible {
			failures = append(failures, name+" baseline target coverage mismatch")
			continue
		}
		if current.DocumentHitAt5 < previous.DocumentHitAt5 {
			failures = append(failures, name+" document Hit@5 regressed")
		}
		if current.EvidenceBundleHitAt5 < previous.EvidenceBundleHitAt5 {
			failures = append(failures, name+" returned evidence bundle Hit@5 regressed")
		}
	}
	return failures
}

func validateHoldoutReservation(fixture evaluation.Fixture, repo *searchindex.Repository, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var reservation struct {
		SchemaVersion      int      `json:"schema_version"`
		CanonicalSourceIDs []string `json:"canonical_source_ids"`
	}
	if err := json.Unmarshal(data, &reservation); err != nil {
		return err
	}
	if reservation.SchemaVersion != 1 || len(reservation.CanonicalSourceIDs) == 0 {
		return fmt.Errorf("holdout reservation is empty or invalid")
	}
	reserved := map[string]bool{}
	for _, id := range reservation.CanonicalSourceIDs {
		reserved[id] = false
	}
	for _, item := range fixture.Cases {
		for _, target := range item.Expectation.Targets {
			doc, ok := repo.Documents[target.DocumentID]
			if !ok {
				return fmt.Errorf("holdout target document %q missing", target.DocumentID)
			}
			id := doc.SourceID
			if id == "" {
				id = doc.ID
			}
			if _, ok := reserved[id]; !ok {
				return fmt.Errorf("holdout target %q is not a reserved canonical source", id)
			}
			reserved[id] = true
		}
	}
	for id, covered := range reserved {
		if !covered {
			return fmt.Errorf("reserved source %q has no holdout target", id)
		}
	}
	return nil
}
