package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	evaluation "github.com/chromato99/krx-rule-mcp/internal/eval"
)

type englishEmbeddingContract struct {
	Model          string `json:"model"`
	Revision       string `json:"revision"`
	Dimensions     int    `json:"dimensions"`
	QueryPrefix    string `json:"query_prefix"`
	DocumentPrefix string `json:"document_prefix"`
	InputFormat    string `json:"input_format"`
}

type englishBaselineMetrics struct {
	Cases                  int     `json:"cases"`
	DocumentEligible       int     `json:"document_eligible"`
	DocumentHitAt5         int     `json:"document_hit_at_5"`
	MRRAt5                 float64 `json:"mrr_at_5"`
	EvidenceEligible       int     `json:"evidence_eligible"`
	EvidenceHitAt1         int     `json:"evidence_hit_at_1"`
	EvidenceRecallAt3      int     `json:"evidence_recall_at_3"`
	CandidateRecallAt64    int     `json:"candidate_recall_at_64"`
	StatusCorrect          int     `json:"status_correct"`
	InsufficientCases      int     `json:"insufficient_cases"`
	InsufficientRefused    int     `json:"insufficient_refused"`
	FalseSupported         int     `json:"false_supported"`
	EnglishCanonicalChecks int     `json:"english_canonical_checks"`
	EnglishCanonicalPassed int     `json:"english_canonical_passed"`
}

type englishNonRegressionBaseline struct {
	SchemaVersion int                      `json:"schema_version"`
	FixtureSHA256 string                   `json:"fixture_sha256"`
	Embedding     englishEmbeddingContract `json:"embedding"`
	Overall       englishBaselineMetrics   `json:"overall"`
	Holdout       englishBaselineMetrics   `json:"holdout"`
}

func loadEnglishNonRegressionBaseline(path string) (englishNonRegressionBaseline, error) {
	file, err := os.Open(path)
	if err != nil {
		return englishNonRegressionBaseline{}, fmt.Errorf("open English non-regression baseline: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var baseline englishNonRegressionBaseline
	if err := decoder.Decode(&baseline); err != nil {
		return englishNonRegressionBaseline{}, fmt.Errorf("decode English non-regression baseline: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return englishNonRegressionBaseline{}, fmt.Errorf("decode English non-regression baseline: trailing JSON value")
	}
	if baseline.SchemaVersion != 1 {
		return englishNonRegressionBaseline{}, fmt.Errorf("English non-regression baseline schema_version = %d, want 1", baseline.SchemaVersion)
	}
	if baseline.FixtureSHA256 == "" || baseline.Embedding.Model == "" || baseline.Embedding.Revision == "" {
		return englishNonRegressionBaseline{}, fmt.Errorf("English non-regression baseline identity is incomplete")
	}
	if baseline.Overall.Cases <= 0 || baseline.Holdout.Cases <= 0 {
		return englishNonRegressionBaseline{}, fmt.Errorf("English non-regression baseline coverage is incomplete")
	}
	return baseline, nil
}

func englishBaselineMetricsFromSummary(summary evaluation.Summary) englishBaselineMetrics {
	return englishBaselineMetrics{
		Cases: summary.Cases, DocumentEligible: summary.DocumentEligible,
		DocumentHitAt5: summary.DocumentHitAt5, MRRAt5: summary.MRRAt5,
		EvidenceEligible: summary.EvidenceEligible, EvidenceHitAt1: summary.EvidenceHitAt1,
		EvidenceRecallAt3: summary.EvidenceRecallAt3, CandidateRecallAt64: summary.CandidateEvidenceRecall64,
		StatusCorrect: summary.StatusCorrect, InsufficientCases: summary.InsufficientCases,
		InsufficientRefused: summary.InsufficientRefused, FalseSupported: summary.FalseSupported,
		EnglishCanonicalChecks: summary.EnglishCanonicalChecks, EnglishCanonicalPassed: summary.EnglishCanonicalPassed,
	}
}
