package security

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Metrics struct {
	mu       sync.Mutex
	Requests map[string]int64
}

func NewMetrics() *Metrics {
	return &Metrics{Requests: map[string]int64{}}
}

func (m *Metrics) Count(status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Requests[status]++
}

func (m *Metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	for status, count := range m.Requests {
		_, _ = w.Write([]byte("krx_rule_mcp_http_requests_total{status=\"" + status + "\"} " + itoa64(count) + "\n"))
	}
}

func WithMetrics(metrics *Metrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		metrics.Count(http.StatusText(rec.status))
	})
}

func WithBearerToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			http.Error(w, "server bearer token is not configured", http.StatusInternalServerError)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		now := time.Now()
		mu.Lock()
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
