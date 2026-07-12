package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/corpus"
	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	mcpserver "github.com/chromato99/krx-rule-mcp/internal/mcp"
	"github.com/chromato99/krx-rule-mcp/internal/model"
	"github.com/chromato99/krx-rule-mcp/internal/security"
	"gopkg.in/yaml.v3"
)

func TestStatelessHTTPContractChainWithPublishedGeneration(t *testing.T) {
	dataRoot, indexDir := writeHTTPContractRelease(t)
	repo, err := searchindex.LoadRepositoryGeneration(dataRoot, indexDir, searchindex.RepositoryLoadOptions{
		RequireCorpusManifest: true,
	})
	if err != nil {
		t.Fatalf("load published repository generation: %v", err)
	}
	if repo.GenerationID == "" || repo.CorpusReleaseHash == "" {
		t.Fatalf("published repository identity is incomplete: %#v", repo)
	}

	const (
		bearerToken = "integration-test-token"
		responseCap = int64(64 << 10)
	)
	server := mcpserver.NewServer(&mcpserver.Service{
		Repo:               repo,
		ReleaseGeneration:  repo.GenerationID,
		MaxToolOutputBytes: 32 << 10,
	}, "test")
	handler := security.WithBearerToken(
		bearerToken,
		withResponseSizeLimit(responseCap, statelessMCPHandler(server, nil)),
	)

	unauthorized := postHTTPContractJSON(t, handler, "", `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{}}`)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated initialize status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	initialized := postHTTPContractJSON(t, handler, bearerToken, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	assertHTTPContractResponse(t, initialized, responseCap)
	if !strings.Contains(initialized.Body.String(), "official KRX") {
		t.Fatalf("initialize response lacks legal-source instruction: %s", initialized.Body.String())
	}

	searched := postHTTPContractJSON(t, handler, bearerToken, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_rules","arguments":{"query":"증거금 청산 의무","language":"ko","limit":5}}}`)
	searchPayload := decodeHTTPContractStructured[struct {
		ReleaseGeneration string `json:"release_generation"`
		Results           []struct {
			ID             string   `json:"id"`
			MatchedChunkID string   `json:"matched_chunk_id"`
			ArticleID      string   `json:"article_id"`
			HeadingPath    []string `json:"heading_path"`
		} `json:"results"`
	}](t, searched, responseCap)
	if searchPayload.ReleaseGeneration != repo.GenerationID || len(searchPayload.Results) == 0 {
		t.Fatalf("search result does not identify the loaded generation: %#v", searchPayload)
	}
	match := searchPayload.Results[0]
	if match.ID != "integration-rule" || match.MatchedChunkID == "" || match.ArticleID != "제5조" || !containsHTTPContractHeading(match.HeadingPath, "제5조") {
		t.Fatalf("search result lost stable chunk/article anchors: %#v", match)
	}

	contextRequest := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_context","arguments":{"chunk_id":%q,"before_chunks":0,"after_chunks":1,"max_chars":4000}}}`,
		match.MatchedChunkID,
	)
	contextResult := postHTTPContractJSON(t, handler, bearerToken, contextRequest)
	contextPayload := decodeHTTPContractStructured[struct {
		Content string `json:"content"`
		Chunks  []struct {
			ID          string   `json:"id"`
			ArticleID   string   `json:"article_id"`
			HeadingPath []string `json:"heading_path"`
		} `json:"chunks"`
	}](t, contextResult, responseCap)
	if len(contextPayload.Chunks) == 0 || contextPayload.Chunks[0].ID != match.MatchedChunkID {
		t.Fatalf("context does not start at requested chunk %q: %#v", match.MatchedChunkID, contextPayload.Chunks)
	}
	if contextPayload.Chunks[0].ArticleID != "제5조" || !containsHTTPContractHeading(contextPayload.Chunks[0].HeadingPath, "제5조") || !strings.Contains(contextPayload.Content, match.MatchedChunkID) {
		t.Fatalf("context lost chunk identity or legal anchor: %#v content=%q", contextPayload.Chunks[0], contextPayload.Content)
	}

	resource := postHTTPContractJSON(t, handler, bearerToken, `{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"krx-rule://rules/integration-rule"}}`)
	resourcePayload := decodeHTTPContractResult[struct {
		Contents []struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"contents"`
	}](t, resource, responseCap)
	if len(resourcePayload.Contents) != 1 || resourcePayload.Contents[0].URI != "krx-rule://rules/integration-rule" || !strings.Contains(resourcePayload.Contents[0].Text, "증거금을 납부") {
		t.Fatalf("resource/read did not return the indexed rule body: %#v", resourcePayload)
	}
}

type httpContractWireResponse struct {
	JSONRPC string `json:"jsonrpc"`
	Result  struct {
		StructuredContent json.RawMessage `json:"structuredContent"`
	} `json:"result"`
	Error json.RawMessage `json:"error"`
}

func postHTTPContractJSON(t *testing.T, handler http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func assertHTTPContractResponse(t *testing.T, recorder *httptest.ResponseRecorder, maxBytes int64) httpContractWireResponse {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("MCP response status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if int64(recorder.Body.Len()) > maxBytes {
		t.Fatalf("MCP response bytes = %d, cap = %d", recorder.Body.Len(), maxBytes)
	}
	var response httpContractWireResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode MCP JSON-RPC response: %v; body=%s", err, recorder.Body.String())
	}
	if response.JSONRPC != "2.0" || len(response.Error) != 0 {
		t.Fatalf("unexpected MCP JSON-RPC response: %s", recorder.Body.String())
	}
	return response
}

func decodeHTTPContractStructured[T any](t *testing.T, recorder *httptest.ResponseRecorder, maxBytes int64) T {
	t.Helper()
	response := assertHTTPContractResponse(t, recorder, maxBytes)
	if len(response.Result.StructuredContent) == 0 {
		t.Fatalf("MCP tool response has no structuredContent: %s", recorder.Body.String())
	}
	var output T
	if err := json.Unmarshal(response.Result.StructuredContent, &output); err != nil {
		t.Fatalf("decode MCP structuredContent: %v; body=%s", err, recorder.Body.String())
	}
	return output
}

func decodeHTTPContractResult[T any](t *testing.T, recorder *httptest.ResponseRecorder, maxBytes int64) T {
	t.Helper()
	assertHTTPContractResponse(t, recorder, maxBytes)
	var envelope struct {
		Result T `json:"result"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode MCP result: %v; body=%s", err, recorder.Body.String())
	}
	return envelope.Result
}

func containsHTTPContractHeading(path []string, want string) bool {
	for _, heading := range path {
		if strings.HasPrefix(strings.ReplaceAll(heading, " ", ""), strings.ReplaceAll(want, " ", "")) {
			return true
		}
	}
	return false
}

func writeHTTPContractRelease(t *testing.T) (string, string) {
	t.Helper()
	dataRoot := t.TempDir()
	searchable := true
	body := "# 파생상품시장 업무규정\n\n## 제5조(청산 의무)\n\n① 회원은 거래소에 증거금을 납부하고 청산 의무를 이행하여야 한다.\n\n② 제7조에 따른 절차는 별도로 정한다."
	doc := model.Document{
		SchemaVersion:      corpus.IndexSourceSchemaVersion,
		ID:                 "integration-rule",
		Title:              "파생상품시장 업무규정",
		SourceURL:          "https://example.test/out/regulation/regulationViewPop.do",
		CollectedAt:        time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		ContentHash:        model.HashText("파생상품시장 업무규정\n" + body),
		BodyHash:           model.HashText(body),
		ConversionStatus:   "converted",
		PreservationStatus: "preserved",
		Searchable:         &searchable,
		QualityStatus:      "ok",
		Language:           model.LanguageKorean,
		DocumentType:       model.DocumentTypeRule,
		Body:               body,
	}

	relativePath := filepath.ToSlash(filepath.Join("ko", "rules", "integration-rule", "index.md"))
	documentPath := filepath.Join(dataRoot, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(documentPath), 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := doc
	metadata.Body = ""
	var markdown bytes.Buffer
	markdown.WriteString("---\n")
	encoder := yaml.NewEncoder(&markdown)
	encoder.SetIndent(2)
	if err := encoder.Encode(metadata); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	markdown.WriteString("---\n\n")
	markdown.WriteString(body)
	markdown.WriteByte('\n')
	if err := os.WriteFile(documentPath, markdown.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	indexSourceHash, err := corpus.IndexSourceHash([]model.Document{doc}, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	var manifestDocument map[string]any
	if err := json.Unmarshal(metadataJSON, &manifestDocument); err != nil {
		t.Fatal(err)
	}
	manifestDocument["path"] = relativePath
	manifest := map[string]any{
		"schema_version":    corpus.IndexSourceSchemaVersion,
		"version":           "http-contract-test",
		"generated_at":      "2026-07-01T00:00:00Z",
		"source":            "http-contract-test",
		"documents":         []any{manifestDocument},
		"attachment_log":    []any{},
		"index_source_hash": indexSourceHash,
		"release_profile": map[string]any{
			"version":             1,
			"default":             "strict",
			"allowed_failure_ids": []any{},
		},
	}
	releaseProjection := make(map[string]any, len(manifest)-1)
	for key, value := range manifest {
		if key != "generated_at" {
			releaseProjection[key] = value
		}
	}
	canonicalRelease, err := json.Marshal(releaseProjection)
	if err != nil {
		t.Fatal(err)
	}
	manifest["release_hash"] = model.HashBytes(canonicalRelease)
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataRoot, "manifest.json"), append(manifestJSON, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot, _, err := searchindex.BuildReleaseSnapshot(dataRoot)
	if err != nil {
		t.Fatalf("build release snapshot: %v", err)
	}
	indexDir := filepath.Join(t.TempDir(), "index")
	lock, err := searchindex.AcquireGenerationBuildLock(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Publish(searchindex.GenerationBuild{Snapshot: snapshot}); err != nil {
		_ = lock.Close()
		t.Fatalf("publish release snapshot: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	return dataRoot, indexDir
}
