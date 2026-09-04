package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	evaluation "github.com/chromato99/krx-rule-mcp/internal/eval"
	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	mcpserver "github.com/chromato99/krx-rule-mcp/internal/mcp"
)

func main() {
	dataDir := flag.String("data-dir", env("KRX_RULE_DATA_DIR", "../krx-rule-markdown/data"), "schema-v2 corpus directory")
	indexDir := flag.String("index-dir", env("KRX_RULE_INDEX_DIR", "index"), "immutable index generation directory")
	fixturePath := flag.String("fixture", "eval/golden/rag-v1.json", "versioned golden fixture")
	sourceFixturePath := flag.String("source-fixture", "eval/source/rag-queries-2026-07-28.json", "checksummed original 50-case fixture")
	lexiconPath := flag.String("domain-lexicon", env("KRX_DOMAIN_LEXICON_PATH", searchindex.DefaultDomainLexiconPath), "domain lexicon YAML")
	outputPath := flag.String("output", "eval/results/rag-v1-latest.json", "evaluation report output")
	englishQualityFloorPath := flag.String("english-quality-floor", "eval/baselines/rag-v1-english-floor.json", "model-independent English minimum quality floor")
	split := flag.String("split", "", "optional fixture split for diagnostic runs")
	casePrefix := flag.String("case-prefix", "", "optional case-id prefix for diagnostic runs")
	vectorEnabled := flag.Bool("vector", envBool("KRX_VECTOR_SEARCH_ENABLED"), "load full vector generation and query embedder")
	requireVector := flag.Bool("require-vector", false, "fail unless full vector generation and query embedder are available")
	rerankerEnabled := flag.Bool("reranker", envBool("KRX_RERANKER_ENABLED"), "rerank bounded Korean chunk candidates through TEI")
	requireReranker := flag.Bool("require-reranker", false, "fail unless the configured reranker is available for Korean queries")
	rerankerAll := flag.Bool("reranker-all", false, "diagnostic: rerank every eligible Korean query instead of weak supported evidence only")
	rerankerTimeout := flag.Duration("reranker-timeout", 30*time.Minute, "per-query reranker deadline")
	retrievalCandidates := flag.Int("candidate-limit", 0, "optional first-stage candidate limit per channel, max 512")
	failOnGate := flag.Bool("fail-on-gate", false, "exit non-zero when release quality gates fail")
	flag.Parse()
	fatalIf(validateEvalOptions(*split, *casePrefix, *failOnGate))
	if *retrievalCandidates < 0 || *retrievalCandidates > 512 {
		fatalIf(fmt.Errorf("--candidate-limit must be between 1 and 512, or 0 for the default"))
	}

	fixture, fixtureHash, err := evaluation.LoadFixture(*fixturePath)
	fatalIf(err)
	fatalIf(evaluation.VerifySourceFixture(*sourceFixturePath, fixture.Source.SHA256, fixture.Source.OriginalCases))

	loadOptions := searchindex.RepositoryLoadOptions{
		VectorEnabled: *vectorEnabled || *requireVector,
		RequireVector: *requireVector,
	}
	repo, err := searchindex.LoadRepositoryGeneration(*dataDir, *indexDir, loadOptions)
	fatalIf(err)
	fatalIf(evaluation.ValidateFixtureGrounding(fixture, repo))
	lexicon, lexiconDigest, err := searchindex.LoadDomainLexiconWithDigest(*lexiconPath)
	fatalIf(err)

	var embedder searchindex.Embedder
	if *vectorEnabled || *requireVector {
		configured, err := searchindex.NewQueryEmbedderFromEnv()
		fatalIf(err)
		if !repo.Engine.HasVectors() {
			fatalIf(fmt.Errorf("vector evaluation requested but no compatible vectors were loaded"))
		}
		embedder = configured
	}
	var reranker searchindex.Reranker
	rerankerCandidates := 0
	if *rerankerEnabled || *requireReranker {
		configured, err := searchindex.NewTEIRerankerFromEnv()
		fatalIf(err)
		verifyCtx, cancel := context.WithTimeout(context.Background(), *rerankerTimeout)
		verifyErr := configured.VerifyReranker(verifyCtx)
		cancel()
		fatalIf(verifyErr)
		reranker = configured
		rerankerCandidates, err = searchindex.RerankerCandidateLimitFromEnv()
		fatalIf(err)
	}
	runtimeVectorMode := "bm25"
	if embedder != nil {
		runtimeVectorMode = "bm25+vector"
	}
	provenance, releaseGeneration, err := buildProvenance(repo, lexiconDigest, runtimeVectorMode, *retrievalCandidates, reranker, rerankerCandidates, *rerankerAll, *rerankerTimeout, fixture, fixtureHash)
	fatalIf(err)
	service := &mcpserver.Service{
		Repo:                repo,
		Embedder:            embedder,
		VectorRequired:      *requireVector,
		Reranker:            reranker,
		RerankerRequired:    *requireReranker,
		RerankerCandidates:  rerankerCandidates,
		RerankerAll:         *rerankerAll,
		RetrievalCandidates: *retrievalCandidates,
		RerankerTimeout:     *rerankerTimeout,
		DomainLexicon:       lexicon,
		ReleaseGeneration:   releaseGeneration,
	}
	var report evaluation.Report
	if *casePrefix != "" {
		report, err = evaluation.RunCasePrefix(context.Background(), fixture, *casePrefix, service, provenance)
	} else if *split == "" {
		report, err = evaluation.Run(context.Background(), fixture, service, provenance)
	} else {
		report, err = evaluation.RunSplit(context.Background(), fixture, *split, service, provenance)
	}
	fatalIf(err)
	fatalIf(writeReport(*outputPath, report))

	fmt.Printf("rag-eval cases=%d document_hit@5=%.3f mrr@5=%.3f evidence_hit@1=%.3f evidence_recall@3=%.3f candidate_recall@64=%.3f reranker_pool_hit=%.3f refusal=%.3f ambiguous=%.3f context=%.3f filter_leaks=%d search_p95_ms=%.2f reranker_p95_ms=%.2f eval_p95_ms=%.2f report=%s\n",
		report.Summary.Cases,
		report.Summary.DocumentHitAt5Rate,
		report.Summary.MRRAt5,
		report.Summary.EvidenceHitAt1Rate,
		report.Summary.EvidenceRecallAt3Rate,
		report.Summary.CandidateEvidenceRecall64Rate,
		report.Summary.RerankerPoolHitRate,
		report.Summary.InsufficientRefusalRate,
		report.Summary.AmbiguousClarificationRate,
		report.Summary.ContextConsistencyRate,
		report.Summary.FilterLeaks,
		report.Summary.P95SearchLatencyMillis,
		report.Summary.P95RerankerLatencyMillis,
		report.Summary.P95LatencyMillis,
		*outputPath,
	)
	if *failOnGate {
		englishFloor, err := loadEnglishQualityFloor(*englishQualityFloorPath)
		fatalIf(err)
		failures := qualityGateFailures(report, &englishFloor)
		if len(failures) > 0 {
			for _, failure := range failures {
				fmt.Fprintln(os.Stderr, "gate:", failure)
			}
			os.Exit(1)
		}
	}
}

