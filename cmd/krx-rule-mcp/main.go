package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	mcpserver "github.com/chromato99/krx-rule-mcp/internal/mcp"
	"github.com/chromato99/krx-rule-mcp/internal/security"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

type artifactRuntime struct {
	ReleaseGeneration    string
	CorpusDigest         string
	IndexDigest          string
	VectorDigest         string
	VectorMetadataDigest string
	DomainLexiconDigest  string
	RuntimeVectorMode    string
	ServerImageDigest    string
}

type releaseDescriptor struct {
	Schema              string                   `json:"schema"`
	CorpusReleaseHash   string                   `json:"corpus_release_hash"`
	IndexSourceHash     string                   `json:"index_source_hash"`
	IndexBuildHash      string                   `json:"index_build_hash"`
	BM25ArtifactDigest  string                   `json:"bm25_artifact_digest"`
	BM25SnapshotVersion uint16                   `json:"bm25_snapshot_version"`
	IndexerVersion      string                   `json:"indexer_version"`
	Vector              *vectorReleaseDescriptor `json:"vector,omitempty"`
	DomainLexiconDigest string                   `json:"domain_lexicon_digest"`
	RuntimeVectorMode   string                   `json:"runtime_vector_mode"`
	ServerImageDigest   string                   `json:"server_image_digest"`
}

type vectorReleaseDescriptor struct {
	ArtifactDigest     string `json:"artifact_digest"`
	MetadataDigest     string `json:"metadata_digest"`
	GenerationID       string `json:"generation_id,omitempty"`
	IndexSourceHash    string `json:"index_source_hash"`
	IndexBuildHash     string `json:"index_build_hash"`
	Model              string `json:"model"`
	ModelRevision      string `json:"model_revision,omitempty"`
	Dimensions         int    `json:"dimensions"`
	QueryPrefix        string `json:"query_prefix"`
	DocumentPrefix     string `json:"document_prefix"`
	Scope              string `json:"scope"`
	ExpectedChunkCount int    `json:"expected_chunk_count"`
	StoredVectorCount  int    `json:"stored_vector_count"`
}

type httpRuntimeConfig struct {
	Addr                  string
	Token                 string
	Origins               []string
	RequestSizeLimit      int64
	ResponseSizeLimit     int64
	MaxConcurrentRequests int
	RequestTimeout        time.Duration
	ShutdownTimeout       time.Duration
	ExpectedGeneration    string
	Artifacts             artifactRuntime
	Metrics               *security.Metrics
}

