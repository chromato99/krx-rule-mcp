package evaluation

import (
	"time"

	mcpserver "github.com/chromato99/krx-rule-mcp/internal/mcp"
)

const EvaluatorVersion = "rag-evaluator-v1"

type Fixture struct {
	SchemaVersion  int             `json:"schema_version"`
	FixtureVersion string          `json:"fixture_version"`
	Name           string          `json:"name"`
	Source         FixtureSource   `json:"source"`
	Policies       FixturePolicies `json:"policies"`
	Cases          []Case          `json:"cases"`
}

type FixtureSource struct {
	URL            string `json:"url"`
	SHA256         string `json:"sha256"`
	EvaluationDate string `json:"evaluation_date"`
	OriginalCases  int    `json:"original_cases"`
}

type FixturePolicies struct {
	ChunkIDsInExpectations              bool `json:"chunk_ids_in_expectations"`
	ScoresAreConfidence                 bool `json:"scores_are_confidence"`
	ManualReviewExcludedFromAutoMetrics bool `json:"manual_review_excluded_from_automatic_evidence_metrics"`
}

type Case struct {
	ID          string      `json:"id"`
	Group       string      `json:"group"`
	Split       string      `json:"split"`
	Input       CaseInput   `json:"input"`
	Expectation Expectation `json:"expectation"`
}