func validateEvalOptions(split, casePrefix string, failOnGate bool) error {
	if strings.TrimSpace(split) != "" && strings.TrimSpace(casePrefix) != "" {
		return fmt.Errorf("--split and --case-prefix cannot be combined")
	}
	if (strings.TrimSpace(split) != "" || strings.TrimSpace(casePrefix) != "") && failOnGate {
		return fmt.Errorf("--fail-on-gate cannot be combined with diagnostic filters")
	}
	return nil
}

type releaseVectorDescriptor struct {
	ArtifactDigest     string `json:"artifact_digest"`
	MetadataDigest     string `json:"metadata_digest"`
	GenerationID       string `json:"generation_id"`
	IndexSourceHash    string `json:"index_source_hash"`
	IndexBuildHash     string `json:"index_build_hash"`
	Model              string `json:"model"`
	ModelRevision      string `json:"model_revision"`
	Dimensions         int    `json:"dimensions"`
	QueryPrefix        string `json:"query_prefix"`
	DocumentPrefix     string `json:"document_prefix"`
	InputFormat        string `json:"input_format"`
	Scope              string `json:"scope"`
	ExpectedChunkCount int    `json:"expected_chunk_count"`
	StoredVectorCount  int    `json:"stored_vector_count"`
}

type releaseDescriptor struct {
	Schema                  string                       `json:"schema"`
	CorpusReleaseHash       string                       `json:"corpus_release_hash"`
	IndexSourceHash         string                       `json:"index_source_hash"`
	IndexBuildHash          string                       `json:"index_build_hash"`
	BM25ArtifactDigest      string                       `json:"bm25_artifact_digest"`
	BM25SnapshotVersion     uint16                       `json:"bm25_snapshot_version"`
	IndexerVersion          string                       `json:"indexer_version"`
	Vector                  *releaseVectorDescriptor     `json:"vector,omitempty"`
	Reranker                *evaluation.RerankerIdentity `json:"reranker,omitempty"`
	DomainLexiconDigest     string                       `json:"domain_lexicon_digest"`
	RuntimeVectorMode       string                       `json:"runtime_vector_mode"`
	RetrievalCandidateLimit int                          `json:"retrieval_candidate_limit"`
	ServerImageDigest       string                       `json:"server_image_digest"`
	TEIImageDigest          string                       `json:"tei_image_digest"`
	RerankerImageDigest     string                       `json:"reranker_image_digest,omitempty"`
}

