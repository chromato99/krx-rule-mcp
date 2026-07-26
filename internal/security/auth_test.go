package security

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAuthMode(t *testing.T) {
	for _, test := range []struct {
		value string
		want  AuthMode
	}{
		{value: "required", want: AuthModeRequired},
		{value: " REQUIRED ", want: AuthModeRequired},
		{value: "disabled", want: AuthModeDisabled},
	} {
		got, err := ParseAuthMode(test.value)
		if err != nil || got != test.want {
			t.Fatalf("ParseAuthMode(%q) = (%q, %v), want %q", test.value, got, err, test.want)
		}
	}
	if _, err := ParseAuthMode("optional"); err == nil {
		t.Fatal("optional auth mode should be rejected")
	}
}

func TestBearerTokenRegistryAcceptsMultipleEnabledTokens(t *testing.T) {
	registry := newTestBearerRegistry(t, "first-token", "second-token")
	if registry.ActiveTokenCount() != 2 || registry.Digest() == "" {
		t.Fatalf("registry metadata = count %d digest %q", registry.ActiveTokenCount(), registry.Digest())
	}
	for _, token := range []string{"first-token", "second-token"} {
		if !registry.accepts(token) {
			t.Fatalf("registry rejected enabled token %q", token)
		}
	}
	for _, token := range []string{"", "wrong-token", strings.Repeat("x", maxBearerTokenLength+1)} {
		if registry.accepts(token) {
			t.Fatalf("registry accepted invalid token of length %d", len(token))
		}
	}
}

func TestBearerTokenRegistryRejectsDisabledToken(t *testing.T) {
	enabledHash := bearerTokenSHA256("enabled-token")
	disabledHash := bearerTokenSHA256("disabled-token")
	registry, err := parseBearerTokenRegistry([]byte(fmt.Sprintf(`version: 1
tokens:
  - id: enabled
    sha256: %s
    enabled: true
  - id: disabled
    sha256: %s
    enabled: false
`, enabledHash, disabledHash)))
	if err != nil {
		t.Fatal(err)
	}
	if !registry.accepts("enabled-token") || registry.accepts("disabled-token") {
		t.Fatal("enabled/disabled token policy was not enforced")
	}
}

func TestBearerTokenRegistryValidation(t *testing.T) {
	validHash := bearerTokenSHA256("valid-token")
	tests := []struct {
		name string
		data string
	}{
		{name: "empty", data: ""},
		{name: "unknown field", data: "version: 1\nunknown: true\ntokens: []\n"},
		{name: "wrong version", data: fmt.Sprintf("version: 2\ntokens:\n  - id: one\n    sha256: %s\n    enabled: true\n", validHash)},
		{name: "no tokens", data: "version: 1\ntokens: []\n"},
		{name: "invalid id", data: fmt.Sprintf("version: 1\ntokens:\n  - id: bad id\n    sha256: %s\n    enabled: true\n", validHash)},
		{name: "invalid hash", data: "version: 1\ntokens:\n  - id: one\n    sha256: ABCD\n    enabled: true\n"},
		{name: "missing enabled", data: fmt.Sprintf("version: 1\ntokens:\n  - id: one\n    sha256: %s\n", validHash)},
		{name: "no enabled tokens", data: fmt.Sprintf("version: 1\ntokens:\n  - id: one\n    sha256: %s\n    enabled: false\n", validHash)},
		{name: "duplicate id", data: fmt.Sprintf("version: 1\ntokens:\n  - id: one\n    sha256: %s\n    enabled: true\n  - id: one\n    sha256: %s\n    enabled: false\n", validHash, bearerTokenSHA256("other"))},
		{name: "duplicate hash", data: fmt.Sprintf("version: 1\ntokens:\n  - id: one\n    sha256: %s\n    enabled: true\n  - id: two\n    sha256: %s\n    enabled: false\n", validHash, validHash)},
		{name: "multiple documents", data: fmt.Sprintf("version: 1\ntokens:\n  - id: one\n    sha256: %s\n    enabled: true\n---\nversion: 1\n", validHash)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseBearerTokenRegistry([]byte(test.data)); err == nil {
				t.Fatalf("invalid registry was accepted:\n%s", test.data)
			}
		})
	}
}

func TestBearerTokenRegistryLimits(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("version: 1\ntokens:\n")
	for i := 0; i <= MaxBearerTokens; i++ {
		_, _ = fmt.Fprintf(&builder, "  - id: token-%d\n    sha256: %s\n    enabled: true\n", i, bearerTokenSHA256(fmt.Sprintf("token-%d", i)))
	}
	if _, err := parseBearerTokenRegistry([]byte(builder.String())); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("too many tokens error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "tokens.yaml")
	if err := os.WriteFile(path, make([]byte, MaxBearerTokenRegistryBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBearerTokenRegistry(path); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized registry error = %v", err)
	}
}

func TestBearerTokenRegistryIsStartupSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.yaml")
	writeTestBearerRegistry(t, path, "old-token")
	oldRegistry, err := LoadBearerTokenRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	writeTestBearerRegistry(t, path, "new-token")
	if !oldRegistry.accepts("old-token") || oldRegistry.accepts("new-token") {
		t.Fatal("loaded registry changed when its source file was updated")
	}
	newRegistry, err := LoadBearerTokenRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if newRegistry.accepts("old-token") || !newRegistry.accepts("new-token") {
		t.Fatal("reloaded registry did not reflect the updated source file")
	}
}

func TestWithBearerAuthRequiredAndDisabled(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	registry := newTestBearerRegistry(t, "accepted-token")
	required := WithBearerAuth(AuthModeRequired, registry, next)

	for _, test := range []struct {
		name   string
		header string
		want   int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "bare", header: "accepted-token", want: http.StatusUnauthorized},
		{name: "wrong", header: "Bearer wrong-token", want: http.StatusUnauthorized},
		{name: "accepted", header: "bearer accepted-token", want: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			request.Header.Set("Authorization", test.header)
			recorder := httptest.NewRecorder()
			required.ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d", recorder.Code, test.want)
			}
			if test.want == http.StatusUnauthorized && recorder.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("WWW-Authenticate = %q", recorder.Header().Get("WWW-Authenticate"))
			}
		})
	}

	disabled := WithBearerAuth(AuthModeDisabled, nil, next)
	recorder := httptest.NewRecorder()
	disabled.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("disabled auth status = %d", recorder.Code)
	}
}

func newTestBearerRegistry(t *testing.T, tokens ...string) *BearerTokenRegistry {
	t.Helper()
	var builder strings.Builder
	builder.WriteString("version: 1\ntokens:\n")
	for i, token := range tokens {
		_, _ = fmt.Fprintf(&builder, "  - id: token-%d\n    sha256: %s\n    enabled: true\n", i, bearerTokenSHA256(token))
	}
	registry, err := parseBearerTokenRegistry([]byte(builder.String()))
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func writeTestBearerRegistry(t *testing.T, path, token string) {
	t.Helper()
	data := fmt.Sprintf("version: 1\ntokens:\n  - id: test\n    sha256: %s\n    enabled: true\n", bearerTokenSHA256(token))
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func bearerTokenSHA256(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
