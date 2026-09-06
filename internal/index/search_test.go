package index

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/model"
)

func TestCandidateBudgetDoesNotDependOnResultLimit(t *testing.T) {
	var body strings.Builder
	for i := 1; i <= 180; i++ {
		fmt.Fprintf(&body, "## 제%d조(증거금 납부)\n\n증거금 납부 의무와 절차 %d\n\n", i, i)
	}
	engine := buildTestEngine([]model.Document{{ID: "rule", Title: "규정", Body: body.String()}}, nil, nil)
	var baseline []ChunkCandidate
	for _, limit := range []int{5, 0, 1, 10, 50} {
		got := engine.RetrieveCandidates(SearchOptions{Query: "증거금 납부", Limit: limit}).Chunks
		if len(got) != DefaultRetrievalCandidateLimit {
			t.Fatalf("limit=%d candidates=%d", limit, len(got))
		}
		if baseline == nil {
			baseline = got
			continue
		}
		for i := range got {
			if got[i].ChunkID != baseline[i].ChunkID || got[i].Score != baseline[i].Score {
				t.Fatalf("limit=%d changed candidate %d", limit, i)
			}
		}
	}
}

func buildTestEngine(documents []model.Document, attachments map[string]AttachmentDocument, vectors map[string][]float64) *Engine {
	searchable := true
	for index := range documents {
		if documents[index].Searchable == nil {
			documents[index].Searchable = &searchable
		}
		for attachmentIndex := range documents[index].Attachments {
			if documents[index].Attachments[attachmentIndex].Searchable == nil {
				documents[index].Attachments[attachmentIndex].Searchable = &searchable
			}
		}
	}
	for id, attachment := range attachments {
		if attachment.Attachment.Searchable == nil {
			attachment.Attachment.Searchable = &searchable
			attachments[id] = attachment
		}
	}
	return BuildWithAttachments(documents, attachments, vectors)
}

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
	engine := buildTestEngine(docs, nil, nil)
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
	engine := buildTestEngine(docs, nil, nil)
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
	engine := buildTestEngine(docs, nil, map[string][]float64{
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

func TestBM25AndVectorCandidatesFromDifferentChunksRemainEvidence(t *testing.T) {
	doc := model.Document{
		ID: "mixed-channel", Title: "복합 규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule,
		Body: "**제1조(상장심사)** 상장 심사 요건을 정한다.\n\n**제2조(결제수량)** 결제 수량을 정한다.",
	}
	engine := buildTestEngine([]model.Document{doc}, nil, map[string][]float64{
		"mixed-channel#0": {0, 1},
		"mixed-channel#1": {1, 0},
	})
	results := engine.Search(SearchOptions{Query: "상장 심사", OriginalQuery: "상장 심사", QueryVector: []float64{1, 0}, Limit: 1})
	if len(results) != 1 || len(results[0].EvidenceMatches) < 2 {
		t.Fatalf("channel-specific evidence was collapsed: %#v", results)
	}
	var bm25Only, vectorOnly bool
	for _, evidence := range results[0].EvidenceMatches {
		bm25Only = bm25Only || evidence.BM25Score > 0 && evidence.VectorScore == 0
		vectorOnly = vectorOnly || evidence.VectorScore > 0 && evidence.BM25Score == 0
	}
	if !bm25Only || !vectorOnly {
		t.Fatalf("BM25/vector evidence provenance was lost: %#v", results[0].EvidenceMatches)
	}
	if results[0].MatchedChunkID != results[0].EvidenceMatches[0].ChunkID {
		t.Fatalf("representative chunk is not the highest-ranked evidence: %#v", results[0])
	}
}

func TestArticleEvidenceOutranksTitleOnlyMetadataMatch(t *testing.T) {
	now := time.Now()
	titleOnly := model.Document{
		ID: "title-only", Title: "고유 결제 수량 계산", CollectedAt: now,
		DocumentType: model.DocumentTypeRule, Body: "일반 사항만 정한다.",
	}
	evidence := model.Document{
		ID: "article-evidence", Title: "파생상품 업무규정", CollectedAt: now,
		DocumentType: model.DocumentTypeRule, Body: "**제818조(결제수량)** 고유 결제 수량 계산 방법을 정한다.",
	}
	results := buildTestEngine([]model.Document{titleOnly, evidence}, nil, nil).Search(SearchOptions{
		Query: "고유 결제 수량 계산", OriginalQuery: "고유 결제 수량 계산", Limit: 2,
	})
	if len(results) != 2 || results[0].ID != evidence.ID || results[0].ArticleID != "제818조" {
		t.Fatalf("title metadata displaced direct article evidence: %#v", results)
	}
}

func TestInvalidQueryVectorFallsBackToBM25(t *testing.T) {
	doc := model.Document{ID: "a", Title: "상장규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "상장 심사"}
	engine := buildTestEngine([]model.Document{doc}, nil, map[string][]float64{"a#0": {1, 0}})
	tests := []struct {
		name   string
		vector []float64
	}{
		{name: "wrong dimensions", vector: []float64{1}},
		{name: "nan", vector: []float64{math.NaN(), 0}},
		{name: "infinity", vector: []float64{math.Inf(1), 0}},
		{name: "float32 overflow", vector: []float64{math.MaxFloat64, 0}},
		{name: "zero norm", vector: []float64{0, 0}},
		{name: "float32 underflow to zero", vector: []float64{math.SmallestNonzeroFloat64, 0}},
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
	engine := buildTestEngine([]model.Document{doc}, nil, nil)
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
	engine := buildTestEngine(docs, nil, map[string][]float64{
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
	engine := buildTestEngine(docs, nil, map[string][]float64{
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
	engine := buildTestEngine(docs, nil, nil)
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
	engine := buildTestEngine([]model.Document{doc}, nil, nil)
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
	engine := buildTestEngine(docs, nil, nil)
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
	engine := buildTestEngine(docs, nil, nil)
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
	engine := buildTestEngine([]model.Document{doc}, nil, nil)
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

func TestStructuredChunksSkipEnglishTOCAndPreserveSectionArticleAnchors(t *testing.T) {
	doc := model.Document{
		ID:           "rule-english-structure",
		Title:        "Derivatives Market Business Regulation",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Language:     model.LanguageEnglish,
		Body: strings.Join([]string{
			"TABLE OF CONTENTS",
			"",
			"§818. Calculation of Settlement Quantity ........ 42",
			"",
			"CHAPTER I. GENERAL PROVISIONS",
			"",
			"Section 1. Settlement",
			"",
			"§818. Calculation of Settlement Quantity",
			"",
			"(1) The settlement quantity equals the exercise quantity multiplied by the multiplier.",
			"",
			"§819. Settlement Payment",
			"",
			"The Exchange pays the settlement amount.",
		}, "\n"),
	}
	engine := buildTestEngine([]model.Document{doc}, nil, nil)
	results := engine.Search(SearchOptions{Query: "exercise quantity multiplier", Limit: 3})
	wantPath := []string{"CHAPTER I. GENERAL PROVISIONS", "Section 1. Settlement", "§818. Calculation of Settlement Quantity"}
	if len(results) != 1 || results[0].ArticleID != "§818" || !stringSlicesEqual(results[0].HeadingPath, wantPath) {
		t.Fatalf("unexpected English structural anchor: %#v", results)
	}
	for _, chunk := range engine.chunks {
		if strings.Contains(chunk.Text, "........ 42") {
			t.Fatalf("English table of contents row was indexed as evidence: %#v", chunk)
		}
	}
}

func TestEnglishChapterOwnershipDoesNotTreatPartiesAsPartHeading(t *testing.T) {
	text := "CHAPTER III. USE OF FUND\n\n§10. Use of Fund\n\nparties shall not be obligated to pay unrelated expenses.\n\nSection 1. Records\n\nGeneral introductory text.\n\n§11. Record Retention\n\nRecords shall be retained."
	chunks := ChunkTextWithAnchors(text, 1600)
	for _, chunk := range chunks {
		if strings.Contains(chunk.Text, "parties shall") && chunk.ArticleID != "§10" {
			t.Fatalf("parties reset owner: %#v", chunk)
		}
		if strings.Contains(chunk.Text, "General introductory") && chunk.ArticleID != "" {
			t.Fatalf("previous article leaked into new section: %#v", chunk)
		}
		if strings.Contains(chunk.Text, "Records shall") && chunk.ArticleID != "§11" {
			t.Fatalf("new article owner lost: %#v", chunk)
		}
		for _, heading := range chunk.HeadingPath {
			if strings.HasPrefix(heading, "parties") {
				t.Fatal("sentence became section heading")
			}
		}
	}
}

func TestChunkCandidatesRemainIndependentUntilDocumentAggregation(t *testing.T) {
	doc := model.Document{
		ID: "rule-candidates", Title: "청크 후보 규정", CollectedAt: time.Now(),
		DocumentType: model.DocumentTypeRule,
		Body: strings.Join([]string{
			"**제1조(사전절차)** 사전 신고 절차를 정한다.",
			"",
			"**제2조(협의절차)** 협의 요청 절차를 정한다.",
			"",
			"**제3조(공동절차)** 사전 협의 결과를 기록한다.",
		}, "\n"),
	}
	engine := buildTestEngine([]model.Document{doc}, nil, nil)
	query := "사전 협의"
	candidates := engine.bm25ChunkCandidates(Tokenize(query), meaningfulQueryTerms(query), Filter{}, nil, 10)
	if len(candidates) < 3 {
		t.Fatalf("chunk candidates collapsed before fusion: %#v", candidates)
	}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if candidate.DocumentID != doc.ID {
			t.Fatalf("candidate document = %q, want %q", candidate.DocumentID, doc.ID)
		}
		seen[candidate.ChunkID] = struct{}{}
	}
	if len(seen) != len(candidates) {
		t.Fatalf("chunk candidates contain duplicate IDs: %#v", candidates)
	}
	results := engine.Search(SearchOptions{Query: query, OriginalQuery: query, Limit: 1})
	if len(results) != 1 || len(results[0].EvidenceMatches) != 3 {
		t.Fatalf("document aggregation lost top evidence candidates: %#v", results)
	}
	if results[0].MatchedChunkID != results[0].EvidenceMatches[0].ChunkID {
		t.Fatalf("matched chunk %q is not top evidence %q", results[0].MatchedChunkID, results[0].EvidenceMatches[0].ChunkID)
	}
}

func TestAdjacentDistinctArticleChunksRemainSeparateEvidence(t *testing.T) {
	candidates := []ChunkCandidate{
		{ChunkID: "rule#10", DocumentID: "rule", ChunkIndex: 10, ArticleID: "제20조", Text: "ETF 순자산가치 괴리율 3퍼센트", Score: 2},
		{ChunkID: "rule#11", DocumentID: "rule", ChunkIndex: 11, ArticleID: "제20조", Text: "ETN 지표가치 괴리율 6퍼센트", Score: 1},
	}
	selected := selectEvidenceCandidates(candidates, 3)
	if len(selected) != 2 {
		t.Fatalf("adjacent distinct evidence was collapsed: %#v", selected)
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
				ID:               "att-margin",
				Title:            "증거금 산출 별표",
				FileName:         "margin.pdf",
				ConversionStatus: model.AttachmentConverted,
			},
		},
	}
	engine := buildTestEngine([]model.Document{doc}, map[string]AttachmentDocument{
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
	engine := buildTestEngine([]model.Document{doc}, nil, nil)
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

func TestReviewedExactMatchRejectsSubstringExpansion(t *testing.T) {
	exact := ExpandDomainQueryWithLexicon("동적상하한가", loadTestDomainLexicon(t))
	if !exact.Reviewed() || !exact.ReviewedExactMatch() {
		t.Fatalf("exact reviewed alias was not recognized: %#v", exact)
	}
	if exact.MatchedTermCount() == 0 || exact.ReviewedMatchCount() == 0 {
		t.Fatalf("reviewed match counts were not recorded: %#v", exact)
	}
	if len(exact.ReviewedAppliedTerms()) == 0 {
		t.Fatalf("reviewed applied terms were not exposed: %#v", exact)
	}
	mixed := ExpandDomainQueryWithLexicon("동적상하한가 운전면허 갱신", loadTestDomainLexicon(t))
	if mixed.ReviewedExactMatch() {
		t.Fatalf("substring expansion covered a mixed query: %#v", mixed)
	}
	unreviewed := DomainQueryExpansion{
		OriginalQuery: "동적상하한가",
		AppliedTerms: []DomainLexiconMatch{{
			MatchedTerms: []string{"동적상하한가"}, Confidence: "high", ReviewStatus: "draft",
		}},
	}
	if unreviewed.Reviewed() || unreviewed.ReviewedExactMatch() {
		t.Fatalf("unknown review status was trusted: %#v", unreviewed)
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
	engine := buildTestEngine([]model.Document{
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
func TestExpandedEvidencePrefersDirectHeadingPhrase(t *testing.T) {
	terms := []string{"실시간 가격제한의 가격변동폭"}
	candidates := []ChunkCandidate{
		{
			ChunkID:    "indirect",
			DocumentID: "rule",
			FusedScore: 0.040,
			Text:       "실시간 가격제한의 가격변동폭을 인용한다.",
			HeadingPath: []string{
				"제62조(상·하한 단일가호가의 우선순위)",
			},
		},
		{
			ChunkID:    "direct",
			DocumentID: "rule",
			FusedScore: 0.030,
			HeadingPath: []string{
				"제60조의4(실시간 가격제한의 가격변동폭)",
			},
		},
	}
	selected := selectExpandedEvidenceCandidates(candidates, 2, nil, terms, nil, nil, "")
	if len(selected) != 2 || selected[0].ChunkID != "direct" {
		t.Fatalf("direct heading was not preferred: %#v", selected)
	}
	if selected[0].Score <= selected[1].Score {
		t.Fatalf("public evidence scores do not explain order: %#v", selected)
	}
}

func TestExpandedEvidenceRewardsMatchingAttachmentPhrase(t *testing.T) {
	terms := []string{
		"증거금 감면액 산출변수",
		"가격상관율을 기초자산별로 산술평균한 값 중 최솟값",
	}
	candidates := []ChunkCandidate{
		{
			ChunkID:    "body",
			DocumentID: "rule",
			FusedScore: 0.035,
			Text:       "증거금 감면액 산출변수를 변경한다.",
		},
		{
			ChunkID:      "attachment",
			DocumentID:   "rule",
			AttachmentID: "att-1",
			FusedScore:   0.020,
			Text:         "가격상관율을 기초자산별로 산술평균한 값 중 최솟값으로 한다.",
		},
	}
	selected := selectExpandedEvidenceCandidates(candidates, 2, nil, terms, nil, nil, "")
	if len(selected) != 2 || selected[0].ChunkID != "attachment" {
		t.Fatalf("matching attachment was not preferred: %#v", selected)
	}
}

func TestExpandedEvidenceKeepsQuantitativeClaimsInSelectedBundle(t *testing.T) {
	candidates := []ChunkCandidate{
		{ChunkID: "generic", DocumentID: "rule", ChunkIndex: 1, ArticleID: "제18조", FusedScore: 0.04, Text: "유동성공급호가 제출의무"},
		{ChunkID: "three", DocumentID: "rule", ChunkIndex: 10, ArticleID: "제18조", FusedScore: 0.02, Text: "괴리율 3퍼센트"},
		{ChunkID: "six", DocumentID: "rule", ChunkIndex: 20, ArticleID: "제19조", FusedScore: 0.02, Text: "괴리율 6퍼센트"},
	}
	selected := selectExpandedEvidenceCandidates(candidates, 3, nil, nil, []string{"3퍼센트", "6퍼센트"}, nil, "")
	if len(selected) != 3 || selected[0].ChunkID == "generic" {
		t.Fatalf("quantitative evidence was not promoted: %#v", selected)
	}
}

func TestExpandedEvidenceCoversExplicitIdentifierConcepts(t *testing.T) {
	candidates := []ChunkCandidate{
		{ChunkID: "generic", DocumentID: "rule", ChunkIndex: 1, ArticleID: "제20조", FusedScore: 0.05, Text: "유동성공급호가 제출의무"},
		{ChunkID: "etf", DocumentID: "rule", ChunkIndex: 2, ArticleID: "제20조", FusedScore: 0.02, Text: "상장지수집합투자기구의 순자산가치"},
		{ChunkID: "etn", DocumentID: "rule", ChunkIndex: 3, ArticleID: "제20조", FusedScore: 0.02, Text: "상장지수증권의 지표가치"},
	}
	concepts := [][]string{
		{"ETF", "상장지수집합투자기구"},
		{"ETN", "상장지수증권"},
	}
	selected := selectExpandedEvidenceCandidates(candidates, 3, nil, nil, nil, concepts, "")
	seen := map[string]bool{}
	for _, candidate := range selected {
		seen[candidate.ChunkID] = true
	}
	if !seen["etf"] || !seen["etn"] {
		t.Fatalf("identifier concepts were not covered: %#v", selected)
	}
}

func TestExpandedEvidencePromotesReviewedEvidenceConcept(t *testing.T) {
	candidates := []ChunkCandidate{
		{ChunkID: "generic", DocumentID: "rule", FusedScore: 0.040, Text: "상장예비심사 신청 절차"},
		{ChunkID: "direct", DocumentID: "rule", FusedScore: 0.030, Text: "신청인은 상장예비심사 전에 거래소와 사전협의하여야 한다."},
	}
	concepts := [][]string{{"사전협의 의무", "사전협의", "미리 거래소와 협의"}}
	selected := selectExpandedEvidenceCandidates(candidates, 2, nil, nil, nil, concepts, "")
	if len(selected) != 2 || selected[0].ChunkID != "direct" {
		t.Fatalf("reviewed evidence concept was not promoted: %#v", selected)
	}
}

func TestEvidenceSetConceptCoverageUsesWholeDocumentCandidateSet(t *testing.T) {
	candidates := []ChunkCandidate{
		{ChunkID: "direct-rule", Text: "순자산가치 괴리율이 기준을 초과하지 않도록 유동성공급호가를 제출한다."},
		{ChunkID: "document-context", Text: "상장지수집합투자기구 ETF"},
	}
	concepts := [][]string{
		{"ETF", "상장지수집합투자기구"},
		{"NAV", "순자산가치"},
		{"LP", "유동성공급호가"},
	}
	if got := evidenceSetConceptCoverage(candidates, concepts); got != 1 {
		t.Fatalf("evidenceSetConceptCoverage() = %v, want 1", got)
	}
}

func TestFormulaQueryPrefersConcreteFormulaChunk(t *testing.T) {
	candidates := []ChunkCandidate{
		{ChunkID: "explanation", DocumentID: "rule", FusedScore: 0.040, Text: "평가항목 점수는 다음 계산식에 따라 산출한다."},
		{ChunkID: "formula", DocumentID: "rule", FusedScore: 0.036, Text: "```math\n점수 = min(10, 10 × 거래실적 / 평가기준)\n```"},
	}
	selected := selectExpandedEvidenceCandidates(candidates, 2, nil, nil, nil, nil, "평가점수 min 10 계산식")
	if len(selected) != 2 || selected[0].ChunkID != "formula" {
		t.Fatalf("formula chunk was not preferred: %#v", selected)
	}
}

func TestSearchQueryTokensAreDeduplicated(t *testing.T) {
	got := uniqueSearchTokens([]string{"증거금", "증거금", "margin", "증거금", "margin"})
	want := []string{"증거금", "margin"}
	if !slices.Equal(got, want) {
		t.Fatalf("uniqueSearchTokens() = %#v, want %#v", got, want)
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

func TestCompositionalLexiconMatchRequiresEveryConceptGroup(t *testing.T) {
	entry := DomainLexiconEntry{
		ID: "listing-consultation", Canonical: "사전협의 의무",
		Confidence: "high", ReviewStatus: "curated-corpus",
		MatchGroups: [][]string{
			{"상장", "신규상장", "listing"},
			{"협의", "조율", "조정", "consultation"},
		},
		Expansions: []string{"사전협의"},
	}
	matched := ExpandDomainQueryWithLexicon("신규상장 진행 순서를 거래소와 조정해야 하나", []DomainLexiconEntry{entry})
	if !matched.Applied() || matched.MatchedTermCount() != 2 || matched.ReviewedMatchedGroupCount() != 2 || !strings.Contains(matched.ExpandedQuery, "사전협의") {
		t.Fatalf("compositional match = %#v", matched)
	}
	for _, query := range []string{"신규상장 신청 서류", "거래소와 진행 방법 조율"} {
		if got := ExpandDomainQueryWithLexicon(query, []DomainLexiconEntry{entry}); got.Applied() {
			t.Errorf("partial concept groups matched %q: %#v", query, got)
		}
	}
}

func TestRepositoryCompositionalLexiconSeparatesNearbyLegalConcepts(t *testing.T) {
	entries := loadTestDomainLexicon(t)
	tests := []struct {
		query    string
		wantID   string
		rejectID string
	}{
		{query: "KRX futures real-time 상하한가 폭", wantID: "derivatives_realtime_price_limit"},
		{query: "보고대상 거래별 고유번호를 포함하는 조문", wantID: "trade_reporting_uti"},
		{query: "거래 상대방 법인 식별번호", rejectID: "trade_reporting_uti"},
		{query: "시장조성을 나흘만 한 경우", wantID: "market_maker_short_evaluation_period"},
		{query: "의무충족일수와 시장조성일수의 비율", rejectID: "market_maker_short_evaluation_period"},
		{query: "when is intraday clearing margin due", wantID: "intraday_member_margin_deadline", rejectID: "intraday_client_margin_deadline"},
		{query: "intraday customer clearing margin deadline", wantID: "intraday_client_margin_deadline", rejectID: "intraday_member_margin_deadline"},
	}
	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			expansion := ExpandDomainQueryWithLexicon(test.query, entries)
			seen := map[string]bool{}
			for _, match := range expansion.AppliedTerms {
				seen[match.ID] = true
			}
			if test.wantID != "" && !seen[test.wantID] {
				t.Fatalf("expansion missing %q: %#v", test.wantID, expansion.AppliedTerms)
			}
			if test.rejectID != "" && seen[test.rejectID] {
				t.Fatalf("expansion incorrectly included %q: %#v", test.rejectID, expansion.AppliedTerms)
			}
		})
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
