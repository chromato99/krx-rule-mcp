package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHTTPMCPManualSmoke(t *testing.T) {
	endpoint := strings.TrimSpace(os.Getenv("KRX_HTTP_SMOKE_ENDPOINT"))
	if endpoint == "" {
		t.Skip("set KRX_HTTP_SMOKE_ENDPOINT to run the HTTP MCP smoke test")
	}
	token := strings.TrimSpace(os.Getenv("KRX_HTTP_SMOKE_TOKEN"))
	if token == "" {
		t.Fatal("KRX_HTTP_SMOKE_TOKEN is required")
	}

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "krx-rule-mcp-smoke", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), &mcpsdk.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{Transport: bearerTokenTransport{
			token: token,
			base:  http.DefaultTransport,
		}},
	}, nil)
	if err != nil {
		t.Fatalf("connect HTTP MCP: %v", err)
	}
	defer session.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	korean := callSearchRules(t, ctx, session, map[string]any{
		"query":    "동적상하한가",
		"language": "ko",
		"limit":    5,
	})
	if len(korean.Results) == 0 {
		t.Fatal("Korean search returned no results")
	}
	if korean.QueryExpansion == nil || !containsSmokeExpansionTerm(korean.QueryExpansion, "실시간가격제한제도") {
		t.Fatalf("Korean search did not apply expected domain expansion: %#v", korean.QueryExpansion)
	}
	for _, result := range korean.Results {
		if result.Language != "ko" {
			t.Fatalf("Korean search returned non-Korean result: %#v", result)
		}
	}

	english := callSearchRules(t, ctx, session, map[string]any{
		"query":    "dynamic price limit",
		"language": "en",
		"limit":    5,
	})
	if len(english.Results) == 0 {
		t.Fatal("English search returned no results")
	}
	for _, result := range english.Results {
		if result.Language != "en" {
			t.Fatalf("English search returned non-English result: %#v", result)
		}
	}
}

type smokeSearchOutput struct {
	Mode           string               `json:"mode"`
	QueryExpansion *smokeQueryExpansion `json:"query_expansion"`
	Results        []smokeSearchResult  `json:"results"`
}

type smokeQueryExpansion struct {
	AppliedTerms []smokeAppliedTerm `json:"applied_terms"`
}

type smokeAppliedTerm struct {
	AddedTerms []string `json:"added_terms"`
}

type smokeSearchResult struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Language string `json:"language"`
}

func callSearchRules(t *testing.T, ctx context.Context, session *mcpsdk.ClientSession, args map[string]any) smokeSearchOutput {
	t.Helper()
	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "search_rules", Arguments: args})
	if err != nil {
		t.Fatalf("search_rules(%v): %v", args, err)
	}
	if result.IsError {
		t.Fatalf("search_rules(%v) returned tool error: %#v", args, result.Content)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out smokeSearchOutput
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal structured content: %v\n%s", err, data)
	}
	if out.Mode == "" {
		t.Fatalf("search_rules(%v) returned empty mode: %#v", args, out)
	}
	return out
}

func containsSmokeExpansionTerm(expansion *smokeQueryExpansion, term string) bool {
	for _, applied := range expansion.AppliedTerms {
		for _, added := range applied.AddedTerms {
			if added == term {
				return true
			}
		}
	}
	return false
}

type bearerTokenTransport struct {
	token string
	base  http.RoundTripper
}

func (t bearerTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}
