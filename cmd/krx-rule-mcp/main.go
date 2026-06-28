package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	mcpserver "github.com/chromato99/krx-rule-mcp/internal/mcp"
	"github.com/chromato99/krx-rule-mcp/internal/security"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

func main() {
	var (
		mode         = flag.String("mode", env("KRX_MCP_MODE", "stdio"), "transport mode: stdio or http")
		addr         = flag.String("addr", env("KRX_MCP_ADDR", ":8080"), "HTTP listen address")
		dataDir      = flag.String("data-dir", envDataDir(), "data directory")
		indexPath    = flag.String("index", "", "BM25/core index snapshot path")
		vectorIndex  = flag.String("vector-index", os.Getenv("KRX_VECTOR_INDEX_PATH"), "optional vector snapshot path")
		lexiconPath  = flag.String("domain-lexicon", env("KRX_DOMAIN_LEXICON_PATH", searchindex.DefaultDomainLexiconPath), "domain lexicon YAML path for query expansion")
		token        = flag.String("token", os.Getenv("KRX_MCP_BEARER_TOKEN"), "required bearer token for HTTP mode")
		origins      = flag.String("allowed-origins", os.Getenv("KRX_MCP_ALLOWED_ORIGINS"), "comma-separated Origin allowlist for HTTP mode")
		requestLimit = flag.Int64("request-size-limit", 1<<20, "maximum HTTP request body size in bytes")
	)
	flag.Parse()
	if strings.TrimSpace(*indexPath) == "" {
		*indexPath = env("KRX_INDEX_PATH", filepath.Join(*dataDir, "index", "bm25.krxidx"))
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	repo, err := searchindex.LoadRepository(*dataDir, *indexPath, *vectorIndex)
	if err != nil {
		logger.Error("load repository failed", "error", err)
		os.Exit(1)
	}
	logger.Info("repository loaded", "documents", len(repo.Documents), "attachments", len(repo.Attachments))

	lexicon, err := searchindex.LoadDomainLexicon(*lexiconPath)
	if err != nil {
		logger.Error("load domain lexicon failed", "path", *lexiconPath, "error", err)
		os.Exit(1)
	}
	logger.Info("domain lexicon loaded", "path", *lexiconPath, "entries", len(lexicon))

	embedder, enabled, err := searchindex.NewEmbedderFromEnv()
	var activeEmbedder searchindex.Embedder
	if enabled && err != nil {
		logger.Warn("vector search disabled; embedding configuration failed", "error", err)
	} else if enabled && repo.Engine.HasVectors() {
		logger.Info("vector search enabled", "model", embedder.Model, "base_url", embedder.BaseURL)
		activeEmbedder = embedder
	} else if enabled {
		logger.Warn("vector search disabled; vector snapshot has no stored vectors")
	} else {
		logger.Info("vector search disabled; BM25-only mode")
	}

	service := &mcpserver.Service{Repo: repo, Embedder: activeEmbedder, DomainLexicon: lexicon, Logger: logger}
	server := mcpserver.NewServer(service, version)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch strings.ToLower(*mode) {
	case "stdio":
		if err := server.Run(ctx, &mcpsdk.StdioTransport{}); err != nil && ctx.Err() == nil {
			logger.Error("stdio server failed", "error", err)
			os.Exit(1)
		}
	case "http":
		if strings.TrimSpace(*token) == "" {
			logger.Error("HTTP mode requires KRX_MCP_BEARER_TOKEN or -token")
			os.Exit(1)
		}
		if err := runHTTP(ctx, *addr, *token, splitCSV(*origins), *requestLimit, server, logger); err != nil {
			logger.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	default:
		logger.Error("unknown mode", "mode", *mode)
		os.Exit(1)
	}
}

func runHTTP(ctx context.Context, addr, token string, origins []string, requestLimit int64, server *mcpsdk.Server, logger *slog.Logger) error {
	mcpHandler := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server {
		return server
	}, &mcpsdk.StreamableHTTPOptions{
		Logger:         logger,
		SessionTimeout: 30 * time.Minute,
	})
	metrics := security.NewMetrics()
	protected := security.WithMetrics(metrics,
		security.WithRateLimit(120, time.Minute,
			security.WithRequestSizeLimit(requestLimit,
				security.WithOriginAllowlist(origins,
					security.WithBearerToken(token, mcpHandler)))))

	mux := http.NewServeMux()
	mux.Handle("/mcp", protected)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ready\n")) })
	mux.Handle("/metrics", metrics)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	logger.Info("HTTP MCP server listening", "addr", addr)
	err := httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
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
