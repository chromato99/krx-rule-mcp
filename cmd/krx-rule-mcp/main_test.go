package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	mcpserver "github.com/chromato99/krx-rule-mcp/internal/mcp"
	"github.com/chromato99/krx-rule-mcp/internal/model"
	"gopkg.in/yaml.v3"
)

func TestValidateExpectedGeneration(t *testing.T) {
	validBytes := sha256.Sum256([]byte("release"))
	valid := hex.EncodeToString(validBytes[:])
	for _, value := range []string{"", valid} {
		if err := validateExpectedGeneration(value); err != nil {
			t.Fatalf("validateExpectedGeneration(%q): %v", value, err)
		}
	}
	for _, value := range []string{"change-me", "ABCDEF" + valid[6:], valid[:63]} {
		if err := validateExpectedGeneration(value); err == nil {
			t.Fatalf("expected invalid generation %q to fail", value)
		}
	}
}

func TestSearchSlots(t *testing.T) {
	if searchSlots(0) != nil {
		t.Fatal("zero concurrency should leave the service-local limiter disabled")
	}
	if slots := searchSlots(3); cap(slots) != 3 {
		t.Fatalf("slot capacity = %d", cap(slots))
	}
}

func TestResolveVectorPolicy(t *testing.T) {
	tests := []struct {
		enabled bool
		policy  string
		force   bool
		want    bool
		wantErr bool
	}{
		{enabled: false, policy: "optional", want: false},
		{enabled: false, policy: "required", want: false},
		{enabled: true, policy: "optional", want: false},
		{enabled: true, policy: "required", want: true},
		{enabled: true, policy: "optional", force: true, want: true},
		{enabled: false, policy: "optional", force: true, wantErr: true},
		{enabled: true, policy: "unknown", wantErr: true},
	}
	for _, test := range tests {
		got, err := resolveVectorPolicy(test.enabled, test.policy, test.force)
		if (err != nil) != test.wantErr || got != test.want {
			t.Fatalf("resolveVectorPolicy(%v, %q, %v) = (%v, %v)", test.enabled, test.policy, test.force, got, err)
		}
	}
}