type CaseInput struct {
	Query         string `json:"query"`
	Language      string `json:"language,omitempty"`
	DocumentType  string `json:"document_type,omitempty"`
	Category      string `json:"category,omitempty"`
	EffectiveFrom string `json:"effective_from,omitempty"`
	EffectiveTo   string `json:"effective_to,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

func (in CaseInput) MCPInput() mcpserver.SearchRulesInput {
	return mcpserver.SearchRulesInput{
		Query:         in.Query,
		Language:      in.Language,
		DocumentType:  in.DocumentType,
		Category:      in.Category,
		EffectiveFrom: in.EffectiveFrom,
		EffectiveTo:   in.EffectiveTo,
		Limit:         in.Limit,
	}
}

type Expectation struct {
	EvidenceStatus        string                     `json:"evidence_status"`
	ClaimRelation         string                     `json:"claim_relation"`
	TargetPolicy          string                     `json:"target_policy"`
	AtLeast               int                        `json:"at_least,omitempty"`
	Targets               []Target                   `json:"targets"`
	ClarificationRequired bool                       `json:"clarification_required,omitempty"`
	AllowResults          *bool                      `json:"allow_results,omitempty"`
	QueryExpansion        *QueryExpansionExpectation `json:"query_expansion,omitempty"`
}

type Target struct {
	DocumentID   string               `json:"document_id"`
	ArticleID    string               `json:"article_id,omitempty"`
	AttachmentID string               `json:"attachment_id,omitempty"`
	Evidence     *EvidenceExpectation `json:"evidence,omitempty"`
}

type EvidenceExpectation struct {
	MustContainAll   []string `json:"must_contain_all,omitempty"`
	MustContainAny   []string `json:"must_contain_any,omitempty"`
	MustNotContain   []string `json:"must_not_contain,omitempty"`
	NormalizedValues []any    `json:"normalized_values,omitempty"`
	ManualReview     bool     `json:"manual_review,omitempty"`
}

type QueryExpansionExpectation struct {
	RequiredEntryIDs []string `json:"required_entry_ids,omitempty"`
	RequiredTerms    []string `json:"required_terms,omitempty"`
}

type Provenance struct {
	EvaluatorVersion    string             `json:"evaluator_version"`
	ServerCommit        string             `json:"server_commit"`
	ReleaseGeneration   string             `json:"release_generation"`
	CorpusReleaseHash   string             `json:"corpus_release_hash"`
	IndexGeneration     string             `json:"index_generation"`
	IndexSourceHash     string             `json:"index_source_hash"`
	IndexBuildHash      string             `json:"index_build_hash"`
	IndexerVersion      string             `json:"indexer_version"`
	BM25ArtifactDigest  string             `json:"bm25_artifact_digest"`
	BM25SnapshotVersion uint16             `json:"bm25_snapshot_version"`
	Embedding           *EmbeddingIdentity `json:"embedding,omitempty"`
	LexiconDigest       string             `json:"lexicon_digest"`
	RuntimeVectorMode   string             `json:"runtime_vector_mode"`
	RetrievalContract   string             `json:"retrieval_contract"`
	AnswerabilityGate   string             `json:"answerability_gate"`
	FixtureVersion      string             `json:"fixture_version"`
	FixtureSHA256       string             `json:"fixture_sha256"`
	SourceFixtureSHA256 string             `json:"source_fixture_sha256"`
	Extra               map[string]string  `json:"extra,omitempty"`
}

type EmbeddingIdentity struct {
	Model              string  `json:"model"`
	Revision           string  `json:"revision"`
	Dimensions         int     `json:"dimensions"`
	QueryPrefix        string  `json:"query_prefix"`
	DocumentPrefix     string  `json:"document_prefix"`
	Scope              string  `json:"scope"`
	InputFormat        string  `json:"input_format"`
	ExpectedChunkCount int     `json:"expected_chunk_count"`
	StoredVectorCount  int     `json:"stored_vector_count"`
	Coverage           float64 `json:"coverage"`
	ArtifactDigest     string  `json:"artifact_digest"`
	MetadataDigest     string  `json:"metadata_digest"`
}

type Report struct {
	SchemaVersion int                     `json:"schema_version"`
	GeneratedAt   time.Time               `json:"generated_at"`
	Provenance    Provenance              `json:"provenance"`
	Summary       Summary                 `json:"summary"`
	Slices        map[string]SliceMetrics `json:"slices"`
	Cases         []CaseResult            `json:"cases"`
}

type Summary struct {
	Cases                      int     `json:"cases"`
	SupportedCases             int     `json:"supported_cases"`
	InsufficientCases          int     `json:"insufficient_cases"`
	AmbiguousCases             int     `json:"ambiguous_cases"`
	DocumentEligible           int     `json:"document_eligible"`
	DocumentHitAt1             int     `json:"document_hit_at_1"`
	DocumentHitAt5             int     `json:"document_hit_at_5"`
	DocumentHitAt5Rate         float64 `json:"document_hit_at_5_rate"`
	MRRAt5                     float64 `json:"mrr_at_5"`
	EvidenceEligible           int     `json:"evidence_eligible"`
	EvidenceHitAt1             int     `json:"evidence_hit_at_1"`
	EvidenceHitAt1Rate         float64 `json:"evidence_hit_at_1_rate"`
	EvidenceRecallAt3          int     `json:"evidence_recall_at_3"`
	EvidenceRecallAt3Rate      float64 `json:"evidence_recall_at_3_rate"`
	ManualEvidenceEligible     int     `json:"manual_evidence_eligible"`
	ManualEvidenceHitAt1       int     `json:"manual_evidence_hit_at_1"`
	ManualEvidenceHitAt1Rate   float64 `json:"manual_evidence_hit_at_1_rate"`
	StatusCorrect              int     `json:"status_correct"`
	StatusAccuracy             float64 `json:"status_accuracy"`
	InsufficientRefused        int     `json:"insufficient_refused"`
	InsufficientRefusalRate    float64 `json:"insufficient_refusal_rate"`
	FalseSupported             int     `json:"false_supported"`
	FalseSupportedRate         float64 `json:"false_supported_rate"`
	AmbiguousClarified         int     `json:"ambiguous_clarified"`
	AmbiguousClarificationRate float64 `json:"ambiguous_clarification_rate"`
	ContextChecks              int     `json:"context_checks"`
	ContextConsistent          int     `json:"context_consistent"`
	ContextConsistencyRate     float64 `json:"context_consistency_rate"`
	FilterLeaks                int     `json:"filter_leaks"`
	ExpansionChecks            int     `json:"expansion_checks"`
	ExpansionPassed            int     `json:"expansion_passed"`
	EnglishCanonicalChecks     int     `json:"english_canonical_checks"`
	EnglishCanonicalPassed     int     `json:"english_canonical_passed"`
	HWPAttachmentChecks        int     `json:"hwp_attachment_checks"`
	HWPAttachmentPassed        int     `json:"hwp_attachment_passed"`
	AutomaticPassed            int     `json:"automatic_passed"`
	ManualReviewCases          int     `json:"manual_review_cases"`
	P95LatencyMillis           float64 `json:"p95_latency_ms"`
}

type SliceMetrics struct {
	Cases              int     `json:"cases"`
	DocumentEligible   int     `json:"document_eligible"`
	DocumentHitAt5     int     `json:"document_hit_at_5"`
	EvidenceEligible   int     `json:"evidence_eligible"`
	EvidenceHitAt1     int     `json:"evidence_hit_at_1"`
	EvidenceHitAt1Rate float64 `json:"evidence_hit_at_1_rate"`
	EvidenceRecallAt3  int     `json:"evidence_recall_at_3"`
	StatusCorrect      int     `json:"status_correct"`
}

type CaseResult struct {
	ID                    string           `json:"id"`
	Group                 string           `json:"group"`
	Split                 string           `json:"split"`
	ExpectedStatus        string           `json:"expected_status"`
	ObservedStatus        string           `json:"observed_status"`
	Answerable            bool             `json:"answerable"`
	StatusCorrect         bool             `json:"status_correct"`
	ClarificationProvided bool             `json:"clarification_provided"`
	DocumentRank          int              `json:"document_rank,omitempty"`
	EvidenceRank          int              `json:"evidence_rank,omitempty"`
	EvidenceEligible      bool             `json:"evidence_eligible"`
	EvidenceManualReview  bool             `json:"evidence_manual_review"`
	FilterLeaks           int              `json:"filter_leaks"`
	ExpansionPassed       *bool            `json:"expansion_passed,omitempty"`
	ContextChecks         []ContextCheck   `json:"context_checks,omitempty"`
	Results               []ObservedResult `json:"results"`
	Answerability         any              `json:"answerability"`
	Mode                  string           `json:"mode"`
	ElapsedMillis         float64          `json:"elapsed_ms"`
	AutomaticPassed       bool             `json:"automatic_passed"`
	Failures              []string         `json:"failures,omitempty"`
}

type ObservedResult struct {
	ID                    string                              `json:"id"`
	Rank                  int                                 `json:"rank"`
	Score                 float64                             `json:"score"`
	BM25Score             float64                             `json:"bm25_score,omitempty"`
	VectorScore           float64                             `json:"vector_score,omitempty"`
	MatchedChunkID        string                              `json:"matched_chunk_id,omitempty"`
	ArticleID             string                              `json:"article_id,omitempty"`
	AttachmentIDs         []string                            `json:"attachment_ids,omitempty"`
	Evidence              []ObservedEvidence                  `json:"evidence,omitempty"`
	CanonicalKoreanSource *mcpserver.CanonicalKoreanSourceDTO `json:"canonical_korean_source,omitempty"`
}

type ObservedEvidence struct {
	ChunkID      string   `json:"chunk_id"`
	Source       string   `json:"source"`
	AttachmentID string   `json:"attachment_id,omitempty"`
	ArticleID    string   `json:"article_id,omitempty"`
	HeadingPath  []string `json:"heading_path,omitempty"`
	Score        float64  `json:"score"`
	BM25Score    float64  `json:"bm25_score,omitempty"`
	VectorScore  float64  `json:"vector_score,omitempty"`
}

type ContextCheck struct {
	ChunkID          string `json:"chunk_id"`
	ExpectedDocument string `json:"expected_document"`
	ObservedDocument string `json:"observed_document,omitempty"`
	ChunkPresent     bool   `json:"chunk_present"`
	DocumentMatches  bool   `json:"document_matches"`
	TargetMatches    bool   `json:"target_matches"`
	Error            string `json:"error,omitempty"`
}
