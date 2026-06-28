package index

import (
	"os"
	"path/filepath"
	"strings"
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

func TestVectorFallbackWhenBM25HasNoHits(t *testing.T) {
	docs := []model.Document{
		{ID: "a", Title: "상장규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "상장 심사"},
		{ID: "b", Title: "청산규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "청산 결제"},
	}
	engine := Build(docs, map[string][]float64{
		"a#0": {1, 0},
		"b#0": {0, 1},
	})
	results := engine.Search(SearchOptions{
		Query:       "lexically-unmatched-query",
		QueryVector: []float64{0, 1},
		Limit:       2,
	})
	if len(results) == 0 || results[0].ID != "b" {
		t.Fatalf("expected vector fallback to return semantic match: %#v", results)
	}
	if results[0].BM25Score != 0 || results[0].VectorScore == 0 {
		t.Fatalf("expected vector-only scores: %#v", results[0])
	}
}

func TestBM25UsesTermFrequencyForIndexedDocuments(t *testing.T) {
	docs := []model.Document{
		{ID: "repeated", Title: "반복 문서", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "증거금 증거금 증거금"},
		{ID: "single", Title: "단일 문서", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "증거금"},
	}
	engine := Build(docs, nil)
	results := engine.Search(SearchOptions{Query: "증거금", Limit: 2})
	if len(results) != 2 {
		t.Fatalf("results = %#v, want two documents", results)
	}
	if results[0].ID != "repeated" {
		t.Fatalf("term frequency should rank repeated term first: %#v", results)
	}
}

func TestDocumentsTreatsNegativeOffsetAsZero(t *testing.T) {
	doc := model.Document{ID: "rule-1", Title: "규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "본문"}
	engine := Build([]model.Document{doc}, nil)
	results := engine.Documents(Filter{}, 10, -10)
	if len(results) != 1 || results[0].ID != doc.ID {
		t.Fatalf("negative offset results = %#v", results)
	}
}

func TestBuildSnapshotUsesCurrentIndexVersion(t *testing.T) {
	root := t.TempDir()
	writeFixtureMarkdown(t, root, "rules/ko/test.md", `---
id: test-rule
title: 테스트 규정
document_type: rule
language: ko
content_hash: sha256:test
---
증거금 증거금 증거금
`)

	snap, _, err := BuildSnapshot(root)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	if snap.Version != indexSnapshotFormatVersion {
		t.Fatalf("snapshot version = %d, want %d", snap.Version, indexSnapshotFormatVersion)
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
	if results[0].MatchedChunkID == "" {
		t.Fatalf("missing matched chunk id: %#v", results[0])
	}
	if len(results[0].AttachmentMatches) != 1 || results[0].AttachmentMatches[0].ID != "att-margin" {
		t.Fatalf("missing attachment match: %#v", results[0])
	}
	if results[0].AttachmentMatches[0].ChunkID == "" {
		t.Fatalf("missing attachment chunk id: %#v", results[0].AttachmentMatches[0])
	}
}

func TestContextAroundReturnsNeighboringChunksFromSameSource(t *testing.T) {
	doc := model.Document{
		ID:           "rule-context",
		Title:        "파생상품시장 업무규정 시행세칙",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Body: strings.Join([]string{
			"첫 번째 문맥 " + strings.Repeat("가", 900),
			"두 번째 목표 문맥 증거금 " + strings.Repeat("나", 900),
			"세 번째 문맥 " + strings.Repeat("다", 900),
		}, "\n\n"),
	}
	engine := Build([]model.Document{doc}, nil)
	gotDoc, chunks, ok := engine.ContextAround("rule-context#1", 1, 1)
	if !ok {
		t.Fatal("ContextAround returned false")
	}
	if gotDoc.ID != doc.ID {
		t.Fatalf("document id = %q, want %q", gotDoc.ID, doc.ID)
	}
	if len(chunks) != 3 {
		t.Fatalf("chunks = %d, want 3: %#v", len(chunks), chunks)
	}
	if chunks[0].ID != "rule-context#0" || chunks[1].ID != "rule-context#1" || chunks[2].ID != "rule-context#2" {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
	if chunks[1].Source != "body" || !strings.Contains(chunks[1].Text, "증거금") {
		t.Fatalf("bad target chunk: %#v", chunks[1])
	}
}

func TestExpandDomainQueryDynamicPriceLimit(t *testing.T) {
	expansion := ExpandDomainQueryWithLexicon("동적상하한가 기준이 궁금해", loadTestDomainLexicon(t))
	if !expansion.Applied() {
		t.Fatalf("expected domain expansion: %#v", expansion)
	}
	if !strings.Contains(expansion.ExpandedQuery, "실시간 가격제한의 가격변동폭") {
		t.Fatalf("expanded query missing canonical price limit terms: %q", expansion.ExpandedQuery)
	}
	var found bool
	for _, applied := range expansion.AppliedTerms {
		if applied.ID == "derivatives_realtime_price_limit" {
			found = true
			if applied.Confidence != "high" || len(applied.SourceURLs) == 0 {
				t.Fatalf("missing source metadata: %#v", applied)
			}
		}
	}
	if !found {
		t.Fatalf("missing realtime price limit lexicon match: %#v", expansion.AppliedTerms)
	}
}

func TestExpandDomainQueryDoesNotTreatBarePDFAsETFPortfolioFile(t *testing.T) {
	expansion := ExpandDomainQueryWithLexicon("PDF 첨부 파일", loadTestDomainLexicon(t))
	for _, applied := range expansion.AppliedTerms {
		if applied.ID == "etf_pdf" {
			t.Fatalf("bare PDF should not trigger ETF PDF expansion: %#v", expansion)
		}
	}
}

func TestLoadDomainLexiconFromYAML(t *testing.T) {
	entries := loadTestDomainLexicon(t)
	if len(entries) == 0 {
		t.Fatal("loaded no domain lexicon entries")
	}
	var found bool
	for _, entry := range entries {
		if entry.ID == "derivatives_realtime_price_limit" {
			found = true
			if entry.Canonical != "실시간가격제한제도" || len(entry.SourceURLs) == 0 {
				t.Fatalf("bad realtime price limit entry: %#v", entry)
			}
		}
	}
	if !found {
		t.Fatalf("missing realtime price limit entry: %#v", entries)
	}
}

func loadTestDomainLexicon(t *testing.T) []DomainLexiconEntry {
	t.Helper()
	entries, err := LoadDomainLexicon(filepath.Join("..", "..", DefaultDomainLexiconPath))
	if err != nil {
		t.Fatalf("load domain lexicon: %v", err)
	}
	return entries
}

func writeFixtureMarkdown(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
