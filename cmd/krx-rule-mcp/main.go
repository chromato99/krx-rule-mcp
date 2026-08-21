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
	ReleaseGeneration       string
	CorpusDigest            string
	IndexDigest             string
	VectorDigest            string
	VectorMetadataDigest    string
	DomainLexiconDigest     string
	RuntimeVectorMode       string
	RetrievalCandidateLimit int
	RerankerModel           string
	RerankerRevision        string
	RerankerImageDigest     string
	ServerImageDigest       string
}

type releaseDescriptor struct {
	Schema                  string                     `json:"schema"`
	CorpusReleaseHash       string                     `json:"corpus_release_hash"`
	IndexSourceHash         string                     `json:"index_source_hash"`
	IndexBuildHash          string                     `json:"index_build_hash"`
	BM25ArtifactDigest      string                     `json:"bm25_artifact_digest"`
	BM25SnapshotVersion     uint16                     `json:"bm25_snapshot_version"`
	IndexerVersion          string                     `json:"indexer_version"`
	Vector                  *vectorReleaseDescriptor   `json:"vector,omitempty"`
	Reranker                *rerankerReleaseDescriptor `json:"reranker,omitempty"`
	DomainLexiconDigest     string                     `json:"domain_lexicon_digest"`
	RuntimeVectorMode       string                     `json:"runtime_vector_mode"`
	RetrievalCandidateLimit int                        `json:"retrieval_candidate_limit"`
	ServerImageDigest       string                     `json:"server_image_digest"`
	TEIImageDigest          string                     `json:"tei_image_digest"`
	RerankerImageDigest     string                     `json:"reranker_image_digest,omitempty"`
}

type rerankerReleaseDescriptor struct {
	Model          string `json:"model"`
	ModelRevision  string `json:"model_revision"`
	CandidateLimit int    `json:"candidate_limit"`
	BatchSize      int    `json:"batch_size"`
	Mode           string `json:"mode"`
	Timeout        string `json:"timeout"`
	InputFormat    string `json:"input_format"`
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
	InputFormat        string `json:"input_format"`
	Scope              string `json:"scope"`
	ExpectedChunkCount int    `json:"expected_chunk_count"`
	StoredVectorCount  int    `json:"stored_vector_count"`
}

type httpRuntimeConfig struct {
	Addr                   string
	AuthMode               security.AuthMode
	TokenRegistry          *security.BearerTokenRegistry
	Origins                []string
	RequestSizeLimit       int64
	ResponseSizeLimit      int64
	MaxConcurrentRequests  int
	RequestTimeout         time.Duration
	ShutdownTimeout        time.Duration
	ReadinessEmbedTimeout  time.Duration
	ReadinessRerankTimeout time.Duration
	ExpectedGeneration     string
	Artifacts              artifactRuntime
	Metrics                *security.Metrics
	Embedder               searchindex.Embedder
	VectorRequired         bool
	Reranker               searchindex.Reranker
	RerankerRequired       bool
}