func main() {
	var (
		mode               = flag.String("mode", env("KRX_MCP_MODE", "stdio"), "transport mode: stdio or http")
		addr               = flag.String("addr", env("KRX_MCP_ADDR", ":8080"), "HTTP listen address")
		dataDir            = flag.String("data-dir", envDataDir(), "data directory")
		indexDir           = flag.String("index-dir", envIndexDir(), "search index snapshot directory")
		indexPath          = flag.String("index", "", "BM25/core index snapshot path")
		vectorIndex        = flag.String("vector-index", "", "optional vector snapshot path")
		vectorPolicy       = flag.String("vector-policy", env("KRX_VECTOR_SEARCH_POLICY", "optional"), "vector runtime policy: optional or required when vector search is enabled")
		requireVector      = flag.Bool("require-vector", envBool("KRX_REQUIRE_VECTOR"), "require a valid full-coverage vector snapshot and embedding configuration")
		lexiconPath        = flag.String("domain-lexicon", env("KRX_DOMAIN_LEXICON_PATH", searchindex.DefaultDomainLexiconPath), "domain lexicon YAML path for query expansion")
		token              = flag.String("token", os.Getenv("KRX_MCP_BEARER_TOKEN"), "required bearer token for HTTP mode")
		origins            = flag.String("allowed-origins", os.Getenv("KRX_MCP_ALLOWED_ORIGINS"), "comma-separated Origin allowlist for HTTP mode")
		requestLimit       = flag.Int64("request-size-limit", envInt64("KRX_MCP_REQUEST_SIZE_LIMIT", 1<<20), "maximum HTTP request body size in bytes")
		responseLimit      = flag.Int64("response-size-limit", envInt64("KRX_MCP_RESPONSE_SIZE_LIMIT", 1<<20), "maximum complete HTTP MCP response body size in bytes")
		toolOutputLimit    = flag.Int("tool-output-size-limit", envInt("KRX_MCP_TOOL_OUTPUT_SIZE_LIMIT", 512<<10), "maximum structured tool output size in bytes")
		maxQueryRunes      = flag.Int("max-query-runes", envInt("KRX_MCP_MAX_QUERY_RUNES", 1000), "maximum search query length in characters")
		maxSearches        = flag.Int("max-concurrent-searches", envInt("KRX_MCP_MAX_CONCURRENT_SEARCHES", 16), "maximum concurrent search and query embedding operations")
		maxRequests        = flag.Int("max-concurrent-requests", envInt("KRX_MCP_MAX_CONCURRENT_REQUESTS", 64), "maximum concurrent HTTP MCP requests")
		embedTimeout       = flag.Duration("embedding-timeout", envDuration("KRX_MCP_EMBEDDING_TIMEOUT", 3*time.Second), "query embedding deadline")
		requestTimeout     = flag.Duration("request-timeout", envDuration("KRX_MCP_REQUEST_TIMEOUT", 30*time.Second), "overall HTTP MCP request deadline")
		shutdownTimeout    = flag.Duration("shutdown-timeout", envDuration("KRX_MCP_SHUTDOWN_TIMEOUT", 15*time.Second), "HTTP graceful shutdown deadline")
		expectedGeneration = flag.String("expected-release-generation", os.Getenv("KRX_EXPECTED_RELEASE_GENERATION"), "expected lowercase SHA-256 digest of the canonical release descriptor")
		printGeneration    = flag.Bool("print-release-generation", false, "print the canonical release descriptor and generation, then exit")
	)
	flag.Parse()
	requestedMode := strings.ToLower(strings.TrimSpace(*mode))
	if requestedMode == "http" && !*printGeneration {
		if err := security.ValidateBearerToken(*token); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "invalid HTTP bearer token: %v\n", err)
			os.Exit(1)
		}
		*token = strings.TrimSpace(*token)
		if *maxQueryRunes <= 0 || *maxSearches <= 0 || *maxRequests <= 0 || *requestLimit <= 0 || *toolOutputLimit <= 0 || *embedTimeout <= 0 || *requestTimeout <= 0 || *shutdownTimeout <= 0 {
			_, _ = fmt.Fprintln(os.Stderr, "HTTP limits and timeouts must be greater than zero")
			os.Exit(1)
		}
		if *responseLimit < 1024 {
			_, _ = fmt.Fprintln(os.Stderr, "HTTP response size limit must be at least 1024 bytes")
			os.Exit(1)
		}
		if err := validateExpectedGeneration(*expectedGeneration); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "invalid expected release generation: %v\n", err)
			os.Exit(1)
		}
	}
	legacyIndexPath := strings.TrimSpace(*indexPath)
	if legacyIndexPath == "" {
		legacyIndexPath = strings.TrimSpace(os.Getenv("KRX_INDEX_PATH"))
	}
	legacyVectorPath := strings.TrimSpace(*vectorIndex)
	if legacyVectorPath == "" {
		legacyVectorPath = strings.TrimSpace(os.Getenv("KRX_VECTOR_INDEX_PATH"))
	}
	usePublishedGeneration := legacyIndexPath == "" && legacyVectorPath == ""
	if !usePublishedGeneration {
		if legacyIndexPath == "" {
			legacyIndexPath = searchindex.DefaultBM25Path(*indexDir)
		}
		if legacyVectorPath == "" {
			legacyVectorPath = searchindex.DefaultVectorPath(*indexDir)
		}
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	embedder, vectorEnabled, embedErr := searchindex.NewEmbedderFromEnv()
	vectorRequired, err := resolveVectorPolicy(vectorEnabled, *vectorPolicy, *requireVector)
	if err != nil {
		logger.Error("invalid vector search policy", "error", err)
		os.Exit(1)
	}
	if vectorRequired && embedErr != nil {
		logger.Error("required vector embedding configuration failed", "error", embedErr)
		os.Exit(1)
	}
	loadOptions := searchindex.RepositoryLoadOptions{
		VectorEnabled:         vectorEnabled,
		RequireVector:         vectorRequired,
		RequireCorpusManifest: true,
	}
	var repo *searchindex.Repository
	if usePublishedGeneration {
		repo, err = searchindex.LoadRepositoryGeneration(*dataDir, *indexDir, loadOptions)
	} else {
		loadOptions.VectorIndexPaths = []string{legacyVectorPath}
		repo, err = searchindex.LoadRepositoryWithOptions(*dataDir, legacyIndexPath, loadOptions)
	}
	if err != nil {
		logger.Error("load repository failed", "error", err)
		os.Exit(1)
	}
	logger.Info("repository loaded", "documents", len(repo.Documents), "attachments", len(repo.Attachments), "index_generation", repo.GenerationID)

	lexicon, lexiconDigest, err := searchindex.LoadDomainLexiconWithDigest(*lexiconPath)
	if err != nil {
		logger.Error("load domain lexicon failed", "path", *lexiconPath, "error", err)
		os.Exit(1)
	}
	logger.Info("domain lexicon loaded", "path", *lexiconPath, "entries", len(lexicon))

	var activeEmbedder searchindex.Embedder
	if vectorEnabled && embedErr != nil {
		logger.Warn("vector search disabled; embedding configuration failed", "error", embedErr)
	} else if vectorEnabled && repo.Engine.HasVectors() {
		logger.Info("vector search enabled", "model", embedder.Model, "base_url", embedder.BaseURL)
		activeEmbedder = embedder
	} else if vectorEnabled {
		loggedVectorReason := false
		for _, status := range repo.VectorIndexes {
			if status.RejectedReason == "" {
				continue
			}
			logger.Warn("vector search disabled; vector snapshot rejected", "path", status.Path, "reason", status.RejectedReason)
			loggedVectorReason = true
		}
		if !loggedVectorReason {
			logger.Warn("vector search disabled; vector snapshot has no stored vectors")
		}
	} else {
		logger.Info("vector search disabled; BM25-only mode")
	}
	runtimeVectorMode := "bm25"
	if activeEmbedder != nil {
		runtimeVectorMode = "bm25+vector"
	}
	artifacts, descriptor, err := inspectArtifacts(repo, lexiconDigest, runtimeVectorMode, strings.TrimSpace(os.Getenv("KRX_SERVER_IMAGE_DIGEST")))
	if err != nil {
		logger.Error("inspect loaded artifacts failed", "error", err)
		os.Exit(1)
	}
	logger.Info("release artifacts loaded",
		"release_generation", artifacts.ReleaseGeneration,
		"corpus_digest", artifacts.CorpusDigest,
		"index_digest", artifacts.IndexDigest,
		"vector_digest", artifacts.VectorDigest,
		"vector_metadata_digest", artifacts.VectorMetadataDigest,
		"domain_lexicon_digest", artifacts.DomainLexiconDigest,
		"runtime_vector_mode", artifacts.RuntimeVectorMode,
		"server_image_digest", artifacts.ServerImageDigest,
	)
	if *printGeneration {
		output := struct {
			ReleaseGeneration string            `json:"release_generation"`
			Descriptor        releaseDescriptor `json:"descriptor"`
		}{ReleaseGeneration: artifacts.ReleaseGeneration, Descriptor: descriptor}
		if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
			logger.Error("write release descriptor failed", "error", err)
			os.Exit(1)
		}
		return
	}

	runtimeMetrics := security.NewMetrics()
	service := &mcpserver.Service{
		Repo:               repo,
		Embedder:           activeEmbedder,
		DomainLexicon:      lexicon,
		Logger:             logger,
		ReleaseGeneration:  artifacts.ReleaseGeneration,
		MaxQueryRunes:      *maxQueryRunes,
		EmbeddingTimeout:   *embedTimeout,
		ConcurrentSearches: searchSlots(*maxSearches),
		Observer:           runtimeMetrics,
		MaxToolOutputBytes: *toolOutputLimit,
	}
	server := mcpserver.NewServer(service, version)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch requestedMode {
	case "stdio":
		if err := server.Run(ctx, &mcpsdk.StdioTransport{}); err != nil && ctx.Err() == nil {
			logger.Error("stdio server failed", "error", err)
			os.Exit(1)
		}
	case "http":
		config := httpRuntimeConfig{
			Addr:                  *addr,
			Token:                 *token,
			Origins:               splitCSV(*origins),
			RequestSizeLimit:      *requestLimit,
			ResponseSizeLimit:     *responseLimit,
			MaxConcurrentRequests: *maxRequests,
			RequestTimeout:        *requestTimeout,
			ShutdownTimeout:       *shutdownTimeout,
			ExpectedGeneration:    strings.TrimSpace(*expectedGeneration),
			Artifacts:             artifacts,
			Metrics:               runtimeMetrics,
		}
		if err := runHTTP(ctx, config, server, repo, logger); err != nil {
			logger.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	default:
		logger.Error("unknown mode", "mode", *mode)
		os.Exit(1)
	}
}

