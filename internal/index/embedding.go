package index

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type Embedder interface {
	Embed(context.Context, []string) ([][]float64, error)
}

type EmbedderInfo interface {
	EmbeddingInfo() (model string, dimensions int)
}

type OpenAIEmbedder struct {
	BaseURL     string
	APIKey      string
	Model       string
	Dimensions  int
	InputPrefix string
	Client      *http.Client
}

func NewEmbedderFromEnv() (*OpenAIEmbedder, bool, error) {
	if !envBool("KRX_VECTOR_SEARCH_ENABLED") {
		return nil, false, nil
	}
	embedder, err := NewQueryEmbedderFromEnv()
	return embedder, true, err
}

func NewQueryEmbedderFromEnv() (*OpenAIEmbedder, error) {
	embedder, err := newOpenAIEmbedderFromEnv()
	if err != nil {
		return nil, err
	}
	embedder.InputPrefix = envDefaultPreserveSpace("KRX_EMBEDDING_QUERY_PREFIX", "query: ")
	return embedder, nil
}

func NewDocumentEmbedderFromEnv() (*OpenAIEmbedder, error) {
	embedder, err := newOpenAIEmbedderFromEnv()
	if err != nil {
		return nil, err
	}
	embedder.InputPrefix = envDefaultPreserveSpace("KRX_EMBEDDING_DOCUMENT_PREFIX", "passage: ")
	return embedder, nil
}

func newOpenAIEmbedderFromEnv() (*OpenAIEmbedder, error) {
	dims := 384
	if raw := os.Getenv("KRX_EMBEDDING_DIMENSIONS"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("KRX_EMBEDDING_DIMENSIONS must be a positive integer")
		}
		dims = parsed
	}
	return &OpenAIEmbedder{
		BaseURL:    strings.TrimRight(envDefault("KRX_EMBEDDING_BASE_URL", "http://127.0.0.1:18081/v1"), "/"),
		APIKey:     envDefault("OPENAI_API_KEY", "local"),
		Model:      envDefault("KRX_EMBEDDING_MODEL", "intfloat/multilingual-e5-small"),
		Dimensions: dims,
		Client:     &http.Client{Timeout: 45 * time.Second},
	}, nil
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, input []string) ([][]float64, error) {
	if len(input) == 0 {
		return nil, nil
	}
	prefixed := make([]string, len(input))
	for i, text := range input {
		prefixed[i] = e.InputPrefix + text
	}
	body := map[string]any{
		"model": e.Model,
		"input": prefixed,
	}
	if e.Dimensions > 0 {
		body["dimensions"] = e.Dimensions
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.BaseURL+"/embeddings", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding API returned %s", resp.Status)
	}
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	vectors := make([][]float64, len(input))
	for _, row := range out.Data {
		if row.Index >= 0 && row.Index < len(vectors) {
			vectors[row.Index] = row.Embedding
		}
	}
	return vectors, nil
}

func (e *OpenAIEmbedder) EmbeddingInfo() (string, int) {
	return e.Model, e.Dimensions
}

func EmbedChunks(chunks []chunk, embedder Embedder) (map[string][]float64, error) {
	ctx := context.Background()
	snapshotChunks := make([]SnapshotChunk, 0, len(chunks))
	for _, c := range chunks {
		snapshotChunks = append(snapshotChunks, SnapshotChunk{ID: c.ID, Text: c.Text})
	}
	return EmbedSnapshotChunks(ctx, snapshotChunks, embedder)
}

func EmbedSnapshotChunks(ctx context.Context, chunks []SnapshotChunk, embedder Embedder) (map[string][]float64, error) {
	out := map[string][]float64{}
	const batchSize = 32
	for start := 0; start < len(chunks); start += batchSize {
		end := start + batchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		inputs := make([]string, 0, end-start)
		ids := make([]string, 0, end-start)
		for _, c := range chunks[start:end] {
			inputs = append(inputs, c.Text)
			ids = append(ids, c.ID)
		}
		vectors, err := embedder.Embed(ctx, inputs)
		if err != nil {
			return out, err
		}
		for i, vec := range vectors {
			if len(vec) > 0 {
				out[ids[i]] = vec
			}
		}
	}
	return out, nil
}

func envDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envDefaultPreserveSpace(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	return value
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

func (e *OpenAIEmbedder) client() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return &http.Client{Timeout: 45 * time.Second}
}
