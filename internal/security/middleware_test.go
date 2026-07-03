package security

import (
	"net/http"
	"net/http/httptest"
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

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (r *flushRecorder) Flush() {
	r.flushed = true
}