func buildProvenance(repo *searchindex.Repository, lexiconDigest, runtimeVectorMode string, retrievalCandidateLimit int, reranker searchindex.Reranker, rerankerCandidates int, rerankerAll bool, rerankerTimeout time.Duration, fixture evaluation.Fixture, fixtureHash string) (evaluation.Provenance, string, error) {
	if repo == nil {
		return evaluation.Provenance{}, "", fmt.Errorf("repository is nil")
	}
	var vectorDescriptor *releaseVectorDescriptor
	var embedding *evaluation.EmbeddingIdentity
	var rerankerIdentity *evaluation.RerankerIdentity
	for _, status := range repo.VectorIndexes {
		if status.RejectedReason != "" || status.LoadedVectors == 0 || status.Path != repo.VectorPath {
			continue
		}
		metadata := status.Metadata
		vectorDescriptor = &releaseVectorDescriptor{
			ArtifactDigest: status.ArtifactDigest, MetadataDigest: status.MetadataDigest,
			GenerationID: metadata.GenerationID, IndexSourceHash: metadata.IndexSourceHash,
			IndexBuildHash: metadata.IndexBuildHash, Model: metadata.Model, ModelRevision: metadata.ModelRevision,
			Dimensions: metadata.Dimensions, QueryPrefix: metadata.QueryPrefix, DocumentPrefix: metadata.DocumentPrefix,
			InputFormat: string(metadata.InputFormat), Scope: string(metadata.Scope), ExpectedChunkCount: metadata.ExpectedChunkCount, StoredVectorCount: metadata.StoredVectorCount,
		}
		embedding = &evaluation.EmbeddingIdentity{
			Model: metadata.Model, Revision: metadata.ModelRevision, Dimensions: metadata.Dimensions,
			QueryPrefix: metadata.QueryPrefix, DocumentPrefix: metadata.DocumentPrefix, Scope: string(metadata.Scope),
			InputFormat: string(metadata.InputFormat), ExpectedChunkCount: metadata.ExpectedChunkCount, StoredVectorCount: metadata.StoredVectorCount,
			Coverage: repo.VectorCoverage, ArtifactDigest: status.ArtifactDigest, MetadataDigest: status.MetadataDigest,
		}
		break
	}
	if reranker != nil {
		model, revision := "unknown", ""
		batchSize := 0
		if info, ok := reranker.(searchindex.RerankerInfo); ok {
			model, revision = info.RerankingInfo()
		}
		if configured, ok := reranker.(*searchindex.TEIReranker); ok {
			batchSize = configured.BatchSize
		}
		rerankerIdentity = &evaluation.RerankerIdentity{
			Model: model, Revision: revision, CandidateLimit: rerankerCandidates, BatchSize: batchSize, Mode: rerankerMode(rerankerAll), Timeout: rerankerTimeout.String(), InputFormat: "structured-korean-v1",
		}
	}
	descriptor := releaseDescriptor{
		Schema: "krx-rule-mcp-release-v4", CorpusReleaseHash: repo.CorpusReleaseHash,
		IndexSourceHash: repo.IndexSourceHash, IndexBuildHash: repo.IndexBuildHash,
		BM25ArtifactDigest: repo.BM25ArtifactDigest, BM25SnapshotVersion: repo.BM25SnapshotVersion,
		IndexerVersion: repo.IndexerVersion, Vector: vectorDescriptor, Reranker: rerankerIdentity, DomainLexiconDigest: lexiconDigest,
		RuntimeVectorMode: runtimeVectorMode, ServerImageDigest: strings.TrimSpace(os.Getenv("RULE_MCP_SERVER_IMAGE_DIGEST")),
		RetrievalCandidateLimit: retrievalCandidateLimit,
		TEIImageDigest:          strings.TrimSpace(os.Getenv("RULE_MCP_TEI_IMAGE_DIGEST")),
		RerankerImageDigest:     strings.TrimSpace(os.Getenv("RULE_MCP_RERANKER_IMAGE_DIGEST")),
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return evaluation.Provenance{}, "", fmt.Errorf("encode release descriptor: %w", err)
	}
	sum := sha256.Sum256(encoded)
	releaseGeneration := hex.EncodeToString(sum[:])
	return evaluation.Provenance{
		ServerCommit: vcsRevision(), ReleaseGeneration: releaseGeneration, CorpusReleaseHash: repo.CorpusReleaseHash,
		IndexGeneration: repo.GenerationID, IndexSourceHash: repo.IndexSourceHash, IndexBuildHash: repo.IndexBuildHash,
		IndexerVersion: repo.IndexerVersion, BM25ArtifactDigest: repo.BM25ArtifactDigest,
		BM25SnapshotVersion: repo.BM25SnapshotVersion, Embedding: embedding, Reranker: rerankerIdentity, LexiconDigest: lexiconDigest,
		RuntimeVectorMode: runtimeVectorMode, RetrievalContract: retrievalContract(reranker != nil), AnswerabilityGate: searchindex.AnswerabilityGateVersion,
		RetrievalCandidateLimit: retrievalCandidateLimit,
		FixtureVersion:          fixture.FixtureVersion, FixtureSHA256: fixtureHash, SourceFixtureSHA256: fixture.Source.SHA256,
	}, releaseGeneration, nil
}