func runHTTP(ctx context.Context, config httpRuntimeConfig, server *mcpsdk.Server, repo *searchindex.Repository, logger *slog.Logger) error {
	mcpHandler := withResponseSizeLimit(config.ResponseSizeLimit, statelessMCPHandler(server, logger))
	metrics := config.Metrics
	if metrics == nil {
		metrics = security.NewMetrics()
	}
	metrics.SetRuntimeInfo(security.RuntimeInfo{
		ReleaseGeneration:    config.Artifacts.ReleaseGeneration,
		CorpusDigest:         config.Artifacts.CorpusDigest,
		IndexDigest:          config.Artifacts.IndexDigest,
		VectorDigest:         config.Artifacts.VectorDigest,
		VectorMetadataDigest: config.Artifacts.VectorMetadataDigest,
		DomainLexiconDigest:  config.Artifacts.DomainLexiconDigest,
		RuntimeVectorMode:    config.Artifacts.RuntimeVectorMode,
		ServerImageDigest:    config.Artifacts.ServerImageDigest,
		VectorCoverage:       repo.VectorCoverage,
	})
	authenticated := security.WithBearerToken(config.Token,
		security.WithRateLimit(120, time.Minute, mcpHandler))
	protected := security.WithMetrics(metrics,
		security.WithRateLimit(600, time.Minute,
			security.WithConcurrencyLimit(config.MaxConcurrentRequests,
				security.WithRequestTimeout(config.RequestTimeout,
					security.WithRequestSizeLimit(config.RequestSizeLimit,
						security.WithOriginAllowlist(config.Origins,
							authenticated))))))

	mux := http.NewServeMux()
	mux.Handle("/mcp", protected)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	mux.Handle("/readyz", readinessHandler(config, repo))
	mux.Handle("/metrics", metrics)

	httpServer := &http.Server{
		Addr:              config.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      config.RequestTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.ListenAndServe() }()
	logger.Info("HTTP MCP server listening", "addr", config.Addr, "stateless", true)
	select {
	case err := <-serveErr:
		if err == nil || err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		serveResult := <-serveErr
		if shutdownErr != nil {
			return fmt.Errorf("graceful shutdown: %w", shutdownErr)
		}
		if serveResult != nil && serveResult != http.ErrServerClosed {
			return serveResult
		}
		return nil
	}
}

