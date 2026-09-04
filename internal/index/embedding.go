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

	"github.com/chromato99/krx-rule-mcp/internal/model"
)

type Embedder interface {
	Embed(context.Context, []string) ([][]float64, error)
}

type EmbedderInfo interface {
	EmbeddingInfo() (model string, dimensions int)
}

type EmbeddingInputFormat string

const (
	EmbeddingInputTextV1           EmbeddingInputFormat = "text-v1"
	EmbeddingInputStructuredV1     EmbeddingInputFormat = "structured-v1"
	DefaultEmbeddingInputFormat                         = EmbeddingInputTextV1
	DefaultEmbeddingModel                               = "intfloat/multilingual-e5-small"
	DefaultEmbeddingModelRevision                       = "614241f622f53c4eeff9890bdc4f31cfecc418b3"
	DefaultEmbeddingDimensions                          = 384
	DefaultEmbeddingQueryPrefix                         = "query: "
	DefaultEmbeddingDocumentPrefix                      = "passage: "
)

func ParseEmbeddingInputFormat(value string) (EmbeddingInputFormat, error) {
	format := EmbeddingInputFormat(strings.TrimSpace(value))
	if format == "" {
		return DefaultEmbeddingInputFormat, nil
	}
	switch format {
	case EmbeddingInputTextV1, EmbeddingInputStructuredV1:
		return format, nil
	default:
		return "", fmt.Errorf("unsupported embedding input format %q", value)
	}
}

// PrepareEmbeddingChunks builds the text sent to the document embedder without
// changing the canonical chunk text stored in the BM25/vector snapshots.
func PrepareEmbeddingChunks(chunks []SnapshotChunk, documents []model.Document, format EmbeddingInputFormat) ([]SnapshotChunk, error) {
	if format == EmbeddingInputTextV1 {
		return chunks, nil
	}
	if format != EmbeddingInputStructuredV1 {
		return nil, fmt.Errorf("unsupported embedding input format %q", format)
	}
	byID := make(map[string]model.Document, len(documents))
	for _, document := range documents {
		byID[document.ID] = document
	}
	prepared := make([]SnapshotChunk, len(chunks))
	for index, chunk := range chunks {
		document, ok := byID[chunk.DocID]
		if !ok {
			return nil, fmt.Errorf("embedding chunk %q references unknown document %q", chunk.ID, chunk.DocID)
		}
		prepared[index] = chunk
		prepared[index].Text = structuredEmbeddingText(document, chunk)
	}
	return prepared, nil
}

func structuredEmbeddingText(document model.Document, chunk SnapshotChunk) string {
	fields := make([]string, 0, 8)
	fields = append(fields, "document: "+document.Title)
	if document.Category != "" {
		fields = append(fields, "category: "+document.Category)
	}
	if document.Language != "" {
		fields = append(fields, "language: "+document.Language)
	}
	source := chunk.Source
	if chunk.AttachmentTitle != "" {
		source += " / " + chunk.AttachmentTitle
	}
	fields = append(fields, "source: "+source)
	if chunk.ArticleID != "" {
		fields = append(fields, "article: "+chunk.ArticleID)
	}
	if len(chunk.HeadingPath) > 0 {
		fields = append(fields, "path: "+strings.Join(chunk.HeadingPath, " > "))
	}
	fields = append(fields, "text:\n"+chunk.Text)
	return strings.Join(fields, "\n")
}

type OpenAIEmbedder struct {
	BaseURL       string
	APIKey        string
	Model         string
	ModelRevision string
	Dimensions    int
	InputPrefix   string
	Client        *http.Client
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
	embedder.InputPrefix = EmbeddingQueryPrefixFromEnv(embedder.Model)
	return embedder, nil
}

func NewDocumentEmbedderFromEnv() (*OpenAIEmbedder, error) {
	embedder, err := newOpenAIEmbedderFromEnv()
	if err != nil {
		return nil, err
	}
	embedder.InputPrefix = EmbeddingDocumentPrefixFromEnv(embedder.Model)
	return embedder, nil
}

