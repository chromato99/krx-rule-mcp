package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	"github.com/chromato99/krx-rule-mcp/internal/model"
	"strings"
	"testing"
)

func TestCallerReceivesCandidatesForFalsePremisesAndIncompleteScope(t *testing.T) {
	docs := []model.Document{
		{ID: "a", Title: "시장 가 납부규정", Category: "시장 가", Language: "ko", DocumentType: model.DocumentTypeRule, Body: "## 제2조(추가 납부)\n추가 납부 통지를 받은 회원은 다음 영업일 14시까지 납부하여야 한다. 다만 휴장일에는 다음 영업일에 납부한다."},
		{ID: "b", Title: "시장 나 납부규정", Category: "시장 나", Language: "ko", DocumentType: model.DocumentTypeRule, Body: "## 제7조(추가 납부)\n추가 납부 통지를 받은 회원은 다음 영업일 16시까지 납부하여야 한다."},
	}
	repo := &searchindex.Repository{IndexerVersion: searchindex.IndexerVersion, Documents: map[string]model.Document{}, Engine: buildMCPTestEngine(docs, nil, nil)}
	for _, doc := range docs {
		repo.Documents[doc.ID] = doc
	}
	service := &Service{Repo: repo}
	for _, query := range []string{"추가 납부는 모든 시장에서 무조건 9시까지 하면 되지?", "추가 납부 기한이 언제인가", "회원 납부 의무와 운전면허 갱신"} {
		for _, limit := range []int{0, 1, 5, 50} {
			t.Run(fmt.Sprintf("%s/%d", query, limit), func(t *testing.T) {
				out, err := service.SearchRules(context.Background(), SearchRulesInput{Query: query, Language: "ko", Limit: limit})
				if err != nil {
					t.Fatal(err)
				}
				bound := limit
				if bound == 0 {
					bound = 10
				}
				if len(out.Results) == 0 || len(out.Results) > bound || out.Retrieval.Status != RetrievalCandidatesFound || out.Retrieval.ReturnedResults != len(out.Results) {
					t.Fatalf("candidates hidden or unbounded: %#v", out)
				}
				raw, err := json.Marshal(out)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err = json.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"answerable", "answerability", "candidates"} {
					if fields[name] != nil {
						t.Fatalf("obsolete decision/internal trace leaked: %s", raw)
					}
				}
				if out.ContractVersion != SearchContractVersion || !strings.Contains(out.ScoreNote, "calling LLM") {
					t.Fatal("caller responsibility missing")
				}
				for _, result := range out.Results {
					for _, match := range result.EvidenceMatches {
						zero := 0
						value, err := service.GetContext(context.Background(), GetContextInput{ChunkID: match.ChunkID, BeforeChunks: &zero, AfterChunks: &zero})
						if err != nil {
							t.Fatal(err)
						}
						if value.Document.ID != result.ID || value.Truncated || len(value.Chunks) != 1 || value.Chunks[0].ArticleID != match.ArticleID || !strings.Contains(value.Content, "납부") {
							t.Fatalf("caller cannot inspect original candidate: %#v", value)
						}
					}
				}
			})
		}
	}
	// The caller can resolve scope with an explicit filter in a second search.
	out, err := service.SearchRules(context.Background(), SearchRulesInput{Query: "추가 납부 기한", Category: "시장 나", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].ID != "b" {
		t.Fatalf("scope refinement leaked results: %#v", out)
	}
	// A valid but unmatched filter is an empty retrieval, not a claim about all rules.
	out, err = service.SearchRules(context.Background(), SearchRulesInput{Query: "추가 납부", Category: "없는 분류"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Retrieval.Status != RetrievalNoCandidates || out.Retrieval.ReturnedResults != 0 || len(out.Results) != 0 {
		t.Fatalf("empty retrieval contract: %#v", out)
	}
}

func TestSearchRequiresAvailableCompatibleRepository(t *testing.T) {
	for _, repo := range []*searchindex.Repository{nil, {}, {IndexerVersion: searchindex.IndexerVersion}} {
		_, err := (&Service{Repo: repo}).SearchRules(context.Background(), SearchRulesInput{Query: "납부 기한"})
		if err == nil || !strings.Contains(err.Error(), "retrieval contract mismatch") {
			t.Fatalf("bad repository did not fail as tool error: %v", err)
		}
	}
}

func TestEvaluatorEntryPointsEnforcePublicOutputLimit(t *testing.T) {
	doc := model.Document{ID: "bounded", Title: "납부 규정", DocumentType: model.DocumentTypeRule, Body: "## 제1조\n회원은 증거금을 납부하여야 한다."}
	service := &Service{Repo: testRepository(doc, nil), MaxToolOutputBytes: 64}
	if _, err := service.SearchRules(context.Background(), SearchRulesInput{Query: "증거금 납부"}); err == nil || !strings.Contains(err.Error(), "structured output limit") {
		t.Fatalf("offline search bypassed public output limit: %v", err)
	}
	if _, err := service.GetContext(context.Background(), GetContextInput{ChunkID: "bounded#0"}); err == nil || !strings.Contains(err.Error(), "structured output limit") {
		t.Fatalf("offline context bypassed public output limit: %v", err)
	}
}
