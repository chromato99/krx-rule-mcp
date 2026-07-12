package security

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Metrics struct {
	mu                  sync.Mutex
	Requests            map[string]int64
	ToolCalls           map[string]int64
	ToolDurationSeconds map[string]float64
	EmbeddingFallbacks  map[string]int64
	runtimeInfo         RuntimeInfo
}

type RuntimeInfo struct {
	ReleaseGeneration    string
	CorpusDigest         string
	IndexDigest          string
	VectorDigest         string
	VectorMetadataDigest string
	DomainLexiconDigest  string
	RuntimeVectorMode    string
	ServerImageDigest    string
	VectorCoverage       float64
}

func NewMetrics() *Metrics {
	return &Metrics{
		Requests:            map[string]int64{},
		ToolCalls:           map[string]int64{},
		ToolDurationSeconds: map[string]float64{},
		EmbeddingFallbacks:  map[string]int64{},
	}
}

func (m *Metrics) Count(status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Requests[status]++
}

func (m *Metrics) SetRuntimeInfo(info RuntimeInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runtimeInfo = info
}

func (m *Metrics) ObserveTool(name string, elapsed time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ToolCalls[name]++
	m.ToolDurationSeconds[name] += elapsed.Seconds()
}

func (m *Metrics) CountEmbeddingFallback(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.EmbeddingFallbacks[reason]++
}

func (m *Metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	for status, count := range m.Requests {
		_, _ = fmt.Fprintf(w, "krx_rule_mcp_http_requests_total{status=\"%s\"} %s\n", prometheusLabel(status), itoa64(count))
	}
	for tool, count := range m.ToolCalls {
		_, _ = fmt.Fprintf(w, "krx_rule_mcp_tool_calls_total{tool=\"%s\"} %s\n", prometheusLabel(tool), itoa64(count))
		_, _ = fmt.Fprintf(w, "krx_rule_mcp_tool_duration_seconds_sum{tool=\"%s\"} %g\n", prometheusLabel(tool), m.ToolDurationSeconds[tool])
	}
	for reason, count := range m.EmbeddingFallbacks {
		_, _ = fmt.Fprintf(w, "krx_rule_mcp_embedding_fallback_total{reason=\"%s\"} %s\n", prometheusLabel(reason), itoa64(count))
	}
	if m.runtimeInfo.ReleaseGeneration != "" {
		_, _ = fmt.Fprintf(w,
			"krx_rule_mcp_release_info{release_generation=\"%s\",corpus_digest=\"%s\",index_digest=\"%s\",vector_digest=\"%s\",vector_metadata_digest=\"%s\",domain_lexicon_digest=\"%s\",runtime_vector_mode=\"%s\",server_image_digest=\"%s\"} 1\n",
			prometheusLabel(m.runtimeInfo.ReleaseGeneration),
			prometheusLabel(m.runtimeInfo.CorpusDigest),
			prometheusLabel(m.runtimeInfo.IndexDigest),
			prometheusLabel(m.runtimeInfo.VectorDigest),
			prometheusLabel(m.runtimeInfo.VectorMetadataDigest),
			prometheusLabel(m.runtimeInfo.DomainLexiconDigest),
			prometheusLabel(m.runtimeInfo.RuntimeVectorMode),
			prometheusLabel(m.runtimeInfo.ServerImageDigest),
		)
		_, _ = fmt.Fprintf(w, "krx_rule_mcp_vector_coverage_ratio %g\n", m.runtimeInfo.VectorCoverage)
	}
}

func ValidateBearerToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("bearer token is required")
	}
	normalized := strings.ToLower(token)
	knownPlaceholders := []string{
		"change-me",
		"changeme",
		"replace-me",
		"replace_with_strong_random_token",
		"example-token",
		"your-token-here",
	}
	for _, placeholder := range knownPlaceholders {
		if normalized == placeholder {
			return fmt.Errorf("bearer token uses a known placeholder value")
		}
	}
	if strings.HasPrefix(normalized, "replace_with_") || strings.Contains(normalized, "<token>") {
		return fmt.Errorf("bearer token uses a placeholder value")
	}
	return nil
}

func WithMetrics(metrics *Metrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		metrics.Count(http.StatusText(rec.status))
	})
}

func WithBearerToken(token string, next http.Handler) http.Handler {
	configurationErr := ValidateBearerToken(token)
	token = strings.TrimSpace(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if configurationErr != nil {
			http.Error(w, "server bearer token is not configured", http.StatusInternalServerError)
			return
		}
		got, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(header string) (string, bool) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || fields[1] == "" {
		return "", false
	}
	return fields[1], true
}

func WithOriginAllowlist(allowlist []string, next http.Handler) http.Handler {
	allowed := map[string]struct{}{}
	for _, origin := range allowlist {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowed[origin] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := allowed[origin]; !ok {
			http.Error(w, "origin forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Vary", "Origin")
		next.ServeHTTP(w, r)
	})
}

func WithRequestSizeLimit(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if maxBytes > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func WithRequestTimeout(timeout time.Duration, next http.Handler) http.Handler {
	if timeout <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func WithConcurrencyLimit(limit int, next http.Handler) http.Handler {
	if limit <= 0 {
		return next
	}
	semaphore := make(chan struct{}, limit)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case semaphore <- struct{}{}:
			defer func() { <-semaphore }()
			next.ServeHTTP(w, r)
		case <-r.Context().Done():
			http.Error(w, "request cancelled while waiting for capacity", http.StatusServiceUnavailable)
		default:
			w.Header().Set("Retry-After", "1")
			http.Error(w, "server is at request capacity", http.StatusServiceUnavailable)
		}
	})
}

func WithRateLimit(limit int, window time.Duration, next http.Handler) http.Handler {
	if limit <= 0 || window <= 0 {
		return next
	}
	type bucket struct {
		reset time.Time
		count int
	}
	var mu sync.Mutex
	buckets := map[string]bucket{}
	lastCleanup := time.Now()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		now := time.Now()
		mu.Lock()
		if now.Sub(lastCleanup) >= window {
			for key, item := range buckets {
				if now.After(item.reset) {
					delete(buckets, key)
				}
			}
			lastCleanup = now
		}
		b := buckets[host]
		if b.reset.IsZero() || now.After(b.reset) {
			b = bucket{reset: now.Add(window)}
		}
		b.count++
		buckets[host] = b
		mu.Unlock()
		if b.count > limit {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func itoa64(i int64) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func prometheusLabel(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\n", "\\n")
	return strings.ReplaceAll(value, "\"", "\\\"")
}