func newOpenAIEmbedderFromEnv() (*OpenAIEmbedder, error) {
	modelName := envDefault("KRX_EMBEDDING_MODEL", DefaultEmbeddingModel)
	dims := 0
	if modelName == DefaultEmbeddingModel {
		dims = DefaultEmbeddingDimensions
	}
	if raw := strings.TrimSpace(os.Getenv("KRX_EMBEDDING_DIMENSIONS")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("KRX_EMBEDDING_DIMENSIONS must be a positive integer")
		}
		dims = parsed
	}
	if dims == 0 {
		return nil, fmt.Errorf("KRX_EMBEDDING_DIMENSIONS is required for non-default embedding model %q", modelName)
	}
	modelRevision := ""
	if modelName == DefaultEmbeddingModel {
		modelRevision = DefaultEmbeddingModelRevision
	}
	if configured, ok := os.LookupEnv("KRX_EMBEDDING_MODEL_REVISION"); ok {
		modelRevision = strings.TrimSpace(configured)
	}
	return &OpenAIEmbedder{
		BaseURL:       strings.TrimRight(envDefault("KRX_EMBEDDING_BASE_URL", "http://127.0.0.1:18081/v1"), "/"),
		APIKey:        envDefault("OPENAI_API_KEY", "local"),
		Model:         modelName,
		ModelRevision: modelRevision,
		Dimensions:    dims,
		Client:        &http.Client{Timeout: 10 * time.Minute},
	}, nil
}

// EmbeddingQueryPrefixFromEnv returns the configured query transformation for
// the selected model. E5 prefixes are defaults for the maintained E5 profile,
// not global defaults for every OpenAI-compatible embedding model.
func EmbeddingQueryPrefixFromEnv(modelName string) string {
	fallback := ""
	if modelName == DefaultEmbeddingModel {
		fallback = DefaultEmbeddingQueryPrefix
	}
	return envDefaultPreserveSpace("KRX_EMBEDDING_QUERY_PREFIX", fallback)
}

// EmbeddingDocumentPrefixFromEnv returns the configured document
// transformation for the selected model without leaking E5 conventions into
// another embedding profile.
func EmbeddingDocumentPrefixFromEnv(modelName string) string {
	fallback := ""
	if modelName == DefaultEmbeddingModel {
		fallback = DefaultEmbeddingDocumentPrefix
	}
	return envDefaultPreserveSpace("KRX_EMBEDDING_DOCUMENT_PREFIX", fallback)
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
	seen := make(map[int]struct{}, len(out.Data))
	if len(out.Data) != len(input) {
		return nil, fmt.Errorf("embedding API returned %d vectors for %d inputs", len(out.Data), len(input))
	}
	for _, row := range out.Data {
		if row.Index < 0 || row.Index >= len(vectors) {
			return nil, fmt.Errorf("embedding API returned invalid index %d", row.Index)
		}
		if _, duplicate := seen[row.Index]; duplicate {
			return nil, fmt.Errorf("embedding API returned duplicate index %d", row.Index)
		}
		seen[row.Index] = struct{}{}
		if err := validateEmbeddingVector(row.Embedding, e.Dimensions); err != nil {
			return nil, fmt.Errorf("embedding API index %d: %w", row.Index, err)
		}
		vectors[row.Index] = row.Embedding
	}
	for index, vector := range vectors {
		if vector == nil {
			return nil, fmt.Errorf("embedding API omitted index %d", index)
		}
	}
	return vectors, nil
}

func (e *OpenAIEmbedder) EmbeddingInfo() (string, int) {
	return e.Model, e.Dimensions
}

func EmbedSnapshotChunks(ctx context.Context, chunks []SnapshotChunk, embedder Embedder) (map[string][]float64, error) {
	out := map[string][]float64{}
	expectedDimensions := 0
	if info, ok := embedder.(EmbedderInfo); ok {
		_, expectedDimensions = info.EmbeddingInfo()
	}
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
		if len(vectors) != len(inputs) {
			return out, fmt.Errorf("embedder returned %d vectors for %d inputs", len(vectors), len(inputs))
		}
		for i, vec := range vectors {
			if expectedDimensions == 0 {
				expectedDimensions = len(vec)
			}
			if err := validateEmbeddingVector(vec, expectedDimensions); err != nil {
				return out, fmt.Errorf("embed chunk %q: %w", ids[i], err)
			}
			if _, duplicate := out[ids[i]]; duplicate {
				return out, fmt.Errorf("duplicate chunk id %q in embedding input", ids[i])
			}
			out[ids[i]] = vec
		}
	}
	return out, nil
}

func validateEmbeddingVector(vector []float64, dimensions int) error {
	if dimensions <= 0 {
		return fmt.Errorf("embedding dimensions must be positive")
	}
	if len(vector) != dimensions {
		return fmt.Errorf("embedding dimensions=%d want=%d", len(vector), dimensions)
	}
	if err := validateFiniteNonZeroFloat32Vector(vector); err != nil {
		return fmt.Errorf("embedding %w", err)
	}
	return nil
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
	if !ok {
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
	return &http.Client{Timeout: 10 * time.Minute}
}