func rerankerMode(all bool) string {
	if all {
		return "all-korean"
	}
	return "weak-supported-korean"
}

func retrievalContract(rerankerEnabled bool) string {
	if rerankerEnabled {
		return "chunk-rrf-selective-rerank-v1"
	}
	return "chunk-rrf-v1"
}

func writeReport(path string, report evaluation.Report) error {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode evaluation report: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".rag-eval-*.tmp")
	if err != nil {
		return fmt.Errorf("create report temp file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o644); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("publish evaluation report: %w", err)
	}
	return nil
}

func qualityGateFailures(report evaluation.Report, englishFloor *englishQualityFloor) []string {
	var failures []string
	if report.Summary.Cases < 120 {
		failures = append(failures, "evaluation cases < 120")
	}
	failures = append(failures, embeddingIntegrityFailures(report.Provenance.Embedding)...)

	korean, ok := report.LanguageSummaries["ko"]
	if !ok || korean.Cases < 150 || korean.DocumentEligible < 100 || korean.EvidenceEligible < 90 || korean.InsufficientCases < 20 || korean.AmbiguousCases < 10 {
		failures = append(failures, "Korean evaluation coverage is incomplete")
	} else {
		failures = append(failures, koreanQualityFailures("Korean", korean, report.Provenance.Reranker != nil)...)
	}
	for _, name := range []string{"semantic", "semantic-variant"} {
		cases, eligible, hitAt1 := koreanGroupEvidenceHitAt1(report.Cases, name)
		if cases == 0 || eligible == 0 {
			failures = append(failures, name+" evidence cases missing")
		} else if float64(hitAt1)/float64(eligible) < 0.90 {
			failures = append(failures, name+" evidence Hit@1 < 90%")
		}
	}
	if korean.ManualEvidenceEligible > 0 && korean.ManualEvidenceHitAt1Rate < 0.90 {
		failures = append(failures, "Korean manual evidence target Hit@1 < 90%")
	}
	contradictionCases := 0
	contradictionHitAt1 := 0
	for _, result := range report.Cases {
		if result.Language != "ko" || result.ExpectedStatus != "supported" || result.ClaimRelation != "contradicts" {
			continue
		}
		contradictionCases++
		if result.EvidenceRank == 1 {
			contradictionHitAt1++
		}
	}
	if contradictionCases == 0 {
		failures = append(failures, "Korean contradiction evidence cases missing")
	} else if float64(contradictionHitAt1)/float64(contradictionCases) < 0.90 {
		failures = append(failures, "Korean contradiction target-evidence Hit@1 < 90%")
	}
	if report.Summary.FilterLeaks != 0 {
		failures = append(failures, "filter leaks are non-zero")
	}
	if report.Summary.ContextConsistencyRate < 1 {
		failures = append(failures, "context consistency < 100%")
	}
	if report.Summary.HWPAttachmentChecks == 0 {
		failures = append(failures, "HWP attachment cases missing")
	} else if report.Summary.HWPAttachmentPassed != report.Summary.HWPAttachmentChecks {
		failures = append(failures, "HWP attachment regression")
	}
	koreanHoldout, ok := report.LanguageSplitSummaries["ko"]["holdout"]
	if !ok || koreanHoldout.Cases < 30 || koreanHoldout.DocumentEligible < 15 || koreanHoldout.EvidenceEligible < 15 || koreanHoldout.InsufficientCases < 5 || koreanHoldout.AmbiguousCases < 3 {
		failures = append(failures, "Korean holdout coverage is incomplete")
	} else {
		failures = append(failures, koreanQualityFailures("Korean holdout", koreanHoldout, report.Provenance.Reranker != nil)...)
	}
	failures = append(failures, englishQualityFloorFailures(report, englishFloor)...)
	return failures
}

