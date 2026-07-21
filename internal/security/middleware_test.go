package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBearerAndOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := WithOriginAllowlist([]string{"https://chatgpt.com"}, WithBearerToken("secret", next))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Origin", "https://chatgpt.com")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("authorized request status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bad origin status = %d", rec.Code)
	}
}

func TestBearerTokenRequiresBearerScheme(t *testing.T) {
	handler := WithBearerToken("secret", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bare token status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	req = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "bearer secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("case-insensitive bearer status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestRejectsKnownPlaceholderBearerTokens(t *testing.T) {
	for _, token := range []string{"", "change-me", "REPLACE_WITH_STRONG_RANDOM_TOKEN", "<token>"} {
		if err := ValidateBearerToken(token); err == nil {
			t.Fatalf("expected placeholder token %q to be rejected", token)
		}
	}
	if err := ValidateBearerToken("ci-token"); err != nil {
		t.Fatalf("test-only non-deployment token should remain usable: %v", err)
	}
}

func TestRateLimit(t *testing.T) {
	handler := WithRateLimit(1, time.Minute, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first request status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d", rec.Code)
	}
}

func TestRateLimitWindowReset(t *testing.T) {
	handler := WithRateLimit(1, 10*time.Millisecond, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.RemoteAddr = "192.0.2.44:1234"

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first request status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d", rec.Code)
	}

	time.Sleep(20 * time.Millisecond)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("request after window reset status = %d", rec.Code)
	}
}

func TestRejectedCredentialsDoNotConsumeAuthenticatedQuota(t *testing.T) {
	protected := WithBearerToken("strong-secret", WithRateLimit(1, time.Minute, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	for i := 0; i < 2; i++ {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		request.RemoteAddr = "192.0.2.70:1234"
		protected.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated status = %d", recorder.Code)
		}
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.RemoteAddr = "192.0.2.70:1234"
	request.Header.Set("Authorization", "Bearer strong-secret")
	protected.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("authenticated quota was consumed by rejected credentials: %d", recorder.Code)
	}
}

func TestMetricsRecorderPreservesFlusher(t *testing.T) {
	metrics := NewMetrics()
	base := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler := WithMetrics(metrics, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("wrapped ResponseWriter does not expose http.Flusher")
		}
		flusher.Flush()
		w.WriteHeader(http.StatusAccepted)
	}))

	handler.ServeHTTP(base, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if !base.flushed {
		t.Fatal("Flush was not delegated to underlying writer")
	}
	if base.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", base.Code, http.StatusAccepted)
	}
}

func TestStatusRecorderUnwrapsUnderlyingWriter(t *testing.T) {
	base := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: base}
	if rec.Unwrap() != base {
		t.Fatalf("Unwrap() = %#v, want underlying writer", rec.Unwrap())
	}
}

func TestConcurrencyLimitRejectsExcessRequest(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	handler := WithConcurrencyLimit(1, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", nil))
	}()
	<-entered
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("capacity response = %d headers=%v", rec.Code, rec.Header())
	}
	close(release)
	wg.Wait()
}

func TestRequestTimeoutPropagatesContextDeadline(t *testing.T) {
	handler := WithRequestTimeout(5*time.Millisecond, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		if r.Context().Err() != context.DeadlineExceeded {
			t.Errorf("context error = %v", r.Context().Err())
		}
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestMetricsExposeReleaseIdentity(t *testing.T) {
	metrics := NewMetrics()
	metrics.SetRuntimeInfo(RuntimeInfo{ReleaseGeneration: "gen", CorpusDigest: "corpus", IndexDigest: "index", RuntimeVectorMode: "bm25", VectorCoverage: 0.75})
	metrics.ObserveTool("search_rules", 25*time.Millisecond)
	metrics.CountEmbeddingFallback("deadline")
	rec := httptest.NewRecorder()
	metrics.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `release_generation="gen"`) ||
		!strings.Contains(body, `runtime_vector_mode="bm25"`) ||
		!strings.Contains(body, `krx_rule_mcp_tool_calls_total{tool="search_rules"} 1`) ||
		!strings.Contains(body, `krx_rule_mcp_embedding_fallback_total{reason="deadline"} 1`) ||
		!strings.Contains(body, `krx_rule_mcp_vector_coverage_ratio 0.75`) {
		t.Fatalf("missing release identity metric: %s", body)
	}
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (r *flushRecorder) Flush() {
	r.flushed = true
}
