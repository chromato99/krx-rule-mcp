package index

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
)

func TestOpenAIEmbedderAppliesInputPrefix(t *testing.T) {
	var captured struct {
		Model      string   `json:"model"`
		Input      []string `json:"input"`
		Dimensions int      `json:"dimensions"`
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"index":0,"embedding":[1,0]}]}`)),
		}, nil
	})}

	embedder := &OpenAIEmbedder{
		BaseURL:     "http://embedding.test/v1",
		APIKey:      "local",
		Model:       "intfloat/multilingual-e5-small",
		Dimensions:  2,
		InputPrefix: "query: ",
		Client:      client,
	}
	vectors, err := embedder.Embed(t.Context(), []string{"상장 심사"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vectors) != 1 || len(vectors[0]) != 2 {
		t.Fatalf("vectors = %#v", vectors)
	}
	if captured.Model != "intfloat/multilingual-e5-small" {
		t.Fatalf("model = %q", captured.Model)
	}
	if captured.Dimensions != 2 {
		t.Fatalf("dimensions = %d", captured.Dimensions)
	}
	if len(captured.Input) != 1 || captured.Input[0] != "query: 상장 심사" {
		t.Fatalf("input = %#v", captured.Input)
	}
}

func TestOpenAIEmbedderRejectsMalformedResponse(t *testing.T) {
	tests := []struct {
		name       string
		input      []string
		response   string
		dimensions int
	}{
		{name: "missing row", input: []string{"a", "b"}, response: `{"data":[{"index":0,"embedding":[1,0]}]}`, dimensions: 2},
		{name: "duplicate index", input: []string{"a", "b"}, response: `{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[0,1]}]}`, dimensions: 2},
		{name: "invalid index", input: []string{"a"}, response: `{"data":[{"index":1,"embedding":[1,0]}]}`, dimensions: 2},
		{name: "wrong dimensions", input: []string{"a"}, response: `{"data":[{"index":0,"embedding":[1]}]}`, dimensions: 2},
		{name: "float32 overflow", input: []string{"a"}, response: `{"data":[{"index":0,"embedding":[1e100,0]}]}`, dimensions: 2},
		{name: "zero norm", input: []string{"a"}, response: `{"data":[{"index":0,"embedding":[0,0]}]}`, dimensions: 2},
		{name: "float32 underflow to zero", input: []string{"a"}, response: `{"data":[{"index":0,"embedding":[5e-324,0]}]}`, dimensions: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(tc.response)),
				}, nil
			})}
			embedder := &OpenAIEmbedder{BaseURL: "http://embedding.test/v1", Model: "test", Dimensions: tc.dimensions, Client: client}
			if _, err := embedder.Embed(t.Context(), tc.input); err == nil {
				t.Fatal("Embed accepted malformed response")
			}
		})
	}
}

func TestEmbedSnapshotChunksRejectsInvalidVectors(t *testing.T) {
	tests := []struct {
		name    string
		chunks  []SnapshotChunk
		vectors [][]float64
	}{
		{name: "count", chunks: []SnapshotChunk{{ID: "a", Text: "a"}}, vectors: nil},
		{name: "dimensions", chunks: []SnapshotChunk{{ID: "a", Text: "a"}}, vectors: [][]float64{{1}}},
		{name: "nan", chunks: []SnapshotChunk{{ID: "a", Text: "a"}}, vectors: [][]float64{{math.NaN(), 0}}},
		{name: "overflow", chunks: []SnapshotChunk{{ID: "a", Text: "a"}}, vectors: [][]float64{{1e100, 0}}},
		{name: "zero norm", chunks: []SnapshotChunk{{ID: "a", Text: "a"}}, vectors: [][]float64{{0, 0}}},
		{name: "float32 underflow to zero", chunks: []SnapshotChunk{{ID: "a", Text: "a"}}, vectors: [][]float64{{math.SmallestNonzeroFloat64, 0}}},
		{name: "duplicate chunk", chunks: []SnapshotChunk{{ID: "a", Text: "a"}, {ID: "a", Text: "b"}}, vectors: [][]float64{{1, 0}, {0, 1}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			embedder := staticEmbedder{vectors: tc.vectors, dimensions: 2}
			if _, err := EmbedSnapshotChunks(t.Context(), tc.chunks, embedder); err == nil {
				t.Fatal("EmbedSnapshotChunks accepted invalid vectors")
			}
		})
	}
}

func TestEnvDefaultPreserveSpaceAllowsExplicitEmpty(t *testing.T) {
	t.Setenv("KRX_TEST_PREFIX", "")
	if got := envDefaultPreserveSpace("KRX_TEST_PREFIX", "query: "); got != "" {
		t.Fatalf("prefix = %q, want empty", got)
	}
	if got := envDefaultPreserveSpace("KRX_TEST_PREFIX_UNSET", "query: "); got != "query: " {
		t.Fatalf("fallback prefix = %q", got)
	}
}

func TestVectorMetadataRoundTrip(t *testing.T) {
	path := t.TempDir() + "/vectors.krxvec.meta.json"
	want := VectorMetadata{
		IndexSourceHash: "source",
		IndexBuildHash:  "build",
		CorpusHash:      "source",
		Model:           "intfloat/multilingual-e5-small",
		Dimensions:      384,
		QueryPrefix:     "query: ",
		DocumentPrefix:  "passage: ",
		Scope:           VectorScopeSample,
	}
	if err := WriteVectorMetadata(path, want); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	got, err := LoadVectorMetadata(path)
	if err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	if got.Version != VectorMetadataFormatVersion || got.GeneratedAt == "" {
		t.Fatalf("metadata version/time = %#v", got)
	}
	got.GeneratedAt = ""
	want.Version = VectorMetadataFormatVersion
	if got != want {
		t.Fatalf("metadata = %#v, want %#v", got, want)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type staticEmbedder struct {
	vectors    [][]float64
	dimensions int
}

func (e staticEmbedder) Embed(context.Context, []string) ([][]float64, error) {
	return e.vectors, nil
}

func (e staticEmbedder) EmbeddingInfo() (string, int) {
	return "test", e.dimensions
}
