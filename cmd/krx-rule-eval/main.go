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
	split := flag.String("split", "", "optional fixture split for diagnostic runs")
	vectorEnabled := flag.Bool("vector", envBool("KRX_VECTOR_SEARCH_ENABLED"), "load full vector generation and query embedder")
	requireVector := flag.Bool("require-vector", false, "fail unless full vector generation and query embedder are available")
	failOnGate := flag.Bool("fail-on-gate", false, "exit non-zero when release quality gates fail")
	flag.Parse()
	fatalIf(validateEvalOptions(*split, *failOnGate))

	fixture, fixtureHash, err := evaluation.LoadFixture(*fixturePath)
	fatalIf(err)
	fatalIf(evaluation.VerifySourceFixture(*sourceFixturePath, fixture.Source.SHA256, fixture.Source.OriginalCases))

	loadOptions := searchindex.RepositoryLoadOptions{
		VectorEnabled:         *vectorEnabled || *requireVector,
		RequireVector:         *requireVector,
		RequireCorpusManifest: true,
	}
	repo, err := searchindex.LoadRepositoryGeneration(*dataDir, *indexDir, loadOptions)
	fatalIf(err)
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
	runtimeVectorMode := "bm25"
	if embedder != nil {
		runtimeVectorMode = "bm25+vector"
	}
	provenance, releaseGeneration, err := buildProvenance(repo, lexiconDigest, runtimeVectorMode, fixture, fixtureHash)
	fatalIf(err)
	service := &mcpserver.Service{
		Repo:              repo,
		Embedder:          embedder,
		VectorRequired:    *requireVector,
		DomainLexicon:     lexicon,
		ReleaseGeneration: releaseGeneration,
	}
	var report evaluation.Report
	if *split == "" {
		report, err = evaluation.Run(context.Background(), fixture, service, provenance)
	} else {
		report, err = evaluation.RunSplit(context.Background(), fixture, *split, service, provenance)
	}
	fatalIf(err)
	fatalIf(writeReport(*outputPath, report))

	fmt.Printf("rag-eval cases=%d document_hit@5=%.3f mrr@5=%.3f evidence_hit@1=%.3f evidence_recall@3=%.3f refusal=%.3f ambiguous=%.3f context=%.3f filter_leaks=%d p95_ms=%.2f report=%s\n",
		report.Summary.Cases,
		report.Summary.DocumentHitAt5Rate,
		report.Summary.MRRAt5,
		report.Summary.EvidenceHitAt1Rate,
		report.Summary.EvidenceRecallAt3Rate,
		report.Summary.InsufficientRefusalRate,
		report.Summary.AmbiguousClarificationRate,
		report.Summary.ContextConsistencyRate,
		report.Summary.FilterLeaks,
		report.Summary.P95LatencyMillis,
		*outputPath,
	)
	if *failOnGate {
		failures := qualityGateFailures(report)
		if len(failures) > 0 {
			for _, failure := range failures {
				fmt.Fprintln(os.Stderr, "gate:", failure)
			}
			os.Exit(1)
		}
	}
}