func embeddingIntegrityFailures(embedding *evaluation.EmbeddingIdentity) []string {
	if embedding == nil {
		return []string{"full-vector embedding provenance is missing"}
	}
	var failures []string
	if strings.TrimSpace(embedding.Model) == "" {
		failures = append(failures, "embedding model identity is missing")
	}
	if embedding.Dimensions <= 0 {
		failures = append(failures, "embedding dimensions must be positive")
	}
	if strings.TrimSpace(embedding.InputFormat) == "" {
		failures = append(failures, "embedding input format is missing")
	} else if _, err := searchindex.ParseEmbeddingInputFormat(embedding.InputFormat); err != nil {
		failures = append(failures, "embedding input format is unsupported")
	}
	if embedding.Scope != string(searchindex.VectorScopeFull) {
		failures = append(failures, "embedding vector scope is not full")
	}
	if embedding.ExpectedChunkCount <= 0 || embedding.StoredVectorCount != embedding.ExpectedChunkCount || embedding.Coverage != 1 {
		failures = append(failures, "embedding vector coverage is incomplete")
	}
	if !isSHA256Hex(embedding.ArtifactDigest) || !isSHA256Hex(embedding.MetadataDigest) {
		failures = append(failures, "embedding artifact provenance is incomplete")
	}
	return failures
}

