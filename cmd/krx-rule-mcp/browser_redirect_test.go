package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chromato99/krx-rule-mcp/internal/security"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBrowserProjectRedirectPreservesMCPTransport(t *testing.T) {
	handler := statelessMCPHandler(mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "test"}, nil), nil)
	for _, tc := range []struct {
		name     string
		accept   []string
		headers  map[string]string
		redirect bool
	}{
		{name: "browser navigation", accept: []string{"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"}, headers: map[string]string{"Sec-Fetch-Mode": "navigate"}, redirect: true},
		{name: "explicit HTML", accept: []string{"text/html"}, redirect: true},
		{name: "HTML quality and case", accept: []string{"Text/HTML; q=0.5"}, redirect: true},
		{name: "SSE", accept: []string{"text/event-stream"}},
		{name: "HTML preferred over SSE", accept: []string{"text/html, text/event-stream;q=0.1"}},
		{name: "SSE in another header", accept: []string{"text/html", "text/event-stream"}},
		{name: "JSON", accept: []string{"text/html, application/json"}},
		{name: "MCP media type explicitly excluded", accept: []string{"text/html, text/event-stream;q=0"}},
		{name: "wildcard only", accept: []string{"*/*"}},
		{name: "no Accept"},
		{name: "HTML excluded", accept: []string{"text/html;q=0, */*"}},
		{name: "invalid quality", accept: []string{"text/html;q=invalid"}},
		{name: "quality out of range", accept: []string{"text/html;q=2"}},
		{name: "non-finite quality", accept: []string{"text/html;q=NaN"}},
		{name: "malformed Accept", accept: []string{"text/html, invalid"}},
		{name: "MCP protocol", accept: []string{"text/html, */*"}, headers: map[string]string{"MCP-Protocol-Version": "2025-11-25"}},
		{name: "MCP session", accept: []string{"text/html, */*"}, headers: map[string]string{"MCP-Session-Id": "session"}},
		{name: "SSE resume", accept: []string{"text/html, */*"}, headers: map[string]string{"Last-Event-ID": "event"}},
		{name: "fetch instead of navigation", accept: []string{"text/html, */*"}, headers: map[string]string{"Sec-Fetch-Mode": "cors"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/mcp?token=do-not-forward&next=https://example.test", nil)
			for _, accept := range tc.accept {
				request.Header.Add("Accept", accept)
			}
			for name, value := range tc.headers {
				request.Header.Set(name, value)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Header().Get("Cache-Control") != "no-store" || !strings.Contains(recorder.Header().Get("Vary"), "Accept") {
				t.Fatalf("GET response may be cached across clients: %v", recorder.Header())
			}
			if tc.redirect {
				if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != projectURL || recorder.Header().Get("Referrer-Policy") != "no-referrer" {
					t.Fatalf("browser redirect = %d %v", recorder.Code, recorder.Header())
				}
			} else if recorder.Code < 400 || recorder.Header().Get("Location") != "" {
				t.Fatalf("MCP GET did not reach SDK: %d %v", recorder.Code, recorder.Header())
			}
			if tc.name == "SSE" && recorder.Code != http.StatusMethodNotAllowed {
				t.Fatalf("SSE status = %d, want 405", recorder.Code)
			}
		})
	}

	for _, method := range []string{http.MethodHead, http.MethodOptions, http.MethodDelete} {
		request := httptest.NewRequest(method, "/mcp", nil)
		request.Header.Set("Accept", "text/html")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code < 400 || recorder.Header().Get("Location") != "" {
			t.Fatalf("%s unexpectedly redirected: %d %v", method, recorder.Code, recorder.Header())
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/html, application/json, text/event-stream")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Location") != "" || !strings.Contains(recorder.Body.String(), `"serverInfo"`) {
		t.Fatalf("MCP initialize changed: %d %v %s", recorder.Code, recorder.Header(), recorder.Body)
	}
}

func TestBrowserProjectRedirectRemainsBehindSecurityChecks(t *testing.T) {
	registry := loadHTTPContractBearerRegistry(t, "test-token")
	handler := security.WithOriginAllowlist([]string{"https://allowed.example"},
		security.WithBearerAuth(security.AuthModeRequired, registry,
			statelessMCPHandler(mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "test"}, nil), nil)))
	for _, tc := range []struct {
		name   string
		token  string
		origin string
		status int
	}{
		{name: "missing token", status: http.StatusUnauthorized},
		{name: "wrong token", token: "wrong", status: http.StatusUnauthorized},
		{name: "untrusted origin", token: "test-token", origin: "https://blocked.example", status: http.StatusForbidden},
		{name: "allowed browser", token: "test-token", origin: "https://allowed.example", status: http.StatusFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/mcp", nil)
			request.Header.Set("Accept", "text/html")
			if tc.token != "" {
				request.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.origin != "" {
				request.Header.Set("Origin", tc.origin)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.status)
			}
			if tc.status != http.StatusFound && recorder.Header().Get("Location") != "" {
				t.Fatalf("security rejection redirected: %v", recorder.Header())
			}
		})
	}
}