func validateEvalOptions(split string, failOnGate bool) error {
	if strings.TrimSpace(split) != "" && failOnGate {
		return fmt.Errorf("--fail-on-gate cannot be combined with --split; split runs are diagnostic")
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
	Schema              string                   `json:"schema"`
	CorpusReleaseHash   string                   `json:"corpus_release_hash"`
	IndexSourceHash     string                   `json:"index_source_hash"`
	IndexBuildHash      string                   `json:"index_build_hash"`
	BM25ArtifactDigest  string                   `json:"bm25_artifact_digest"`
	BM25SnapshotVersion uint16                   `json:"bm25_snapshot_version"`
	IndexerVersion      string                   `json:"indexer_version"`
	Vector              *releaseVectorDescriptor `json:"vector,omitempty"`
	DomainLexiconDigest string                   `json:"domain_lexicon_digest"`
	RuntimeVectorMode   string                   `json:"runtime_vector_mode"`
	ServerImageDigest   string                   `json:"server_image_digest"`
	TEIImageDigest      string                   `json:"tei_image_digest"`
}

func buildProvenance(repo *searchindex.Repository, lexiconDigest, runtimeVectorMode string, fixture evaluation.Fixture, fixtureHash string) (evaluation.Provenance, string, error) {
	if repo == nil {
		return evaluation.Provenance{}, "", fmt.Errorf("repository is nil")
	}
	var vectorDescriptor *releaseVectorDescriptor
	var embedding *evaluation.EmbeddingIdentity
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
	descriptor := releaseDescriptor{
		Schema: "krx-rule-mcp-release-v4", CorpusReleaseHash: repo.CorpusReleaseHash,
		IndexSourceHash: repo.IndexSourceHash, IndexBuildHash: repo.IndexBuildHash,
		BM25ArtifactDigest: repo.BM25ArtifactDigest, BM25SnapshotVersion: repo.BM25SnapshotVersion,
		IndexerVersion: repo.IndexerVersion, Vector: vectorDescriptor, DomainLexiconDigest: lexiconDigest,
		RuntimeVectorMode: runtimeVectorMode, ServerImageDigest: strings.TrimSpace(os.Getenv("RULE_MCP_SERVER_IMAGE_DIGEST")),
		TEIImageDigest: strings.TrimSpace(os.Getenv("RULE_MCP_TEI_IMAGE_DIGEST")),
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
		BM25SnapshotVersion: repo.BM25SnapshotVersion, Embedding: embedding, LexiconDigest: lexiconDigest,
		RuntimeVectorMode: runtimeVectorMode, RetrievalContract: "chunk-rrf-v1", AnswerabilityGate: searchindex.AnswerabilityGateVersion,
		FixtureVersion: fixture.FixtureVersion, FixtureSHA256: fixtureHash, SourceFixtureSHA256: fixture.Source.SHA256,
	}, releaseGeneration, nil
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

func qualityGateFailures(report evaluation.Report) []string {
	var failures []string
	if report.Summary.Cases < 120 {
		failures = append(failures, "evaluation cases < 120")
	}
	if report.Summary.DocumentHitAt5Rate < 0.95 {
		failures = append(failures, "Document Hit@5 < 95%")
	}
	if report.Summary.MRRAt5 < 0.90 {
		failures = append(failures, "MRR@5 < 0.90")
	}
	if report.Summary.EvidenceHitAt1Rate < 0.90 {
		failures = append(failures, "evidence Hit@1 < 90%")
	}
	if report.Summary.EvidenceRecallAt3Rate < 0.95 {
		failures = append(failures, "evidence Recall@3 < 95%")
	}
	for _, name := range []string{"semantic", "semantic-variant"} {
		slice := report.Slices[name]
		if slice.EvidenceEligible > 0 && slice.EvidenceHitAt1Rate < 0.90 {
			failures = append(failures, name+" evidence Hit@1 < 90%")
		}
	}
	if report.Summary.ManualEvidenceEligible > 0 && report.Summary.ManualEvidenceHitAt1Rate < 0.90 {
		failures = append(failures, "manual evidence target Hit@1 < 90%")
	}
	falsePremiseCases := 0
	falsePremiseHitAt1 := 0
	for _, result := range report.Cases {
		if !strings.HasPrefix(result.Group, "false-premise") {
			continue
		}
		falsePremiseCases++
		if result.EvidenceRank == 1 {
			falsePremiseHitAt1++
		}
	}
	if falsePremiseCases > 0 && float64(falsePremiseHitAt1)/float64(falsePremiseCases) < 0.90 {
		failures = append(failures, "false-premise target-evidence Hit@1 < 90%")
	}
	if report.Summary.InsufficientRefusalRate < 0.95 {
		failures = append(failures, "insufficient refusal < 95%")
	}
	if report.Summary.FalseSupportedRate > 0.05 {
		failures = append(failures, "false-supported > 5%")
	}
	if report.Summary.AmbiguousClarificationRate < 0.90 {
		failures = append(failures, "ambiguous clarification < 90%")
	}
	if report.Summary.FilterLeaks != 0 {
		failures = append(failures, "filter leaks are non-zero")
	}
	if report.Summary.ContextConsistencyRate < 1 {
		failures = append(failures, "context consistency < 100%")
	}
	if report.Summary.P95LatencyMillis >= 300 {
		failures = append(failures, "p95 latency >= 300ms")
	}
	if report.Summary.HWPAttachmentPassed != report.Summary.HWPAttachmentChecks {
		failures = append(failures, "HWP attachment regression")
	}
	if report.Summary.EnglishCanonicalPassed != report.Summary.EnglishCanonicalChecks {
		failures = append(failures, "English canonical source regression")
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
