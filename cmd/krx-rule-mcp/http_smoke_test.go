package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	mcpserver "github.com/chromato99/krx-rule-mcp/internal/mcp"
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
	grounded := callSearchRules(t, ctx, session, map[string]any{
		"query": "위탁증거금의 사용제한 대용증권 외화", "language": "ko", "limit": 5,
	})
	if grounded.Retrieval.Status != mcpserver.RetrievalCandidatesFound || len(grounded.Results) == 0 {
		t.Fatalf("grounded canary has no candidates: %#v", grounded)
	}
	verifySmokeReturnedContexts(t, ctx, session, grounded)
	for _, limit := range []int{0, 1, 5, 50} {
		args := map[string]any{"query": "KRX 회원의 운전면허 갱신 의무", "language": "ko"}
		if limit != 0 {
			args["limit"] = limit
		}
		out := callSearchRules(t, ctx, session, args)
		if out.Retrieval.ReturnedResults != len(out.Results) || len(out.Results) > max(10, limit) {
			t.Fatalf("unbounded default/limit=%d response: %#v", limit, out)
		}
	}
	ambiguous := callSearchRules(t, ctx, session, map[string]any{"query": "장중에 증거금 추가 통지를 받으면 몇 시까지 채워야 하나", "language": "ko", "limit": 5})
	if ambiguous.Retrieval.Status != mcpserver.RetrievalCandidatesFound || len(ambiguous.Results) == 0 {
		t.Fatalf("missing candidates for caller scope review: %#v", ambiguous)
	}
	verifySmokeReturnedContexts(t, ctx, session, ambiguous)
}

type smokeSearchOutput struct {
	ContractVersion string                  `json:"contract_version"`
	Retrieval       mcpserver.RetrievalInfo `json:"retrieval"`
	Mode            string                  `json:"mode"`
	QueryExpansion  *smokeQueryExpansion    `json:"query_expansion"`
	Results         []smokeSearchResult     `json:"results"`
}

type smokeQueryExpansion struct {
	AppliedTerms []smokeAppliedTerm `json:"applied_terms"`
}

type smokeAppliedTerm struct {
	AddedTerms []string `json:"added_terms"`
}

type smokeSearchResult struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Language        string `json:"language"`
	EvidenceMatches []struct {
		ChunkID      string `json:"chunk_id"`
		ArticleID    string `json:"article_id"`
		AttachmentID string `json:"attachment_id"`
	} `json:"evidence_matches"`
}

func verifySmokeReturnedContexts(t *testing.T, ctx context.Context, session *mcpsdk.ClientSession, out smokeSearchOutput) {
	t.Helper()
	type owner struct{ doc, article, attachment string }
	returned := map[string]owner{}
	for _, result := range out.Results {
		for _, evidence := range result.EvidenceMatches {
			returned[evidence.ChunkID] = owner{result.ID, evidence.ArticleID, evidence.AttachmentID}
		}
	}
	if len(returned) == 0 {
		t.Fatal("no returned context IDs")
	}
	for id, want := range returned {
		response, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "get_context", Arguments: map[string]any{"chunk_id": id, "before_chunks": 0, "after_chunks": 0, "max_chars": 50000}})
		if err != nil {
			t.Fatal(err)
		}
		if response.IsError {
			t.Fatalf("get_context tool error: %#v", response.Content)
		}
		data, err := json.Marshal(response.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var contextOut mcpserver.ContextOutput
		if err := json.Unmarshal(data, &contextOut); err != nil {
			t.Fatal(err)
		}
		if contextOut.Truncated || contextOut.Document.ID != want.doc || len(contextOut.Chunks) != 1 {
			t.Fatalf("context owner/window mismatch: %#v", contextOut)
		}
		chunk := contextOut.Chunks[0]
		if chunk.ID != id || chunk.ArticleID != want.article || chunk.AttachmentID != want.attachment || strings.TrimSpace(chunk.Text) == "" {
			t.Fatalf("returned context mismatch: %#v", chunk)
		}
	}
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
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["answerable"] != nil || fields["answerability"] != nil || out.ContractVersion != mcpserver.SearchContractVersion {
		t.Fatalf("unexpected answer-decision contract: %s", data)
	}
	limit, _ := args["limit"].(int)
	if limit == 0 {
		limit = 10
	}
	expectedStatus := mcpserver.RetrievalNoCandidates
	if len(out.Results) > 0 {
		expectedStatus = mcpserver.RetrievalCandidatesFound
	}
	if out.Retrieval.Status != expectedStatus || out.Retrieval.ReturnedResults != len(out.Results) || len(out.Results) > limit {
		t.Fatalf("invalid bounded retrieval response: %s", data)
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