func isSHA256Hex(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func koreanGroupEvidenceHitAt1(cases []evaluation.CaseResult, group string) (count, eligible, hitAt1 int) {
	for _, result := range cases {
		if result.Language != "ko" || result.Group != group {
			continue
		}
		count++
		if !result.EvidenceEligible || result.EvidenceManualReview {
			continue
		}
		eligible++
		if result.EvidenceRank == 1 {
			hitAt1++
		}
	}
	return count, eligible, hitAt1
}

func koreanQualityFailures(label string, summary evaluation.Summary, rerankerEnabled bool) []string {
	var failures []string
	if summary.DocumentHitAt5Rate < 0.95 {
		failures = append(failures, label+" Document Hit@5 < 95%")
	}
	if summary.MRRAt5 < 0.90 {
		failures = append(failures, label+" MRR@5 < 0.90")
	}
	if summary.EvidenceHitAt1Rate < 0.90 {
		failures = append(failures, label+" evidence Hit@1 < 90%")
	}
	if summary.EvidenceRecallAt3Rate < 0.95 {
		failures = append(failures, label+" evidence Recall@3 < 95%")
	}
	if summary.CandidateEvidenceRecall64Rate < 0.95 {
		failures = append(failures, label+" candidate evidence Recall@64 < 95%")
	}
	if rerankerEnabled && summary.RerankerPoolHitRate < 0.95 {
		failures = append(failures, label+" reranker pool target inclusion < 95%")
	}
	if summary.StatusAccuracy < 0.95 || summary.InsufficientRefusalRate < 0.95 || summary.FalseSupportedRate > 0.05 || summary.AmbiguousClarificationRate < 0.90 {
		failures = append(failures, label+" answerability gate regression")
	}
	if summary.FilterLeaks != 0 || summary.ContextConsistencyRate < 1 {
		failures = append(failures, label+" evidence contract regression")
	}
	return failures
}

func englishQualityFloorFailures(report evaluation.Report, floor *englishQualityFloor) []string {
	if floor == nil {
		return []string{"English quality floor is missing"}
	}
	var failures []string
	if report.Provenance.FixtureSHA256 != floor.FixtureSHA256 {
		return []string{fmt.Sprintf("English quality floor fixture mismatch: report=%s floor=%s", report.Provenance.FixtureSHA256, floor.FixtureSHA256)}
	}
	overall, ok := report.LanguageSummaries["en"]
	if !ok {
		failures = append(failures, "English summary is missing")
	} else {
		failures = append(failures, compareEnglishQualityFloor("English", englishQualityMetricsFromSummary(overall), floor.Overall)...)
	}
	holdout, ok := report.LanguageSplitSummaries["en"]["holdout"]
	if !ok {
		failures = append(failures, "English holdout summary is missing")
	} else {
		failures = append(failures, compareEnglishQualityFloor("English holdout", englishQualityMetricsFromSummary(holdout), floor.Holdout)...)
	}
	return failures
}

func compareEnglishQualityFloor(label string, current, floor englishQualityMetrics) []string {
	var failures []string
	if current.Cases != floor.Cases || current.DocumentEligible != floor.DocumentEligible ||
		current.EvidenceEligible != floor.EvidenceEligible || current.InsufficientCases != floor.InsufficientCases ||
		current.EnglishCanonicalChecks != floor.EnglishCanonicalChecks {
		failures = append(failures, label+" quality-floor coverage mismatch")
		return failures
	}
	checks := []struct {
		name       string
		current    int
		baseline   int
		lowerIsBad bool
	}{
		{name: "Document Hit@5", current: current.DocumentHitAt5, baseline: floor.DocumentHitAt5, lowerIsBad: true},
		{name: "evidence Hit@1", current: current.EvidenceHitAt1, baseline: floor.EvidenceHitAt1, lowerIsBad: true},
		{name: "evidence Recall@3", current: current.EvidenceRecallAt3, baseline: floor.EvidenceRecallAt3, lowerIsBad: true},
		{name: "candidate Recall@64", current: current.CandidateRecallAt64, baseline: floor.CandidateRecallAt64, lowerIsBad: true},
		{name: "status accuracy", current: current.StatusCorrect, baseline: floor.StatusCorrect, lowerIsBad: true},
		{name: "insufficient refusal", current: current.InsufficientRefused, baseline: floor.InsufficientRefused, lowerIsBad: true},
		{name: "false-supported", current: current.FalseSupported, baseline: floor.FalseSupported},
		{name: "canonical Korean source", current: current.EnglishCanonicalPassed, baseline: floor.EnglishCanonicalPassed, lowerIsBad: true},
	}
	for _, check := range checks {
		regressed := check.current > check.baseline
		if check.lowerIsBad {
			regressed = check.current < check.baseline
		}
		if regressed {
			failures = append(failures, fmt.Sprintf("%s %s fell below quality floor: current=%d floor=%d", label, check.name, check.current, check.baseline))
		}
	}
	if current.MRRAt5+1e-12 < floor.MRRAt5 {
		failures = append(failures, fmt.Sprintf("%s MRR@5 fell below quality floor: current=%.6f floor=%.6f", label, current.MRRAt5, floor.MRRAt5))
	}
	return failures
}

func vcsRevision() string {
	if revision := strings.TrimSpace(os.Getenv("KRX_RULE_SERVER_COMMIT")); revision != "" {
		return revision
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && strings.TrimSpace(setting.Value) != "" {
			return setting.Value
		}
	}
	return "unknown"
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func fatalIf(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
