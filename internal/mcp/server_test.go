package mcp

import (
	"context"
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
	service := &Service{Repo: repo}
	_, searchOut, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, SearchRulesInput{Query: "상장", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(searchOut.Results) != 1 {
		t.Fatalf("expected one search result: %#v", searchOut)
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
	service := &Service{Repo: repo}
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
	service := &Service{Repo: testRepository(doc, nil), Embedder: embedder}

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
	service := &Service{Repo: testRepository(doc, map[string][]float64{"rule-1#0": {1, 0}}), Embedder: embedder}

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

type stubEmbedder struct {
	calls   int
	vectors [][]float64
}

func (e *stubEmbedder) Embed(context.Context, []string) ([][]float64, error) {
	e.calls++
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
