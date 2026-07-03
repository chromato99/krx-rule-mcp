package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	"github.com/chromato99/krx-rule-mcp/internal/model"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServiceTools(t *testing.T) {
	doc := model.Document{
		ID:           "rule-1",
		Title:        "코스닥시장 상장규정",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Body:         "상장신청인은 신규상장 심사를 신청할 수 있다.",
	}
	repo := &searchindex.Repository{
		Documents: map[string]model.Document{doc.ID: doc},
		Attachments: map[string]searchindex.AttachmentDocument{
			"att-1": {Attachment: model.Attachment{ID: "att-1", Title: "별표", Status: model.AttachmentConverted}, Text: "첨부 본문"},
		},
		Engine: searchindex.Build([]model.Document{doc}, nil),
	}
	service := &Service{Repo: repo, DomainLexicon: testDomainLexicon(t)}
	_, searchOut, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, SearchRulesInput{Query: "상장", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(searchOut.Results) != 1 {
		t.Fatalf("expected one search result: %#v", searchOut)
	}
	if searchOut.ScoreNote == "" || searchOut.Results[0].MatchedChunkID == "" {
		t.Fatalf("missing RAG search metadata: %#v", searchOut)
	}
	_, ruleOut, err := service.getRule(context.Background(), &mcpsdk.CallToolRequest{}, GetRuleInput{ID: "rule-1"})
	if err != nil {
		t.Fatal(err)
	}
	if ruleOut.Content == "" || ruleOut.Document.Body != "" {
		t.Fatalf("bad rule output: %#v", ruleOut)
	}
	_, attOut, err := service.getAttachment(context.Background(), &mcpsdk.CallToolRequest{}, GetAttachmentInput{ID: "att-1"})
	if err != nil {
		t.Fatal(err)
	}
	if attOut.Content != "첨부 본문" {
		t.Fatalf("bad attachment output: %#v", attOut)
	}
}

func TestServiceSearchRulesLanguageFilter(t *testing.T) {
	ko := model.Document{
		ID:           "rule-1",
		Title:        "코스닥시장 상장규정",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Language:     model.LanguageKorean,
		Body:         "상장 심사",
	}
	en := model.Document{
		ID:           "rule-1-en",
		Title:        "KOSDAQ Market Listing Regulation",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Language:     model.LanguageEnglish,
		SourceID:     "rule-1",
		Body:         "listing review",
	}
	repo := &searchindex.Repository{
		Documents:   map[string]model.Document{ko.ID: ko, en.ID: en},
		Attachments: map[string]searchindex.AttachmentDocument{},
		Engine:      searchindex.Build([]model.Document{ko, en}, nil),
	}
	service := &Service{Repo: repo, DomainLexicon: testDomainLexicon(t)}
	_, out, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, SearchRulesInput{Query: "listing", Language: "en", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].ID != "rule-1-en" || out.Results[0].Language != "en" {
		t.Fatalf("bad language-filtered results: %#v", out.Results)
	}
}

func TestSearchRulesSkipsEmbedderWithoutIndexedVectors(t *testing.T) {
	doc := model.Document{
		ID:           "rule-1",
		Title:        "코스닥시장 상장규정",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Body:         "상장신청인은 신규상장 심사를 신청할 수 있다.",
	}
	embedder := &stubEmbedder{vectors: [][]float64{{1, 0}}}
	service := &Service{Repo: testRepository(doc, nil), Embedder: embedder, DomainLexicon: testDomainLexicon(t)}

	_, out, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, SearchRulesInput{Query: "상장", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != "bm25" {
		t.Fatalf("mode = %q, want bm25", out.Mode)
	}
	if embedder.calls != 0 {
		t.Fatalf("embedder calls = %d, want 0", embedder.calls)
	}
}

func TestSearchRulesUsesVectorModeWhenIndexedVectorsExist(t *testing.T) {
	doc := model.Document{
		ID:           "rule-1",
		Title:        "코스닥시장 상장규정",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Body:         "상장신청인은 신규상장 심사를 신청할 수 있다.",
	}
	embedder := &stubEmbedder{vectors: [][]float64{{1, 0}}}
	service := &Service{Repo: testRepository(doc, map[string][]float64{"rule-1#0": {1, 0}}), Embedder: embedder, DomainLexicon: testDomainLexicon(t)}

	_, out, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, SearchRulesInput{Query: "상장", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != "bm25+vector-rrf" {
		t.Fatalf("mode = %q, want bm25+vector-rrf", out.Mode)
	}
	if embedder.calls != 1 {
		t.Fatalf("embedder calls = %d, want 1", embedder.calls)
	}
	if len(out.Results) == 0 || out.Results[0].BM25Score == 0 || out.Results[0].VectorScore == 0 {
		t.Fatalf("expected blended scores: %#v", out.Results)
	}
}

func TestSearchRulesReportsVectorOnlyFallback(t *testing.T) {
	doc := model.Document{
		ID:           "rule-1",
		Title:        "코스닥시장 상장규정",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Body:         "상장신청인은 신규상장 심사를 신청할 수 있다.",
	}
	embedder := &stubEmbedder{vectors: [][]float64{{1, 0}}}
	service := &Service{Repo: testRepository(doc, map[string][]float64{"rule-1#0": {1, 0}}), Embedder: embedder, DomainLexicon: testDomainLexicon(t)}

	_, out, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, SearchRulesInput{Query: "lexically-unmatched-query", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != "vector" {
		t.Fatalf("mode = %q, want vector", out.Mode)
	}
	if len(out.Results) != 1 || out.Results[0].ID != doc.ID || out.Results[0].BM25Score != 0 || out.Results[0].VectorScore == 0 {
		t.Fatalf("expected vector-only fallback result: %#v", out.Results)
	}
}

func TestSearchRulesExpandsDomainTerms(t *testing.T) {
	doc := model.Document{
		ID:           "derivatives-rule",
		Title:        "파생상품시장 업무규정 시행세칙",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Language:     model.LanguageKorean,
		Body:         "별표25 실시간 가격제한의 가격변동폭은 선물거래와 옵션거래에 적용한다.",
	}
	repo := &searchindex.Repository{
		Documents:   map[string]model.Document{doc.ID: doc},
		Attachments: map[string]searchindex.AttachmentDocument{},
		Engine:      searchindex.Build([]model.Document{doc}, nil),
	}
	service := &Service{Repo: repo, DomainLexicon: testDomainLexicon(t)}

	_, out, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, SearchRulesInput{Query: "동적상하한가", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != "bm25+domain-expansion" {
		t.Fatalf("mode = %q, want bm25+domain-expansion", out.Mode)
	}
	if out.QueryExpansion == nil || !strings.Contains(out.QueryExpansion.ExpandedQuery, "실시간 가격제한의 가격변동폭") {
		t.Fatalf("missing query expansion: %#v", out.QueryExpansion)
	}
	if len(out.Results) != 1 || out.Results[0].ID != doc.ID {
		t.Fatalf("domain expansion should retrieve realtime price limit rule: %#v", out.Results)
	}
}

func TestSearchRulesEmbedsExpandedDomainQuery(t *testing.T) {
	doc := model.Document{
		ID:           "derivatives-rule",
		Title:        "파생상품시장 업무규정 시행세칙",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Language:     model.LanguageKorean,
		Body:         "실시간 가격제한의 가격변동폭",
	}
	embedder := &stubEmbedder{vectors: [][]float64{{1, 0}}}
	service := &Service{Repo: testRepository(doc, map[string][]float64{"derivatives-rule#0": {1, 0}}), Embedder: embedder, DomainLexicon: testDomainLexicon(t)}

	_, out, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, SearchRulesInput{Query: "dynamic price limit", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != "bm25+vector-rrf+domain-expansion" {
		t.Fatalf("mode = %q, want domain-expanded vector mode", out.Mode)
	}
	if len(embedder.inputs) != 1 || !strings.Contains(embedder.inputs[0][0], "실시간 가격제한") {
		t.Fatalf("embedder did not receive expanded query: %#v", embedder.inputs)
	}
}

func TestRealDataDomainExpansionSearch(t *testing.T) {
	if os.Getenv("KRX_DATA_TEST") != "1" {
		t.Skip("set KRX_DATA_TEST=1 to run collected-data domain expansion search")
	}
	dataRoot := os.Getenv("KRX_RULE_DATA_DIR")
	if dataRoot == "" {
		dataRoot = filepath.Join("..", "..", "data")
	}
	repo, err := searchindex.LoadRepository(dataRoot, realDataIndexPath())
	if err != nil {
		t.Fatalf("load repository: %v", err)
	}
	service := &Service{Repo: repo, DomainLexicon: testDomainLexicon(t)}

	_, out, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, SearchRulesInput{
		Query:        "동적상하한가",
		DocumentType: "rule",
		Language:     "ko",
		Limit:        10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.QueryExpansion == nil {
		t.Fatalf("missing domain expansion: %#v", out)
	}
	for _, result := range out.Results {
		if result.Title == "파생상품시장 업무규정 시행세칙" && strings.Contains(result.Snippet, "실시간") {
			return
		}
		for _, match := range result.AttachmentMatches {
			if result.Title == "파생상품시장 업무규정 시행세칙" && strings.Contains(match.Snippet, "실시간") {
				return
			}
		}
	}
	t.Fatalf("missing realtime price limit rule in results: %#v", out.Results)
}

func TestGetContextReturnsMatchedChunkAndNeighbors(t *testing.T) {
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
	service := &Service{Repo: &searchindex.Repository{
		Documents:   map[string]model.Document{doc.ID: doc},
		Attachments: map[string]searchindex.AttachmentDocument{},
		Engine:      searchindex.Build([]model.Document{doc}, nil),
	}}
	_, out, err := service.getContext(context.Background(), &mcpsdk.CallToolRequest{}, GetContextInput{
		ChunkID:      "rule-context#1",
		BeforeChunks: 1,
		AfterChunks:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Document.ID != doc.ID || len(out.Chunks) != 3 {
		t.Fatalf("bad context output: %#v", out)
	}
	if !strings.Contains(out.Content, "chunk_id: rule-context#1") || !strings.Contains(out.Content, "증거금") {
		t.Fatalf("content missing target context: %q", out.Content)
	}
	if out.Document.Body != "" {
		t.Fatalf("document body should be stripped: %#v", out.Document)
	}
}

func TestSearchRulesAddsFormulaNoticeForMatchedAttachment(t *testing.T) {
	doc := model.Document{
		ID:           "rule-1",
		Title:        "시장조성 운영지침",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Language:     model.LanguageKorean,
		Attachments: []model.Attachment{{
			ID:               "att-formula",
			Title:            "시장조성 실적 평가 기준",
			Status:           model.AttachmentConverted,
			FormulaHintCount: 1,
		}},
	}
	att := searchindex.AttachmentDocument{
		Attachment: doc.Attachments[0],
		Text:       "## HWP 수식\n\n수식 1 원본(HWP EqEdit):\n```hwp-equation\n{의무호가`제시시간`} over {의무발생시간}\n```\n\n수식 1 LaTeX(best-effort):\n```math\n\\frac{\\text{의무호가 제시시간}}{\\text{의무발생시간}}\n```",
	}
	repo := &searchindex.Repository{
		Documents:   map[string]model.Document{doc.ID: doc},
		Attachments: map[string]searchindex.AttachmentDocument{att.Attachment.ID: att},
		Engine:      searchindex.BuildWithAttachments([]model.Document{doc}, map[string]searchindex.AttachmentDocument{att.Attachment.ID: att}, nil),
	}
	service := &Service{Repo: repo}

	_, out, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, SearchRulesInput{Query: "의무호가 제시시간", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("expected one search result: %#v", out.Results)
	}
	notice := out.Results[0].FormulaNotice
	if notice == nil || notice.Code != "hwp_formula_latex_best_effort" || notice.Severity != "info" {
		t.Fatalf("missing result formula notice: %#v", out.Results[0])
	}
	if !notice.SourceEquationAvailable || !notice.GeneratedLatexAvailable || notice.FormulaCount != 1 {
		t.Fatalf("bad result formula notice: %#v", notice)
	}
	if len(out.Results[0].AttachmentMatches) != 1 || out.Results[0].AttachmentMatches[0].FormulaNotice == nil {
		t.Fatalf("missing attachment formula notice: %#v", out.Results[0].AttachmentMatches)
	}
	if out.Results[0].AttachmentMatches[0].ChunkID == "" {
		t.Fatalf("missing attachment chunk id: %#v", out.Results[0].AttachmentMatches[0])
	}
}

func TestGetAttachmentAddsFormulaNotice(t *testing.T) {
	att := searchindex.AttachmentDocument{
		Attachment: model.Attachment{ID: "att-formula", Title: "별표", Status: model.AttachmentConverted, FormulaHintCount: 1},
		Text:       "수식 1 원본(HWP EqEdit):\n```hwp-equation\nx over y\n```\n\n수식 1 LaTeX(best-effort):\n```math\n\\frac{x}{y}\n```",
	}
	service := &Service{Repo: &searchindex.Repository{
		Documents:   map[string]model.Document{},
		Attachments: map[string]searchindex.AttachmentDocument{att.Attachment.ID: att},
		Engine:      searchindex.Build(nil, nil),
	}}

	_, out, err := service.getAttachment(context.Background(), &mcpsdk.CallToolRequest{}, GetAttachmentInput{ID: "att-formula"})
	if err != nil {
		t.Fatal(err)
	}
	if out.FormulaNotice == nil || out.FormulaNotice.Code != "hwp_formula_latex_best_effort" {
		t.Fatalf("missing attachment formula notice: %#v", out.FormulaNotice)
	}
	if !out.FormulaNotice.SourceEquationAvailable || !out.FormulaNotice.GeneratedLatexAvailable {
		t.Fatalf("bad attachment formula notice: %#v", out.FormulaNotice)
	}
}

func TestGetAttachmentDistinguishesFormulaTextWithoutEquationBlocks(t *testing.T) {
	att := searchindex.AttachmentDocument{
		Attachment: model.Attachment{ID: "att-formula-text", Title: "별표", Status: model.AttachmentConverted, FormulaHintCount: 2},
		Text:       "일중 의무이행비율 = 의무호가 제시시간 / 의무발생시간",
	}
	service := &Service{Repo: &searchindex.Repository{
		Documents:   map[string]model.Document{},
		Attachments: map[string]searchindex.AttachmentDocument{att.Attachment.ID: att},
		Engine:      searchindex.Build(nil, nil),
	}}

	_, out, err := service.getAttachment(context.Background(), &mcpsdk.CallToolRequest{}, GetAttachmentInput{ID: "att-formula-text"})
	if err != nil {
		t.Fatal(err)
	}
	if out.FormulaNotice == nil || out.FormulaNotice.Code != "formula_text_detected" {
		t.Fatalf("bad formula text notice: %#v", out.FormulaNotice)
	}
	if out.FormulaNotice.SourceEquationAvailable || out.FormulaNotice.GeneratedLatexAvailable {
		t.Fatalf("formula text notice should not claim equation blocks: %#v", out.FormulaNotice)
	}
}

type stubEmbedder struct {
	calls   int
	vectors [][]float64
	inputs  [][]string
}

func (e *stubEmbedder) Embed(_ context.Context, input []string) ([][]float64, error) {
	e.calls++
	e.inputs = append(e.inputs, append([]string(nil), input...))
	return e.vectors, nil
}

func testRepository(doc model.Document, vectors map[string][]float64) *searchindex.Repository {
	return &searchindex.Repository{
		Documents: map[string]model.Document{doc.ID: doc},
		Attachments: map[string]searchindex.AttachmentDocument{
			"att-1": {Attachment: model.Attachment{ID: "att-1", Title: "별표", Status: model.AttachmentConverted}, Text: "첨부 본문"},
		},
		Engine: searchindex.Build([]model.Document{doc}, vectors),
	}
}

func testDomainLexicon(t *testing.T) []searchindex.DomainLexiconEntry {
	t.Helper()
	entries, err := searchindex.LoadDomainLexicon(filepath.Join("..", "..", searchindex.DefaultDomainLexiconPath))
	if err != nil {
		t.Fatalf("load domain lexicon: %v", err)
	}
	return entries
}