func main() {
	var (
		mode                     = flag.String("mode", env("RULE_MCP_MODE", "stdio"), "transport mode: stdio or http")
		addr                     = flag.String("addr", env("RULE_MCP_ADDR", ":8080"), "HTTP listen address")
		dataDir                  = flag.String("data-dir", envDataDir(), "data directory")
		indexDir                 = flag.String("index-dir", envIndexDir(), "search index snapshot directory")
		vectorPolicy             = flag.String("vector-policy", env("KRX_VECTOR_SEARCH_POLICY", "optional"), "vector runtime policy: optional or required when vector search is enabled")
		requireVector            = flag.Bool("require-vector", envBool("KRX_REQUIRE_VECTOR"), "require a valid full-coverage vector snapshot and embedding configuration")
		rerankerPolicy           = flag.String("reranker-policy", env("KRX_RERANKER_POLICY", "optional"), "reranker runtime policy: optional or required when reranking is enabled")
		requireReranker          = flag.Bool("require-reranker", envBool("KRX_REQUIRE_RERANKER"), "require a valid Korean reranker configuration")
		lexiconPath              = flag.String("domain-lexicon", env("KRX_DOMAIN_LEXICON_PATH", searchindex.DefaultDomainLexiconPath), "domain lexicon YAML path for query expansion")
		authModeValue            = flag.String("auth-mode", env("RULE_MCP_AUTH_MODE", string(security.AuthModeRequired)), "HTTP bearer authentication mode: required or disabled")
		bearerTokenFile          = flag.String("bearer-token-file", os.Getenv("RULE_MCP_BEARER_TOKEN_FILE"), "YAML bearer token registry for required HTTP authentication")
		origins                  = flag.String("allowed-origins", os.Getenv("RULE_MCP_ALLOWED_ORIGINS"), "comma-separated Origin allowlist for HTTP mode")
		requestLimit             = flag.Int64("request-size-limit", envInt64("RULE_MCP_REQUEST_SIZE_LIMIT", 1<<20), "maximum HTTP request body size in bytes")
		responseLimit            = flag.Int64("response-size-limit", envInt64("RULE_MCP_RESPONSE_SIZE_LIMIT", 1<<20), "maximum complete HTTP MCP response body size in bytes")
		toolOutputLimit          = flag.Int("tool-output-size-limit", envInt("RULE_MCP_TOOL_OUTPUT_SIZE_LIMIT", 512<<10), "maximum structured tool output size in bytes")
		maxQueryRunes            = flag.Int("max-query-runes", envInt("RULE_MCP_MAX_QUERY_RUNES", 1000), "maximum search query length in characters")
		maxSearches              = flag.Int("max-concurrent-searches", envInt("RULE_MCP_MAX_CONCURRENT_SEARCHES", 16), "maximum concurrent search and query embedding operations")
		retrievalCandidates      = flag.Int("candidate-limit", envInt("KRX_RETRIEVAL_CANDIDATE_LIMIT", 0), "first-stage candidate limit per channel, max 512; 0 derives it from result limit")
		maxRequests              = flag.Int("max-concurrent-requests", envInt("RULE_MCP_MAX_CONCURRENT_REQUESTS", 64), "maximum concurrent HTTP MCP requests")
		embedTimeout             = flag.Duration("embedding-timeout", envDuration("RULE_MCP_EMBEDDING_TIMEOUT", 3*time.Second), "query embedding deadline")
		rerankerTimeout          = flag.Duration("reranker-timeout", envDuration("RULE_MCP_RERANKER_TIMEOUT", 30*time.Minute), "bounded candidate reranker deadline")
		readinessTimeout         = flag.Duration("readiness-embedding-timeout", envDuration("RULE_MCP_READINESS_EMBEDDING_TIMEOUT", 5*time.Second), "required-vector readiness canary embedding deadline")
		readinessRerankerTimeout = flag.Duration("readiness-reranker-timeout", envDuration("RULE_MCP_READINESS_RERANKER_TIMEOUT", 30*time.Second), "required-reranker readiness canary deadline")
		requestTimeout           = flag.Duration("request-timeout", envDuration("RULE_MCP_REQUEST_TIMEOUT", 30*time.Second), "overall HTTP MCP request deadline")
		shutdownTimeout          = flag.Duration("shutdown-timeout", envDuration("RULE_MCP_SHUTDOWN_TIMEOUT", 15*time.Second), "HTTP graceful shutdown deadline")
		expectedGeneration       = flag.String("expected-release-generation", os.Getenv("RULE_MCP_EXPECTED_RELEASE_GENERATION"), "expected lowercase SHA-256 digest of the canonical release descriptor")
		printGeneration          = flag.Bool("print-release-generation", false, "print the canonical release descriptor and generation, then exit")
	)
	flag.Parse()
	if *retrievalCandidates < 0 || *retrievalCandidates > 512 {
		_, _ = fmt.Fprintln(os.Stderr, "candidate limit must be between 1 and 512, or 0 for the default")
		os.Exit(1)
	}
	requestedMode := strings.ToLower(strings.TrimSpace(*mode))
	var httpAuth httpAuthConfig
	if requestedMode == "http" && !*printGeneration {
		var err error
		httpAuth, err = loadHTTPAuthConfig(*authModeValue, *bearerTokenFile)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "invalid HTTP authentication configuration: %v\n", err)
			os.Exit(1)
		}
		if *maxQueryRunes <= 0 || *maxSearches <= 0 || *maxRequests <= 0 || *requestLimit <= 0 || *toolOutputLimit <= 0 || *embedTimeout <= 0 || *rerankerTimeout <= 0 || *readinessTimeout <= 0 || *readinessRerankerTimeout <= 0 || *requestTimeout <= 0 || *shutdownTimeout <= 0 {
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
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if requestedMode == "http" && !*printGeneration {
		if httpAuth.Mode == security.AuthModeDisabled {
			logger.Warn("HTTP bearer authentication disabled; use only on a trusted private network")
		} else {
			logger.Info("HTTP bearer token registry loaded",
				"active_tokens", httpAuth.Registry.ActiveTokenCount(),
				"registry_digest", httpAuth.Registry.Digest(),
			)
		}
	}
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
	reranker, rerankerEnabled, rerankerErr := searchindex.NewRerankerFromEnv()
	rerankerRequired, err := resolveRerankerPolicy(rerankerEnabled, *rerankerPolicy, *requireReranker)
	if err != nil {
		logger.Error("invalid reranker policy", "error", err)
		os.Exit(1)
	}
	if rerankerRequired && rerankerErr != nil {
		logger.Error("required reranker configuration failed", "error", rerankerErr)
		os.Exit(1)
	}
	if requestedMode == "http" && !*printGeneration && rerankerRequired && *requestTimeout <= *rerankerTimeout {
		logger.Error("request timeout must exceed required reranker timeout", "request_timeout", *requestTimeout, "reranker_timeout", *rerankerTimeout)
		os.Exit(1)
	}
	rerankerCandidates := 0
	if rerankerEnabled && rerankerErr == nil {
		rerankerCandidates, rerankerErr = searchindex.RerankerCandidateLimitFromEnv()
		if rerankerRequired && rerankerErr != nil {
			logger.Error("required reranker candidate configuration failed", "error", rerankerErr)
			os.Exit(1)
		}
	}
	loadOptions := searchindex.RepositoryLoadOptions{
		VectorEnabled: vectorEnabled,
		RequireVector: vectorRequired,
	}
	repo, err := searchindex.LoadRepositoryGeneration(*dataDir, *indexDir, loadOptions)
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
	var activeReranker searchindex.Reranker
	if rerankerEnabled && rerankerErr != nil {
		logger.Warn("reranker disabled; configuration failed", "error", rerankerErr)
	} else if rerankerEnabled {
		model, revision := reranker.RerankingInfo()
		logger.Info("Korean reranker enabled", "model", model, "revision", revision, "base_url", reranker.BaseURL, "candidates", rerankerCandidates)
		activeReranker = reranker
	} else {
		logger.Info("Korean reranker disabled")
	}
	if activeReranker != nil {
		verifyCtx, cancel := context.WithTimeout(context.Background(), *rerankerTimeout)
		verifyErr := reranker.VerifyReranker(verifyCtx)
		cancel()
		if verifyErr != nil {
			if rerankerRequired {
				logger.Error("required reranker identity check failed", "error", verifyErr)
				os.Exit(1)
			}
			logger.Warn("reranker disabled; identity check failed", "error", verifyErr)
			activeReranker = nil
		}
	}
	runtimeVectorMode := "bm25"
	if activeEmbedder != nil {
		runtimeVectorMode = "bm25+vector"
	}
	artifacts, descriptor, err := inspectArtifacts(
		repo,
		lexiconDigest,
		runtimeVectorMode,
		*retrievalCandidates,
		activeReranker,
		rerankerCandidates,
		*rerankerTimeout,
		strings.TrimSpace(os.Getenv("RULE_MCP_SERVER_IMAGE_DIGEST")),
		strings.TrimSpace(os.Getenv("RULE_MCP_TEI_IMAGE_DIGEST")),
		strings.TrimSpace(os.Getenv("RULE_MCP_RERANKER_IMAGE_DIGEST")),
	)
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
		"retrieval_candidate_limit", artifacts.RetrievalCandidateLimit,
		"reranker_model", artifacts.RerankerModel,
		"reranker_revision", artifacts.RerankerRevision,
		"reranker_image_digest", artifacts.RerankerImageDigest,
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
		Repo:                repo,
		Embedder:            activeEmbedder,
		VectorRequired:      vectorRequired,
		Reranker:            activeReranker,
		RerankerRequired:    rerankerRequired,
		RerankerCandidates:  rerankerCandidates,
		RetrievalCandidates: *retrievalCandidates,
		DomainLexicon:       lexicon,
		Logger:              logger,
		ReleaseGeneration:   artifacts.ReleaseGeneration,
		MaxQueryRunes:       *maxQueryRunes,
		EmbeddingTimeout:    *embedTimeout,
		RerankerTimeout:     *rerankerTimeout,
		ConcurrentSearches:  searchSlots(*maxSearches),
		Observer:            runtimeMetrics,
		MaxToolOutputBytes:  *toolOutputLimit,
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
			Addr:                   *addr,
			AuthMode:               httpAuth.Mode,
			TokenRegistry:          httpAuth.Registry,
			Origins:                splitCSV(*origins),
			RequestSizeLimit:       *requestLimit,
			ResponseSizeLimit:      *responseLimit,
			MaxConcurrentRequests:  *maxRequests,
			RequestTimeout:         *requestTimeout,
			ShutdownTimeout:        *shutdownTimeout,
			ReadinessEmbedTimeout:  *readinessTimeout,
			ReadinessRerankTimeout: *readinessRerankerTimeout,
			ExpectedGeneration:     strings.TrimSpace(*expectedGeneration),
			Artifacts:              artifacts,
			Metrics:                runtimeMetrics,
			Embedder:               activeEmbedder,
			VectorRequired:         vectorRequired,
			Reranker:               activeReranker,
			RerankerRequired:       rerankerRequired,
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
		ReleaseGeneration:       config.Artifacts.ReleaseGeneration,
		CorpusDigest:            config.Artifacts.CorpusDigest,
		IndexDigest:             config.Artifacts.IndexDigest,
		VectorDigest:            config.Artifacts.VectorDigest,
		VectorMetadataDigest:    config.Artifacts.VectorMetadataDigest,
		DomainLexiconDigest:     config.Artifacts.DomainLexiconDigest,
		RuntimeVectorMode:       config.Artifacts.RuntimeVectorMode,
		RetrievalCandidateLimit: config.Artifacts.RetrievalCandidateLimit,
		RerankerModel:           config.Artifacts.RerankerModel,
		RerankerRevision:        config.Artifacts.RerankerRevision,
		RerankerImageDigest:     config.Artifacts.RerankerImageDigest,
		ServerImageDigest:       config.Artifacts.ServerImageDigest,
		VectorCoverage:          repo.VectorCoverage,
		AuthMode:                string(config.AuthMode),
		AuthRegistryDigest:      config.TokenRegistry.Digest(),
		ActiveBearerTokens:      config.TokenRegistry.ActiveTokenCount(),
	})
	authenticated := security.WithBearerAuth(config.AuthMode, config.TokenRegistry,
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
	logger.Info("HTTP MCP server listening",
		"addr", config.Addr,
		"stateless", true,
		"auth_mode", config.AuthMode,
		"active_bearer_tokens", config.TokenRegistry.ActiveTokenCount(),
		"auth_registry_digest", config.TokenRegistry.Digest(),
	)
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
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if repo == nil || len(repo.Documents) == 0 {
			http.Error(w, "repository not ready", http.StatusServiceUnavailable)
			return
		}
		if config.ExpectedGeneration != "" && config.ExpectedGeneration != config.Artifacts.ReleaseGeneration {
			http.Error(w, "loaded release generation does not match expected generation", http.StatusServiceUnavailable)
			return
		}
		if config.VectorRequired {
			if repo.Engine == nil || !repo.Engine.HasVectors() || repo.VectorScope != searchindex.VectorScopeFull || repo.VectorCoverage != 1 {
				http.Error(w, "required full-coverage vector index is not ready", http.StatusServiceUnavailable)
				return
			}
			if config.Embedder == nil {
				http.Error(w, "required embedding service is not configured", http.StatusServiceUnavailable)
				return
			}
			timeout := config.ReadinessEmbedTimeout
			if timeout <= 0 {
				timeout = 5 * time.Second
			}
			ctx, cancel := context.WithTimeout(request.Context(), timeout)
			vectors, err := config.Embedder.Embed(ctx, []string{"상장 규정"})
			cancel()
			if err != nil {
				http.Error(w, "required embedding service is not ready", http.StatusServiceUnavailable)
				return
			}
			if err := validateReadinessEmbedding(config.Embedder, repo.Engine, vectors); err != nil {
				http.Error(w, "required embedding response is invalid", http.StatusServiceUnavailable)
				return
			}
		}
		if config.RerankerRequired {
			if config.Reranker == nil {
				http.Error(w, "required reranker service is not configured", http.StatusServiceUnavailable)
				return
			}
			timeout := config.ReadinessRerankTimeout
			if timeout <= 0 {
				timeout = 30 * time.Second
			}
			ctx, cancel := context.WithTimeout(request.Context(), timeout)
			if verifier, ok := config.Reranker.(searchindex.RerankerVerifier); ok {
				if err := verifier.VerifyReranker(ctx); err != nil {
					cancel()
					http.Error(w, "required reranker identity is not ready", http.StatusServiceUnavailable)
					return
				}
			}
			scores, err := config.Reranker.Rerank(ctx, "상장 심사", []string{"상장 심사 절차를 정한다.", "무관한 문장이다."})
			cancel()
			if err != nil || len(scores) != 2 {
				http.Error(w, "required reranker service is not ready", http.StatusServiceUnavailable)
				return
			}
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		registryDigest := config.TokenRegistry.Digest()
		if registryDigest == "" {
			registryDigest = "none"
		}
		_, _ = fmt.Fprintf(w,
			"ready release_generation=%s auth_mode=%s auth_registry_generation=%s active_bearer_tokens=%d\n",
			config.Artifacts.ReleaseGeneration,
			config.AuthMode,
			registryDigest,
			config.TokenRegistry.ActiveTokenCount(),
		)
	})
}

