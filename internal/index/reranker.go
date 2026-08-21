package index

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultRerankerModel          = "dragonkue/bge-reranker-v2-m3-ko"
	DefaultRerankerRevision       = "2aca5884ecac490192af9ebd86836d9073d826cd"
	DefaultRerankerCandidateLimit = 20
	DefaultRerankerBatchSize      = 4
)

// Reranker scores a bounded list of passages against one query. Scores are
// ranking signals only; callers must not treat them as answerability
// probabilities.
type Reranker interface {
	Rerank(context.Context, string, []string) ([]RerankScore, error)
}

type RerankerInfo interface {
	RerankingInfo() (model, revision string)
}

type RerankerVerifier interface {
	VerifyReranker(context.Context) error
}

type RerankScore struct {
	Index int
	Score float64
}

// TEIReranker calls the native Text Embeddings Inference /rerank endpoint.
type TEIReranker struct {
	BaseURL       string
	APIKey        string
	Model         string
	ModelRevision string
	BatchSize     int
	Client        *http.Client
}

func NewRerankerFromEnv() (*TEIReranker, bool, error) {
	if !envBool("KRX_RERANKER_ENABLED") {
		return nil, false, nil
	}
	reranker, err := NewTEIRerankerFromEnv()
	return reranker, true, err
}

func NewTEIRerankerFromEnv() (*TEIReranker, error) {
	baseURL := strings.TrimRight(envDefault("KRX_RERANKER_BASE_URL", "http://127.0.0.1:18082"), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("KRX_RERANKER_BASE_URL is required when reranking is enabled")
	}
	batchSize := DefaultRerankerBatchSize
	if raw := strings.TrimSpace(os.Getenv("KRX_RERANKER_BATCH_SIZE")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 32 {
			return nil, fmt.Errorf("KRX_RERANKER_BATCH_SIZE must be between 1 and 32")
		}
		batchSize = parsed
	}
	return &TEIReranker{
		BaseURL:       baseURL,
		APIKey:        envDefault("KRX_RERANKER_API_KEY", "local"),
		Model:         envDefault("KRX_RERANKER_MODEL", DefaultRerankerModel),
		ModelRevision: envDefault("KRX_RERANKER_MODEL_REVISION", DefaultRerankerRevision),
		BatchSize:     batchSize,
		Client:        &http.Client{Timeout: 31 * time.Minute},
	}, nil
}

func RerankerCandidateLimitFromEnv() (int, error) {
	raw := strings.TrimSpace(os.Getenv("KRX_RERANKER_CANDIDATE_LIMIT"))
	if raw == "" {
		return DefaultRerankerCandidateLimit, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || value > 128 {
		return 0, fmt.Errorf("KRX_RERANKER_CANDIDATE_LIMIT must be between 1 and 128")
	}
	return value, nil
}

func (r *TEIReranker) Rerank(ctx context.Context, query string, passages []string) ([]RerankScore, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("reranker query is required")
	}
	if len(passages) == 0 {
		return nil, nil
	}
	batchSize := r.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultRerankerBatchSize
	}
	out := make([]RerankScore, 0, len(passages))
	for start := 0; start < len(passages); start += batchSize {
		end := start + batchSize
		if end > len(passages) {
			end = len(passages)
		}
		batch, err := r.rerankBatch(ctx, query, passages[start:end])
		if err != nil {
			return nil, fmt.Errorf("rerank passages %d:%d: %w", start, end, err)
		}
		for _, score := range batch {
			score.Index += start
			out = append(out, score)
		}
	}
	return out, nil
}

func (r *TEIReranker) rerankBatch(ctx context.Context, query string, passages []string) ([]RerankScore, error) {
	body := struct {
		Query      string   `json:"query"`
		Texts      []string `json:"texts"`
		Truncate   bool     `json:"truncate"`
		RawScores  bool     `json:"raw_scores"`
		ReturnText bool     `json:"return_text"`
	}{
		Query: query, Texts: passages, Truncate: true, RawScores: true,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode reranker request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.BaseURL+"/rerank", bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("create reranker request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(r.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+r.APIKey)
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("reranker API returned %s", resp.Status)
	}
	var response []struct {
		Index int     `json:"index"`
		Score float64 `json:"score"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode reranker response: %w", err)
	}
	if len(response) != len(passages) {
		return nil, fmt.Errorf("reranker API returned %d scores for %d passages", len(response), len(passages))
	}
	seen := make(map[int]struct{}, len(response))
	out := make([]RerankScore, 0, len(response))
	for _, row := range response {
		if row.Index < 0 || row.Index >= len(passages) {
			return nil, fmt.Errorf("reranker API returned invalid index %d", row.Index)
		}
		if _, duplicate := seen[row.Index]; duplicate {
			return nil, fmt.Errorf("reranker API returned duplicate index %d", row.Index)
		}
		if math.IsNaN(row.Score) || math.IsInf(row.Score, 0) {
			return nil, fmt.Errorf("reranker API returned non-finite score for index %d", row.Index)
		}
		seen[row.Index] = struct{}{}
		out = append(out, RerankScore{Index: row.Index, Score: row.Score})
	}
	return out, nil
}

func (r *TEIReranker) RerankingInfo() (string, string) {
	return r.Model, r.ModelRevision
}

func (r *TEIReranker) VerifyReranker(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.BaseURL+"/info", nil)
	if err != nil {
		return fmt.Errorf("create reranker info request: %w", err)
	}
	if strings.TrimSpace(r.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+r.APIKey)
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("reranker info API returned %s", resp.Status)
	}
	var info struct {
		ModelID  string  `json:"model_id"`
		ModelSHA *string `json:"model_sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return fmt.Errorf("decode reranker info response: %w", err)
	}
	if info.ModelID != r.Model {
		return fmt.Errorf("reranker model id %q does not match configured model %q", info.ModelID, r.Model)
	}
	if r.ModelRevision != "" {
		if info.ModelSHA == nil || strings.TrimSpace(*info.ModelSHA) == "" {
			return fmt.Errorf("reranker did not report an immutable model revision")
		}
		if strings.TrimSpace(*info.ModelSHA) != r.ModelRevision {
			return fmt.Errorf("reranker model revision %q does not match configured revision %q", strings.TrimSpace(*info.ModelSHA), r.ModelRevision)
		}
	}
	return nil
}

func (r *TEIReranker) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return http.DefaultClient
}
