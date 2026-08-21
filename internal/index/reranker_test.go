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

func TestTEIRerankerValidatesAndMapsCompleteResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/rerank" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("unexpected request path/auth: %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var request struct {
			Query      string   `json:"query"`
			Texts      []string `json:"texts"`
			Truncate   bool     `json:"truncate"`
			RawScores  bool     `json:"raw_scores"`
			ReturnText bool     `json:"return_text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Query != "질의" || len(request.Texts) != 2 || !request.Truncate || !request.RawScores || request.ReturnText {
			t.Fatalf("unexpected request: %#v", request)
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[{"index":1,"score":4.5},{"index":0,"score":-1.25}]`))}, nil
	})}

	reranker := &TEIReranker{BaseURL: "http://reranker.test", APIKey: "secret", Client: client}
	scores, err := reranker.Rerank(context.Background(), "질의", []string{"가", "나"})
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 2 || scores[0].Index != 1 || scores[0].Score != 4.5 || scores[1].Index != 0 {
		t.Fatalf("scores = %#v", scores)
	}
}

func TestTEIRerankerRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing", body: `[{"index":0,"score":1}]`},
		{name: "duplicate", body: `[{"index":0,"score":1},{"index":0,"score":2}]`},
		{name: "bad index", body: `[{"index":0,"score":1},{"index":2,"score":2}]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			_, err := (&TEIReranker{BaseURL: "http://reranker.test", Client: client}).Rerank(context.Background(), "질의", []string{"가", "나"})
			if err == nil {
				t.Fatalf("invalid response accepted: %s", test.body)
			}
		})
	}
}

func TestTEIRerankerVerifiesImmutableIdentity(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/info" {
			t.Fatalf("unexpected info request: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"model_id":"model","model_sha":"revision"}`))}, nil
	})}
	reranker := &TEIReranker{BaseURL: "http://reranker.test", Model: "model", ModelRevision: "revision", Client: client}
	if err := reranker.VerifyReranker(context.Background()); err != nil {
		t.Fatal(err)
	}
	reranker.ModelRevision = "other"
	if err := reranker.VerifyReranker(context.Background()); err == nil {
		t.Fatal("mismatched reranker revision accepted")
	}
}

func TestTEIRerankerBatchesPassagesAndRestoresGlobalIndexes(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var request struct {
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		rows := make([]map[string]any, len(request.Texts))
		for index := range request.Texts {
			rows[index] = map[string]any{"index": index, "score": float64(index)}
		}
		encoded, _ := json.Marshal(rows)
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(encoded)))}, nil
	})}
	reranker := &TEIReranker{BaseURL: "http://reranker.test", BatchSize: 2, Client: client}
	scores, err := reranker.Rerank(context.Background(), "질의", []string{"0", "1", "2", "3", "4"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(scores) != 5 || scores[4].Index != 4 {
		t.Fatalf("batched scores calls=%d scores=%#v", calls, scores)
	}
}

func TestApplyRerankScoresUsesRankFusionAndIdentifierProtection(t *testing.T) {
	set := SearchCandidateSet{Chunks: []ChunkCandidate{
		{ChunkID: "semantic", Text: "UPI를 사용한다", Score: 0.03, FusedScore: 0.03, BaselineRank: 1, FinalRank: 1},
		{ChunkID: "exact", Text: "UTI 거래고유식별기호를 포함한다", Score: 0.02, FusedScore: 0.02, BaselineRank: 2, FinalRank: 2},
		{ChunkID: "tail", Text: "일반 조문", Score: 0.01, FusedScore: 0.01, BaselineRank: 3, FinalRank: 3},
	}}
	err := ApplyRerankScores(&set, []RerankScore{{Index: 0, Score: 10}, {Index: 1, Score: -10}}, 2, [][]string{{"UTI", "거래고유식별기호"}})
	if err != nil {
		t.Fatal(err)
	}
	if set.Chunks[0].ChunkID != "exact" || set.Chunks[0].RerankerRank != 1 || set.Chunks[0].FinalRank != 1 {
		t.Fatalf("protected exact identifier was not promoted: %#v", set.Chunks)
	}
	if set.Chunks[2].Reranked || set.Chunks[2].RerankerRank != 0 {
		t.Fatalf("tail candidate was marked reranked: %#v", set.Chunks[2])
	}
	if err := ApplyRerankScores(&set, []RerankScore{{Index: 0, Score: math.NaN()}, {Index: 1, Score: 0}}, 2, nil); err == nil {
		t.Fatal("non-finite reranker score accepted")
	}
}
