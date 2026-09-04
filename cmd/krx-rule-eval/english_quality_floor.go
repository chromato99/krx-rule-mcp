package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	evaluation "github.com/chromato99/krx-rule-mcp/internal/eval"
)

type englishQualityMetrics struct {
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

type englishQualityFloor struct {
	SchemaVersion int                   `json:"schema_version"`
	FixtureSHA256 string                `json:"fixture_sha256"`
	Overall       englishQualityMetrics `json:"overall"`
	Holdout       englishQualityMetrics `json:"holdout"`
}

func loadEnglishQualityFloor(path string) (englishQualityFloor, error) {
	file, err := os.Open(path)
	if err != nil {
		return englishQualityFloor{}, fmt.Errorf("open English quality floor: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var floor englishQualityFloor
	if err := decoder.Decode(&floor); err != nil {
		return englishQualityFloor{}, fmt.Errorf("decode English quality floor: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return englishQualityFloor{}, fmt.Errorf("decode English quality floor: trailing JSON value")
	}
	if floor.SchemaVersion != 1 {
		return englishQualityFloor{}, fmt.Errorf("English quality floor schema_version = %d, want 1", floor.SchemaVersion)
	}
	if !isSHA256Hex(floor.FixtureSHA256) {
		return englishQualityFloor{}, fmt.Errorf("English quality floor fixture identity is incomplete")
	}
	if err := validateEnglishQualityMetrics("overall", floor.Overall); err != nil {
		return englishQualityFloor{}, err
	}
	if err := validateEnglishQualityMetrics("holdout", floor.Holdout); err != nil {
		return englishQualityFloor{}, err
	}
	return floor, nil
}

func validateEnglishQualityMetrics(label string, metrics englishQualityMetrics) error {
	invalid := metrics.Cases <= 0 ||
		metrics.DocumentEligible < 0 || metrics.DocumentEligible > metrics.Cases ||
		metrics.DocumentHitAt5 < 0 || metrics.DocumentHitAt5 > metrics.DocumentEligible ||
		metrics.MRRAt5 < 0 || metrics.MRRAt5 > 1 ||
		metrics.EvidenceEligible < 0 || metrics.EvidenceEligible > metrics.Cases ||
		metrics.EvidenceHitAt1 < 0 || metrics.EvidenceHitAt1 > metrics.EvidenceEligible ||
		metrics.EvidenceRecallAt3 < 0 || metrics.EvidenceRecallAt3 > metrics.EvidenceEligible ||
		metrics.CandidateRecallAt64 < 0 || metrics.CandidateRecallAt64 > metrics.EvidenceEligible ||
		metrics.StatusCorrect < 0 || metrics.StatusCorrect > metrics.Cases ||
		metrics.InsufficientCases < 0 || metrics.InsufficientCases > metrics.Cases ||
		metrics.InsufficientRefused < 0 || metrics.InsufficientRefused > metrics.InsufficientCases ||
		metrics.FalseSupported < 0 || metrics.FalseSupported > metrics.InsufficientCases ||
		metrics.EnglishCanonicalChecks < 0 || metrics.EnglishCanonicalChecks > metrics.Cases ||
		metrics.EnglishCanonicalPassed < 0 || metrics.EnglishCanonicalPassed > metrics.EnglishCanonicalChecks
	if invalid {
		return fmt.Errorf("English quality floor %s metrics are invalid", label)
	}
	return nil
}

func englishQualityMetricsFromSummary(summary evaluation.Summary) englishQualityMetrics {
	return englishQualityMetrics{
		Cases: summary.Cases, DocumentEligible: summary.DocumentEligible,
		DocumentHitAt5: summary.DocumentHitAt5, MRRAt5: summary.MRRAt5,
		EvidenceEligible: summary.EvidenceEligible, EvidenceHitAt1: summary.EvidenceHitAt1,
		EvidenceRecallAt3: summary.EvidenceRecallAt3, CandidateRecallAt64: summary.CandidateEvidenceRecall64,
		StatusCorrect: summary.StatusCorrect, InsufficientCases: summary.InsufficientCases,
		InsufficientRefused: summary.InsufficientRefused, FalseSupported: summary.FalseSupported,
		EnglishCanonicalChecks: summary.EnglishCanonicalChecks, EnglishCanonicalPassed: summary.EnglishCanonicalPassed,
	}
}