func statelessMCPHandler(server *mcpsdk.Server, logger *slog.Logger) http.Handler {
	return mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server {
		return server
	}, &mcpsdk.StreamableHTTPOptions{
		Logger:       logger,
		Stateless:    true,
		JSONResponse: true,
	})
}

// withResponseSizeLimit applies to synchronous MCP POST replies. The SDK's
// JSON response mode makes one complete JSON-RPC body available here, so the
// limit covers the envelope and typed structuredContent rather than only the
// pre-serialization Go value. GET remains unbuffered because it may be an SSE
// stream under the MCP transport contract.
func withResponseSizeLimit(limit int64, next http.Handler) http.Handler {
	if limit <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		buffered := newBoundedResponseWriter(limit)
		next.ServeHTTP(buffered, r)
		if buffered.overflow {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusInternalServerError)
			message := []byte("MCP response exceeded the configured response size limit\n")
			if int64(len(message)) > limit {
				message = message[:limit]
			}
			_, _ = w.Write(message)
			return
		}
		copyResponseHeaders(w.Header(), buffered.header)
		status := buffered.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(buffered.body.Bytes())
	})
}

type boundedResponseWriter struct {
	header   http.Header
	body     bytes.Buffer
	limit    int64
	status   int
	overflow bool
}

func newBoundedResponseWriter(limit int64) *boundedResponseWriter {
	return &boundedResponseWriter{header: make(http.Header), limit: limit}
}