func TestDisabledVectorModeDoesNotReadMalformedSnapshot(t *testing.T) {
	dataRoot, indexPath := writeRuntimeTestCorpusAndIndex(t)
	vectorPath := filepath.Join(t.TempDir(), "malformed.krxvec")
	if err := os.WriteFile(vectorPath, []byte("not a vector snapshot"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, err := searchindex.LoadRepositoryWithOptions(dataRoot, indexPath, searchindex.RepositoryLoadOptions{
		VectorEnabled:    false,
		VectorIndexPaths: []string{vectorPath},
	})
	if err != nil {
		t.Fatalf("disabled vector mode read malformed file: %v", err)
	}
	if repo.Engine.HasVectors() || len(repo.VectorIndexes) != 0 {
		t.Fatalf("disabled vector mode loaded vector state: %#v", repo.VectorIndexes)
	}
	if _, err := searchindex.LoadRepositoryWithOptions(dataRoot, indexPath, searchindex.RepositoryLoadOptions{
		VectorEnabled:    true,
		RequireVector:    true,
		VectorIndexPaths: []string{vectorPath},
	}); err == nil {
		t.Fatal("required vector mode accepted malformed snapshot")
	}
}

func TestReleaseGenerationBindsCanonicalDescriptor(t *testing.T) {
	dataRoot, indexPath := writeRuntimeTestCorpusAndIndex(t)
	repo, err := searchindex.LoadRepositoryWithOptions(dataRoot, indexPath, searchindex.RepositoryLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	lexiconPath := filepath.Join("..", "..", "config", "domain-lexicon.yaml")
	_, lexiconDigest, err := searchindex.LoadDomainLexiconWithDigest(lexiconPath)
	if err != nil {
		t.Fatal(err)
	}
	first, descriptor, err := inspectArtifacts(repo, lexiconDigest, "bm25", "sha256:image-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	second, _, err := inspectArtifacts(repo, lexiconDigest, "bm25", "sha256:image-a")
	if err != nil {
		t.Fatal(err)
	}
	changed, _, err := inspectArtifacts(repo, lexiconDigest, "bm25", "sha256:image-b")
	if err != nil {
		t.Fatal(err)
	}
	if first.ReleaseGeneration != second.ReleaseGeneration || first.ReleaseGeneration == changed.ReleaseGeneration {
		t.Fatalf("release generation is not deterministic or image-bound: first=%s second=%s changed=%s", first.ReleaseGeneration, second.ReleaseGeneration, changed.ReleaseGeneration)
	}
	if descriptor.CorpusReleaseHash == "" || descriptor.CorpusReleaseHash != repo.CorpusReleaseHash || descriptor.IndexSourceHash == "" || descriptor.IndexBuildHash == "" || descriptor.DomainLexiconDigest == "" || descriptor.RuntimeVectorMode != "bm25" {
		t.Fatalf("canonical descriptor is incomplete: %#v", descriptor)
	}
}

func TestReadinessRequiresExpectedReleaseGeneration(t *testing.T) {
	repo := &searchindex.Repository{Documents: map[string]model.Document{"rule-1": {ID: "rule-1"}}}
	config := httpRuntimeConfig{
		ExpectedGeneration: "expected",
		Artifacts:          artifactRuntime{ReleaseGeneration: "actual"},
	}
	recorder := httptest.NewRecorder()
	readinessHandler(config, repo).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("mismatched readiness status = %d", recorder.Code)
	}
	config.ExpectedGeneration = "actual"
	recorder = httptest.NewRecorder()
	readinessHandler(config, repo).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "release_generation=actual") {
		t.Fatalf("matching readiness response = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestStatelessHTTPAcceptsIndependentInitializeAndToolRequests(t *testing.T) {
	repo := &searchindex.Repository{
		Documents:   map[string]model.Document{},
		Attachments: map[string]searchindex.AttachmentDocument{},
		Engine:      searchindex.BuildWithAttachments(nil, nil, nil),
	}
	server := mcpserver.NewServer(&mcpserver.Service{Repo: repo}, "test")
	handler := statelessMCPHandler(server, nil)
	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("MCP-Protocol-Version", "2025-11-25")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	initialized := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if initialized.Code != http.StatusOK || !strings.Contains(initialized.Body.String(), "not an authoritative") || !strings.Contains(initialized.Body.String(), "official KRX") {
		t.Fatalf("stateless initialize response = %d headers=%v body=%s", initialized.Code, initialized.Header(), initialized.Body.String())
	}
	called := post(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_categories","arguments":{}}}`)
	if called.Code != http.StatusOK || strings.Contains(strings.ToLower(called.Body.String()), "session") || !strings.Contains(called.Body.String(), "categories") {
		t.Fatalf("independent stateless tool response = %d body=%s", called.Code, called.Body.String())
	}
	var response struct {
		Result struct {
			Content           []json.RawMessage `json:"content"`
			StructuredContent json.RawMessage   `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(called.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode JSON MCP response: %v; body=%s", err, called.Body.String())
	}
	if len(response.Result.Content) != 0 || len(response.Result.StructuredContent) == 0 {
		t.Fatalf("typed output must be carried once as structuredContent: %s", called.Body.String())
	}
	if strings.Count(called.Body.String(), `"categories"`) != 1 {
		t.Fatalf("structured output was duplicated on the wire: %s", called.Body.String())
	}
}

func TestResponseSizeLimitBoundsCompleteTypedMCPWireResponse(t *testing.T) {
	doc := model.Document{
		ID:           "rule-long",
		Title:        "긴 규정",
		SourceURL:    "https://example.test/rule",
		CollectedAt:  time.Now().UTC(),
		Language:     model.LanguageKorean,
		DocumentType: model.DocumentTypeRule,
		Body:         strings.Repeat("가", 4000),
	}
	repo := &searchindex.Repository{
		Documents:   map[string]model.Document{doc.ID: doc},
		Attachments: map[string]searchindex.AttachmentDocument{},
		Engine:      searchindex.BuildWithAttachments([]model.Document{doc}, nil, nil),
	}
	server := mcpserver.NewServer(&mcpserver.Service{Repo: repo, MaxToolOutputBytes: 64 << 10}, "test")
	const responseLimit = 1024
	handler := withResponseSizeLimit(responseLimit, statelessMCPHandler(server, nil))
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_rule","arguments":{"id":"rule-long","max_chars":4000}}}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("oversize typed MCP response status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.Len() > responseLimit || !strings.Contains(recorder.Body.String(), "response size limit") {
		t.Fatalf("wire response was not safely bounded: bytes=%d body=%q", recorder.Body.Len(), recorder.Body.String())
	}
}

func TestResponseSizeLimitPreservesAllowedStatusBodyAndHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Test", "preserved")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("within-limit"))
	})
	recorder := httptest.NewRecorder()
	withResponseSizeLimit(32, next).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if recorder.Code != http.StatusCreated || recorder.Body.String() != "within-limit" || recorder.Header().Get("X-Test") != "preserved" {
		t.Fatalf("bounded response changed allowed output: status=%d headers=%v body=%q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

func writeRuntimeTestCorpusAndIndex(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	doc := model.Document{
		ID:           "rule-1",
		Title:        "상장규정",
		SourceURL:    "https://example.test/rule",
		CollectedAt:  time.Now().UTC(),
		BodyHash:     model.HashText("상장 심사"),
		ContentHash:  model.HashText("상장규정\n상장 심사"),
		Language:     model.LanguageKorean,
		DocumentType: model.DocumentTypeRule,
		Body:         "상장 심사",
	}
	path := filepath.Join(root, "ko", "rules", model.Slug(doc.Title), "index.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := doc
	meta.Body = ""
	var content bytes.Buffer
	content.WriteString("---\n")
	encoder := yaml.NewEncoder(&content)
	encoder.SetIndent(2)
	if err := encoder.Encode(meta); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	content.WriteString("---\n\n")
	content.WriteString(strings.TrimSpace(doc.Body))
	content.WriteByte('\n')
	if err := os.WriteFile(path, content.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := searchindex.BuildSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	manifestIdentity := map[string]any{
		"index_source_hash": snapshot.IndexSourceHash,
		"schema_version":    2,
	}
	canonicalIdentity, err := json.Marshal(manifestIdentity)
	if err != nil {
		t.Fatal(err)
	}
	manifestIdentity["release_hash"] = model.HashBytes(canonicalIdentity)
	manifestJSON, err := json.Marshal(manifestIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), manifestJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err = searchindex.BuildSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(t.TempDir(), "bm25.krxidx")
	if err := searchindex.WriteSnapshot(indexPath, snapshot); err != nil {
		t.Fatal(err)
	}
	return root, indexPath
}
