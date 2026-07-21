package index

import (
	"math"
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
	engine := BuildWithAttachments(docs, nil, nil)
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
	engine := BuildWithAttachments(docs, nil, nil)
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
	engine := BuildWithAttachments(docs, nil, map[string][]float64{
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

func TestInvalidQueryVectorFallsBackToBM25(t *testing.T) {
	doc := model.Document{ID: "a", Title: "상장규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "상장 심사"}
	engine := BuildWithAttachments([]model.Document{doc}, nil, map[string][]float64{"a#0": {1, 0}})
	tests := []struct {
		name   string
		vector []float64
	}{
		{name: "wrong dimensions", vector: []float64{1}},
		{name: "nan", vector: []float64{math.NaN(), 0}},
		{name: "infinity", vector: []float64{math.Inf(1), 0}},
		{name: "float32 overflow", vector: []float64{math.MaxFloat64, 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			results := engine.Search(SearchOptions{Query: "상장", QueryVector: tc.vector, Limit: 1})
			if len(results) != 1 || results[0].ID != doc.ID {
				t.Fatalf("BM25 fallback results = %#v", results)
			}
			if results[0].VectorScore != 0 || results[0].BM25Score == 0 {
				t.Fatalf("invalid query vector was reported as vector search: %#v", results[0])
			}
		})
	}
}

func TestAssetReferencesKeepAltAndAnchorButNotLocalTarget(t *testing.T) {
	doc := model.Document{
		ID: "asset-rule", Title: "자산 규정", DocumentType: model.DocumentTypeRule, Language: model.LanguageKorean,
		Body: "제1조\n\n![위험도표](assets/supersecretlocalpath.png)",
		Assets: []model.Asset{{
			ID: "asset-chart", SourceAnchor: "hwp:BinData/BIN0001.png",
			ReferencePath: "assets/supersecretlocalpath.png",
		}},
	}
	engine := BuildWithAttachments([]model.Document{doc}, nil, nil)
	if results := engine.Search(SearchOptions{Query: "supersecretlocalpath", Limit: 5}); len(results) != 0 {
		t.Fatalf("local asset target was indexed: %#v", results)
	}
	results := engine.Search(SearchOptions{Query: "위험도표 BIN0001", Limit: 5})
	if len(results) != 1 || results[0].ID != doc.ID {
		t.Fatalf("asset alt/source anchor was not indexed: %#v", results)
	}
	_, chunks, ok := engine.ContextAround(results[0].MatchedChunkID, 0, 0)
	if !ok || len(chunks) != 1 || strings.Contains(chunks[0].Text, "supersecretlocalpath") || !strings.Contains(chunks[0].Text, "hwp:BinData/BIN0001.png") {
		t.Fatalf("asset search chunk = %#v", chunks)
	}
}

func TestRRFScoreTiePrefersStrongerBM25(t *testing.T) {
	docs := []model.Document{
		{ID: "semantic", Title: "의미 검색", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "공통"},
		{ID: "lexical", Title: "정확 검색", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "공통 희귀 희귀 희귀"},
	}
	engine := BuildWithAttachments(docs, nil, map[string][]float64{
		"semantic#0": {1, 0},
		"lexical#0":  {0.8, 0.2},
	})
	results := engine.Search(SearchOptions{
		Query:       "공통 희귀",
		QueryVector: []float64{1, 0},
		Limit:       2,
	})
	if len(results) != 2 {
		t.Fatalf("results = %#v, want two documents", results)
	}
	if !sameScore(results[0].Score, results[1].Score) {
		t.Fatalf("test setup should produce an RRF score tie: %#v", results)
	}
	if results[0].ID != "lexical" {
		t.Fatalf("RRF tie should prefer stronger BM25 result: %#v", results)
	}
}

func TestTokenizeAddsScriptNotationAliases(t *testing.T) {
	tokens := Tokenize("S_{0} S<sub>-15</sub> 99<sup>th</sup> 컨설팅 방식<sup>3)</sup>")
	for _, want := range []string{"s0", "s15", "99th", "방식3"} {
		if !containsToken(tokens, want) {
			t.Fatalf("Tokenize() missing %q in %#v", want, tokens)
		}
	}
}

func TestVectorFallbackWhenBM25HasNoHits(t *testing.T) {
	docs := []model.Document{
		{ID: "a", Title: "상장규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "상장 심사"},
		{ID: "b", Title: "청산규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "청산 결제"},
	}
	engine := BuildWithAttachments(docs, nil, map[string][]float64{
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

func containsToken(tokens []string, want string) bool {
	for _, token := range tokens {
		if token == want {
			return true
		}
	}
	return false
}

func TestBM25UsesTermFrequencyForIndexedDocuments(t *testing.T) {
	docs := []model.Document{
		{ID: "repeated", Title: "반복 문서", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "증거금 증거금 증거금"},
		{ID: "single", Title: "단일 문서", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "증거금"},
	}
	engine := BuildWithAttachments(docs, nil, nil)
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
	engine := BuildWithAttachments([]model.Document{doc}, nil, nil)
	results := engine.Documents(Filter{}, 10, -10)
	if len(results) != 1 || results[0].ID != doc.ID {
		t.Fatalf("negative offset results = %#v", results)
	}
}

func TestSearchLimitClampsToMax(t *testing.T) {
	var docs []model.Document
	for i := 0; i < 60; i++ {
		docs = append(docs, model.Document{
			ID:           "rule-" + itoa(i),
			Title:        "상장 규정",
			CollectedAt:  time.Now(),
			DocumentType: model.DocumentTypeRule,
			Body:         "상장 심사",
		})
	}
	engine := BuildWithAttachments(docs, nil, nil)
	results := engine.Search(SearchOptions{Query: "상장", Limit: 100})
	if len(results) != 50 {
		t.Fatalf("len(results) = %d, want clamp to 50", len(results))
	}
}

func TestDocumentsLimitClampsAndSortsDeterministically(t *testing.T) {
	date := time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC)
	var docs []model.Document
	for i := 249; i >= 0; i-- {
		docs = append(docs, model.Document{
			ID:           "rule-" + itoa(i),
			Title:        "규정",
			CollectedAt:  date,
			DocumentType: model.DocumentTypeRule,
			Body:         "본문",
		})
	}
	engine := BuildWithAttachments(docs, nil, nil)
	results, total := engine.DocumentsPage(Filter{}, 500, 0)
	if total != 250 {
		t.Fatalf("total = %d, want 250", total)
	}
	if len(results) != 200 {
		t.Fatalf("len(results) = %d, want clamp to 200", len(results))
	}
	if results[0].ID != "rule-0" || results[1].ID != "rule-1" {
		t.Fatalf("same-date documents should be ordered by id: %#v", results[:2])
	}
}

func TestChunkTextSplitsMarkdownTablesOnRowsAndRepeatsHeader(t *testing.T) {
	table := strings.Join([]string{
		"| 구분 | 값 |",
		"| --- | --- |",
		"| A | " + strings.Repeat("가", 20) + " |",
		"| B | " + strings.Repeat("나", 20) + " |",
		"| C | " + strings.Repeat("다", 20) + " |",
	}, "\n")
	chunks := ChunkText(table, 65)
	if len(chunks) < 2 {
		t.Fatalf("expected table to split by rows: %#v", chunks)
	}
	for _, chunk := range chunks {
		if !strings.Contains(chunk, "| 구분 | 값 |") || !strings.Contains(chunk, "| --- | --- |") {
			t.Fatalf("chunk missing repeated header: %q", chunk)
		}
	}
}

func TestChunkTextSplitsHTMLTablesOnRowsAndRepeatsHeader(t *testing.T) {
	table := strings.Join([]string{
		`<table>`,
		`  <tr><th rowspan="2">구분</th><th>값</th></tr>`,
		`  <tr><td>A</td><td>` + strings.Repeat("가", 30) + `</td></tr>`,
		`  <tr><td>B</td><td>` + strings.Repeat("나", 30) + `</td></tr>`,
		`  <tr><td>C</td><td>` + strings.Repeat("다", 30) + `</td></tr>`,
		`</table>`,
	}, "\n")
	chunks := ChunkText(table, 120)
	if len(chunks) < 2 {
		t.Fatalf("expected HTML table to split by rows: %#v", chunks)
	}
	for _, chunk := range chunks {
		if !strings.HasPrefix(chunk, "<table>") || !strings.HasSuffix(chunk, "</table>") {
			t.Fatalf("chunk should preserve table wrapper: %q", chunk)
		}
		if !strings.Contains(chunk, `<tr><th rowspan="2">구분</th><th>값</th></tr>`) {
			t.Fatalf("chunk missing repeated header row: %q", chunk)
		}
		if strings.Count(chunk, "<tr") != strings.Count(chunk, "</tr>") {
			t.Fatalf("chunk split a table row: %q", chunk)
		}
	}
}

func TestStructuredChunksPreserveOwningArticleAndHeadingPath(t *testing.T) {
	doc := model.Document{
		ID:           "rule-structure",
		Title:        "구조 규정",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Body: strings.Join([]string{
			"제1장 총칙",
			"",
			"제1절 목적",
			"",
			"**제1조(목적)**① 구조 경계를 검증한다.",
			"",
			"1. 첫 번째 요건",
			"",
			"가. 세부 소유 조문",
			"",
			"제99조(외부조문)에 따른 인용표현은 새 소유 조문이 아니다.",
			"",
			"**제2조(정의)** 정의 조문",
		}, "\n"),
	}
	engine := BuildWithAttachments([]model.Document{doc}, nil, nil)
	results := engine.Search(SearchOptions{Query: "세부 소유", Limit: 1})
	wantPath := []string{"제1장 총칙", "제1절 목적", "제1조(목적)", "①", "1.", "가."}
	if len(results) != 1 || results[0].ArticleID != "제1조" || !stringSlicesEqual(results[0].HeadingPath, wantPath) {
		t.Fatalf("unexpected structural anchor: %#v", results)
	}
	results = engine.Search(SearchOptions{Query: "인용표현", Limit: 1})
	if len(results) != 1 || results[0].ArticleID != "제1조" {
		t.Fatalf("cited article became owning article: %#v", results)
	}
	results = engine.Search(SearchOptions{Query: "정의 조문", Limit: 1})
	if len(results) != 1 || results[0].ArticleID != "제2조" {
		t.Fatalf("new owning article was not detected: %#v", results)
	}
}

func TestChunkTextKeepsHWPEquationMathPairAtomic(t *testing.T) {
	text := strings.Join([]string{
		"## HWP 수식",
		"",
		"### 수식 1",
		"",
		"수식 1 원본(HWP EqEdit):",
		"```hwp-equation",
		"A = " + strings.Repeat("x", 80),
		"```",
		"",
		"수식 1 LaTeX(best-effort):",
		"```math",
		"A = " + strings.Repeat("y", 80),
		"```",
	}, "\n")
	chunks := ChunkTextWithAnchors(text, 40)
	paired := 0
	for _, chunk := range chunks {
		hasSource := strings.Contains(chunk.Text, "```hwp-equation")
		hasMath := strings.Contains(chunk.Text, "```math")
		if hasSource != hasMath {
			t.Fatalf("equation pair was split: %#v", chunks)
		}
		if hasSource {
			paired++
			if !strings.Contains(chunk.Text, "수식 1 원본(HWP EqEdit):") || !strings.Contains(chunk.Text, "수식 1 LaTeX(best-effort):") {
				t.Fatalf("producer equation labels were not kept with the pair: %q", chunk.Text)
			}
			if runeLen(chunk.Text) <= 40 {
				t.Fatalf("test pair should exercise the oversized atomic policy: %q", chunk.Text)
			}
			if !stringSlicesEqual(chunk.HeadingPath, []string{"HWP 수식", "수식 1"}) {
				t.Fatalf("equation heading path = %#v", chunk.HeadingPath)
			}
		}
	}
	if paired != 1 {
		t.Fatalf("paired equation chunks = %d, want 1: %#v", paired, chunks)
	}
}

func TestOversizedTableRowsRemainAtomic(t *testing.T) {
	longCell := strings.Repeat("초과행", 40)
	tests := []struct {
		name string
		text string
		row  string
	}{
		{
			name: "markdown",
			text: "| 구분 | 값 |\n| --- | --- |\n| A | " + longCell + " |\n| B | 짧은행 |",
			row:  "| A | " + longCell + " |",
		},
		{
			name: "html",
			text: "<table>\n<tr><th>구분</th><th>값</th></tr>\n<tr><td>A</td><td>" + longCell + "</td></tr>\n<tr><td>B</td><td>짧은행</td></tr>\n</table>",
			row:  "<tr><td>A</td><td>" + longCell + "</td></tr>",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chunks := ChunkText(tc.text, 80)
			owners := 0
			for _, chunk := range chunks {
				if strings.Contains(chunk, tc.row) {
					owners++
					if runeLen(chunk) <= 80 {
						t.Fatalf("oversized row policy was not exercised: %q", chunk)
					}
				}
			}
			if owners != 1 {
				t.Fatalf("oversized row was split or duplicated: %#v", chunks)
			}
		})
	}
}

func TestBuildSnapshotUsesCurrentIndexVersion(t *testing.T) {
	root := t.TempDir()
	writeIndexTestDocument(t, root, model.Document{
		ID:           "test-rule",
		Title:        "테스트 규정",
		SourceURL:    "https://example.test/rule",
		CollectedAt:  time.Now().UTC(),
		DocumentType: model.DocumentTypeRule,
		Language:     model.LanguageKorean,
		Body:         "증거금 증거금 증거금",
	})

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
	engine := BuildWithAttachments([]model.Document{doc}, nil, nil)
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

func TestDomainExpansionTokenWeightsPreferOriginalQuery(t *testing.T) {
	engine := BuildWithAttachments([]model.Document{
		{
			ID:           "original",
			Title:        "Original",
			DocumentType: model.DocumentTypeRule,
			Language:     model.LanguageKorean,
			Body:         "동적상하한가",
		},
		{
			ID:           "expanded",
			Title:        "Expanded",
			DocumentType: model.DocumentTypeRule,
			Language:     model.LanguageKorean,
			Body:         "실시간가격제한제도",
		},
	}, nil, nil)
	expansion := DomainQueryExpansion{
		OriginalQuery: "동적상하한가",
		ExpandedQuery: "동적상하한가 실시간가격제한제도",
		AppliedTerms: []DomainLexiconMatch{{
			AddedTerms: []string{"실시간가격제한제도"},
		}},
	}
	results := engine.Search(SearchOptions{
		Query:        expansion.ExpandedQuery,
		Limit:        2,
		TokenWeights: expansion.TokenWeights(0.4),
	})
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2: %#v", len(results), results)
	}
	if results[0].ID != "original" {
		t.Fatalf("weighted expansion should prefer original query match, got %#v", results)
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

func TestLoadDomainLexiconFromEmbeddedDefault(t *testing.T) {
	entries, err := LoadDomainLexicon("")
	if err != nil {
		t.Fatalf("load embedded domain lexicon: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("loaded no embedded domain lexicon entries")
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
