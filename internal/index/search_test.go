package index

import (
	"testing"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/model"
)

func TestBM25KoreanSearchAndFilter(t *testing.T) {
	docs := []model.Document{
		{
			ID:            "rule-1",
			Title:         "코스닥시장 상장규정",
			Category:      "코스닥시장규정",
			EffectiveDate: "2026-07-01",
			CollectedAt:   time.Now(),
			DocumentType:  model.DocumentTypeRule,
			Language:      model.LanguageKorean,
			Body:          "상장신청인은 신규상장 심사를 신청할 수 있다.",
		},
		{
			ID:            "notice-1",
			Title:         "파생상품시장 업무규정 시행세칙 개정 예고",
			PublishedDate: "2026-06-16",
			CollectedAt:   time.Now(),
			DocumentType:  model.DocumentTypeNotice,
			Language:      model.LanguageKorean,
			Body:          "외환거래 도입에 따른 조문 정비",
		},
	}
	engine := Build(docs, nil)
	results := engine.Search(SearchOptions{
		Query:  "상장 신청",
		Limit:  5,
		Filter: Filter{DocumentType: model.DocumentTypeRule},
	})
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1: %#v", len(results), results)
	}
	if results[0].ID != "rule-1" {
		t.Fatalf("unexpected top result: %#v", results[0])
	}
}

func TestSearchLanguageFilter(t *testing.T) {
	docs := []model.Document{
		{
			ID:           "rule-1",
			Title:        "코스닥시장 상장규정",
			CollectedAt:  time.Now(),
			DocumentType: model.DocumentTypeRule,
			Language:     model.LanguageKorean,
			Body:         "상장 심사",
		},
		{
			ID:           "rule-1-en",
			Title:        "KOSDAQ Market Listing Regulation",
			CollectedAt:  time.Now(),
			DocumentType: model.DocumentTypeRule,
			Language:     model.LanguageEnglish,
			SourceID:     "rule-1",
			Body:         "listing review",
		},
	}
	engine := Build(docs, nil)
	results := engine.Search(SearchOptions{Query: "listing", Filter: Filter{Language: "en"}, Limit: 5})
	if len(results) != 1 || results[0].ID != "rule-1-en" || results[0].Language != "en" || results[0].SourceID != "rule-1" {
		t.Fatalf("unexpected English results: %#v", results)
	}
	results = engine.Search(SearchOptions{Query: "상장", Filter: Filter{Language: "ko"}, Limit: 5})
	if len(results) != 1 || results[0].ID != "rule-1" || results[0].Language != "ko" {
		t.Fatalf("unexpected Korean results: %#v", results)
	}
}

func TestVectorRRF(t *testing.T) {
	docs := []model.Document{
		{ID: "a", Title: "상장규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "상장 심사"},
		{ID: "b", Title: "청산규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "청산 결제"},
	}
	engine := Build(docs, map[string][]float64{
		"a#0": {1, 0},
		"b#0": {0, 1},
	})
	results := engine.Search(SearchOptions{
		Query:       "상장",
		QueryVector: []float64{1, 0},
		Limit:       2,
	})
	if len(results) == 0 || results[0].ID != "a" {
		t.Fatalf("unexpected RRF results: %#v", results)
	}
	if results[0].BM25Score == 0 || results[0].VectorScore == 0 {
		t.Fatalf("expected both scores: %#v", results[0])
	}
}

func TestAttachmentTextSearchesParentDocument(t *testing.T) {
	doc := model.Document{
		ID:           "rule-attachment",
		Title:        "파생상품시장 업무규정 시행세칙",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Body:         "본문에는 일반적인 업무규정 내용만 있다.",
		Attachments: []model.Attachment{
			{
				ID:       "att-margin",
				Title:    "증거금 산출 별표",
				FileName: "margin.pdf",
				Status:   model.AttachmentConverted,
			},
		},
	}
	engine := BuildWithAttachments([]model.Document{doc}, map[string]AttachmentDocument{
		"att-margin": {
			Attachment: doc.Attachments[0],
			Text:       "최종결제가격 산출과 스프레드증거금률 적용 방법을 정한다.",
		},
	}, nil)
	results := engine.Search(SearchOptions{Query: "스프레드증거금률", Limit: 5})
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1: %#v", len(results), results)
	}
	if results[0].ID != doc.ID {
		t.Fatalf("attachment should return parent document: %#v", results[0])
	}
	if results[0].MatchedSource != "attachment" {
		t.Fatalf("matched source = %q, want attachment", results[0].MatchedSource)
	}
	if len(results[0].AttachmentMatches) != 1 || results[0].AttachmentMatches[0].ID != "att-margin" {
		t.Fatalf("missing attachment match: %#v", results[0])
	}
}
