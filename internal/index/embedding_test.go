package index

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIEmbedderAppliesInputPrefix(t *testing.T) {
	var captured struct {
		Model      string   `json:"model"`
		Input      []string `json:"input"`
		Dimensions int      `json:"dimensions"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1,0]}]}`))
	}))
	defer server.Close()

	embedder := &OpenAIEmbedder{
		BaseURL:     server.URL + "/v1",
		APIKey:      "local",
		Model:       "intfloat/multilingual-e5-small",
		Dimensions:  384,
		InputPrefix: "query: ",
		Client:      server.Client(),
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
	if captured.Dimensions != 384 {
		t.Fatalf("dimensions = %d", captured.Dimensions)
	}
	if len(captured.Input) != 1 || captured.Input[0] != "query: 상장 심사" {
		t.Fatalf("input = %#v", captured.Input)
	}
}

func TestVectorMetadataRoundTrip(t *testing.T) {
	path := t.TempDir() + "/vectors.krxvec.meta.json"
	want := VectorMetadata{
		CorpusHash:     "corpus",
		Model:          "intfloat/multilingual-e5-small",
		Dimensions:     384,
		QueryPrefix:    "query: ",
		DocumentPrefix: "passage: ",
	}
	if err := WriteVectorMetadata(path, want); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	got, err := LoadVectorMetadata(path)
	if err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	if got.Version != 1 || got.GeneratedAt == "" {
		t.Fatalf("metadata version/time = %#v", got)
	}
	got.GeneratedAt = ""
	want.Version = 1
	if got != want {
		t.Fatalf("metadata = %#v, want %#v", got, want)
	}
}