type httpAuthConfig struct {
	Mode     security.AuthMode
	Registry *security.BearerTokenRegistry
}

func loadHTTPAuthConfig(modeValue, tokenFile string) (httpAuthConfig, error) {
	mode, err := security.ParseAuthMode(modeValue)
	if err != nil {
		return httpAuthConfig{}, err
	}
	config := httpAuthConfig{Mode: mode}
	if mode == security.AuthModeDisabled {
		return config, nil
	}
	registry, err := security.LoadBearerTokenRegistry(tokenFile)
	if err != nil {
		return httpAuthConfig{}, err
	}
	config.Registry = registry
	return config, nil
}

func validateReadinessEmbedding(embedder searchindex.Embedder, engine *searchindex.Engine, vectors [][]float64) error {
	if len(vectors) != 1 {
		return fmt.Errorf("embedding canary returned %d vectors; expected 1", len(vectors))
	}
	if info, ok := embedder.(searchindex.EmbedderInfo); ok {
		_, dimensions := info.EmbeddingInfo()
		if dimensions > 0 && len(vectors[0]) != dimensions {
			return fmt.Errorf("embedding canary dimensions=%d want=%d", len(vectors[0]), dimensions)
		}
	}
	if engine == nil {
		return fmt.Errorf("vector engine is nil")
	}
	if err := engine.ValidateQueryVector(vectors[0]); err != nil {
		return fmt.Errorf("validate embedding canary: %w", err)
	}
	return nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envDataDir() string {
	return env("KRX_RULE_DATA_DIR", "data")
}

func envIndexDir() string {
	return env("KRX_RULE_INDEX_DIR", searchindex.DefaultIndexDir)
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

func inspectArtifacts(repo *searchindex.Repository, domainLexiconDigest, runtimeVectorMode string, retrievalCandidateLimit int, reranker searchindex.Reranker, rerankerCandidates int, rerankerTimeout time.Duration, serverImageDigest, teiImageDigest, rerankerImageDigest string) (artifactRuntime, releaseDescriptor, error) {
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
	var rerankerDescriptor *rerankerReleaseDescriptor
	var rerankerModel string
	var rerankerRevision string
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
			InputFormat:        string(metadata.InputFormat),
			Scope:              string(metadata.Scope),
			ExpectedChunkCount: metadata.ExpectedChunkCount,
			StoredVectorCount:  metadata.StoredVectorCount,
		}
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
		rerankerDescriptor = &rerankerReleaseDescriptor{
			Model: model, ModelRevision: revision, CandidateLimit: rerankerCandidates, BatchSize: batchSize, Mode: "weak-supported-korean", Timeout: rerankerTimeout.String(), InputFormat: "structured-korean-v1",
		}
		rerankerModel = model
		rerankerRevision = revision
	}
	descriptor := releaseDescriptor{
		Schema:                  "krx-rule-mcp-release-v4",
		CorpusReleaseHash:       repo.CorpusReleaseHash,
		IndexSourceHash:         repo.IndexSourceHash,
		IndexBuildHash:          repo.IndexBuildHash,
		BM25ArtifactDigest:      repo.BM25ArtifactDigest,
		BM25SnapshotVersion:     repo.BM25SnapshotVersion,
		IndexerVersion:          repo.IndexerVersion,
		Vector:                  vectorDescriptor,
		Reranker:                rerankerDescriptor,
		DomainLexiconDigest:     domainLexiconDigest,
		RuntimeVectorMode:       runtimeVectorMode,
		RetrievalCandidateLimit: retrievalCandidateLimit,
		ServerImageDigest:       serverImageDigest,
		TEIImageDigest:          teiImageDigest,
		RerankerImageDigest:     rerankerImageDigest,
	}
	descriptorJSON, err := json.Marshal(descriptor)
	if err != nil {
		return artifactRuntime{}, releaseDescriptor{}, fmt.Errorf("encode canonical release descriptor: %w", err)
	}
	generationHash := sha256.Sum256(descriptorJSON)
	generation := hex.EncodeToString(generationHash[:])
	return artifactRuntime{
		ReleaseGeneration:       generation,
		CorpusDigest:            repo.CorpusReleaseHash,
		IndexDigest:             repo.BM25ArtifactDigest,
		VectorDigest:            vectorDigest,
		VectorMetadataDigest:    vectorMetadataDigest,
		DomainLexiconDigest:     domainLexiconDigest,
		RuntimeVectorMode:       runtimeVectorMode,
		RetrievalCandidateLimit: retrievalCandidateLimit,
		RerankerModel:           rerankerModel,
		RerankerRevision:        rerankerRevision,
		RerankerImageDigest:     rerankerImageDigest,
		ServerImageDigest:       serverImageDigest,
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

func resolveRerankerPolicy(enabled bool, policy string, requireFlag bool) (bool, error) {
	policy = strings.ToLower(strings.TrimSpace(policy))
	if policy == "" {
		policy = "optional"
	}
	if policy != "optional" && policy != "required" {
		return false, fmt.Errorf("unsupported KRX_RERANKER_POLICY %q; expected optional or required", policy)
	}
	if !enabled {
		if requireFlag {
			return false, fmt.Errorf("--require-reranker needs KRX_RERANKER_ENABLED=true")
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