func (w *boundedResponseWriter) Header() http.Header {
	return w.header
}

func (w *boundedResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *boundedResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.overflow {
		return len(p), nil
	}
	remaining := w.limit - int64(w.body.Len())
	if remaining <= 0 {
		if len(p) > 0 {
			w.overflow = true
		}
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		_, _ = w.body.Write(p[:int(remaining)])
		w.overflow = true
		return len(p), nil
	}
	_, _ = w.body.Write(p)
	return len(p), nil
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func readinessHandler(config httpRuntimeConfig, repo *searchindex.Repository) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if repo == nil || len(repo.Documents) == 0 {
			http.Error(w, "repository not ready", http.StatusServiceUnavailable)
			return
		}
		if config.ExpectedGeneration != "" && config.ExpectedGeneration != config.Artifacts.ReleaseGeneration {
			http.Error(w, "loaded release generation does not match expected generation", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprintf(w, "ready release_generation=%s\n", config.Artifacts.ReleaseGeneration)
	})
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envDataDir() string {
	if value := os.Getenv("KRX_RULE_DATA_DIR"); value != "" {
		return value
	}
	return env("KRX_DATA_DIR", "data")
}

func envIndexDir() string {
	if value := strings.TrimSpace(os.Getenv("KRX_RULE_INDEX_DIR")); value != "" {
		return value
	}
	return env("KRX_INDEX_DIR", searchindex.DefaultIndexDir)
}

func splitCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func inspectArtifacts(repo *searchindex.Repository, domainLexiconDigest, runtimeVectorMode, serverImageDigest string) (artifactRuntime, releaseDescriptor, error) {
	if repo == nil {
		return artifactRuntime{}, releaseDescriptor{}, fmt.Errorf("loaded repository is nil")
	}
	var missingIdentity []string
	if strings.TrimSpace(repo.CorpusReleaseHash) == "" {
		missingIdentity = append(missingIdentity, "corpus_release_hash")
	}
	if strings.TrimSpace(repo.IndexSourceHash) == "" {
		missingIdentity = append(missingIdentity, "index_source_hash")
	}
	if strings.TrimSpace(repo.IndexBuildHash) == "" {
		missingIdentity = append(missingIdentity, "index_build_hash")
	}
	if strings.TrimSpace(repo.BM25ArtifactDigest) == "" {
		missingIdentity = append(missingIdentity, "bm25_artifact_digest")
	}
	if repo.BM25SnapshotVersion == 0 {
		missingIdentity = append(missingIdentity, "bm25_snapshot_version")
	}
	if strings.TrimSpace(repo.IndexerVersion) == "" {
		missingIdentity = append(missingIdentity, "indexer_version")
	}
	if len(missingIdentity) > 0 {
		return artifactRuntime{}, releaseDescriptor{}, fmt.Errorf("loaded repository is missing fixed artifact identity: %s", strings.Join(missingIdentity, ", "))
	}
	if _, err := requireSHA256Digest("domain lexicon", domainLexiconDigest); err != nil {
		return artifactRuntime{}, releaseDescriptor{}, err
	}
	var vectorDigest string
	var vectorMetadataDigest string
	var vectorDescriptor *vectorReleaseDescriptor
	if strings.TrimSpace(repo.VectorPath) != "" {
		var adopted *searchindex.VectorIndexStatus
		for index := range repo.VectorIndexes {
			status := &repo.VectorIndexes[index]
			if status.Path == repo.VectorPath && status.LoadedVectors > 0 && status.RejectedReason == "" {
				adopted = status
				break
			}
		}
		if adopted == nil || strings.TrimSpace(adopted.ArtifactDigest) == "" || strings.TrimSpace(adopted.MetadataDigest) == "" {
			return artifactRuntime{}, releaseDescriptor{}, fmt.Errorf("loaded repository is missing fixed identity for adopted vector artifact %q", repo.VectorPath)
		}
		vectorDigest = adopted.ArtifactDigest
		vectorMetadataDigest = adopted.MetadataDigest
		metadata := adopted.Metadata
		vectorDescriptor = &vectorReleaseDescriptor{
			ArtifactDigest:     vectorDigest,
			MetadataDigest:     vectorMetadataDigest,
			GenerationID:       metadata.GenerationID,
			IndexSourceHash:    metadata.IndexSourceHash,
			IndexBuildHash:     metadata.IndexBuildHash,
			Model:              metadata.Model,
			ModelRevision:      metadata.ModelRevision,
			Dimensions:         metadata.Dimensions,
			QueryPrefix:        metadata.QueryPrefix,
			DocumentPrefix:     metadata.DocumentPrefix,
			Scope:              string(metadata.Scope),
			ExpectedChunkCount: metadata.ExpectedChunkCount,
			StoredVectorCount:  metadata.StoredVectorCount,
		}
	}
	descriptor := releaseDescriptor{
		Schema:              "krx-rule-mcp-release-v2",
		CorpusReleaseHash:   repo.CorpusReleaseHash,
		IndexSourceHash:     repo.IndexSourceHash,
		IndexBuildHash:      repo.IndexBuildHash,
		BM25ArtifactDigest:  repo.BM25ArtifactDigest,
		BM25SnapshotVersion: repo.BM25SnapshotVersion,
		IndexerVersion:      repo.IndexerVersion,
		Vector:              vectorDescriptor,
		DomainLexiconDigest: domainLexiconDigest,
		RuntimeVectorMode:   runtimeVectorMode,
		ServerImageDigest:   serverImageDigest,
	}
	descriptorJSON, err := json.Marshal(descriptor)
	if err != nil {
		return artifactRuntime{}, releaseDescriptor{}, fmt.Errorf("encode canonical release descriptor: %w", err)
	}
	generationHash := sha256.Sum256(descriptorJSON)
	generation := hex.EncodeToString(generationHash[:])
	return artifactRuntime{
		ReleaseGeneration:    generation,
		CorpusDigest:         repo.CorpusReleaseHash,
		IndexDigest:          repo.BM25ArtifactDigest,
		VectorDigest:         vectorDigest,
		VectorMetadataDigest: vectorMetadataDigest,
		DomainLexiconDigest:  domainLexiconDigest,
		RuntimeVectorMode:    runtimeVectorMode,
		ServerImageDigest:    serverImageDigest,
	}, descriptor, nil
}

func validateExpectedGeneration(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len(value) != sha256.Size*2 {
		return fmt.Errorf("must be the 64-character SHA-256 digest of the canonical release descriptor")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || hex.EncodeToString(decoded) != value {
		return fmt.Errorf("must be a lowercase hexadecimal SHA-256 digest")
	}
	return nil
}

func requireSHA256Digest(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 {
		return "", fmt.Errorf("%s digest must be a 64-character SHA-256 digest", name)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || hex.EncodeToString(decoded) != value {
		return "", fmt.Errorf("%s digest must be lowercase hexadecimal SHA-256", name)
	}
	return value, nil
}

func searchSlots(limit int) chan struct{} {
	if limit <= 0 {
		return nil
	}
	return make(chan struct{}, limit)
}

func resolveVectorPolicy(enabled bool, policy string, requireFlag bool) (bool, error) {
	policy = strings.ToLower(strings.TrimSpace(policy))
	if policy == "" {
		policy = "optional"
	}
	if policy != "optional" && policy != "required" {
		return false, fmt.Errorf("unsupported KRX_VECTOR_SEARCH_POLICY %q; expected optional or required", policy)
	}
	if !enabled {
		if requireFlag {
			return false, fmt.Errorf("--require-vector needs KRX_VECTOR_SEARCH_ENABLED=true")
		}
		return false, nil
	}
	return requireFlag || policy == "required", nil
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}
