package mcp

import (
	"context"
	"encoding/json"
	"math"
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
		Engine: searchindex.BuildWithAttachments([]model.Document{doc}, nil, nil),
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
	if ruleOut.Content == "" {
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

func TestNewServerBuildsPublicToolSchemas(t *testing.T) {
	service := &Service{Repo: &searchindex.Repository{
		Documents:   map[string]model.Document{},
		Attachments: map[string]searchindex.AttachmentDocument{},
		Engine:      searchindex.BuildWithAttachments(nil, nil, nil),
	}}
	if server := NewServer(service, "test"); server == nil {
		t.Fatal("NewServer returned nil")
	}
}

func TestGetRuleDefaultsToBoundedContent(t *testing.T) {
	doc := model.Document{
		ID:           "rule-long",
		Title:        "긴 규정",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Body:         strings.Repeat("가", 20050),
	}
	service := &Service{Repo: testRepository(doc, nil), DomainLexicon: testDomainLexicon(t)}
	_, out, err := service.getRule(context.Background(), &mcpsdk.CallToolRequest{}, GetRuleInput{ID: doc.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated || out.TotalChars != 20050 || len([]rune(out.Content)) != 20000 {
		t.Fatalf("expected bounded content metadata: %#v", out)
	}
}

func TestGetRuleSupportsBoundedContinuation(t *testing.T) {
	doc := model.Document{ID: "rule-page", Title: "규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "가나다라마바사아자차"}
	service := &Service{Repo: testRepository(doc, nil), ReleaseGeneration: "generation-1"}
	_, first, err := service.getRule(context.Background(), &mcpsdk.CallToolRequest{}, GetRuleInput{ID: doc.ID, MaxChars: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Truncated || first.NextOffset != 4 || first.TotalChars != 10 || first.ReleaseGeneration != "generation-1" {
		t.Fatalf("bad first page: %#v", first)
	}
	_, second, err := service.getRule(context.Background(), &mcpsdk.CallToolRequest{}, GetRuleInput{ID: doc.ID, Offset: first.NextOffset, MaxChars: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(second.Content, "마바사아") || second.NextOffset != 8 {
		t.Fatalf("bad continuation page: %#v", second)
	}
	_, third, err := service.getRule(context.Background(), &mcpsdk.CallToolRequest{}, GetRuleInput{ID: doc.ID, Offset: second.NextOffset, MaxChars: 4})
	if err != nil {
		t.Fatal(err)
	}
	if third.Truncated || first.Content+second.Content+third.Content != doc.Body {
		t.Fatalf("pages do not reconstruct source exactly: first=%q second=%q third=%q", first.Content, second.Content, third.Content)
	}
}

func TestStructuredToolOutputByteLimit(t *testing.T) {
	if err := validateToolOutput(map[string]string{"content": "small"}, 100); err != nil {
		t.Fatalf("small output rejected: %v", err)
	}
	if err := validateToolOutput(map[string]string{"content": strings.Repeat("가", 100)}, 100); err == nil {
		t.Fatal("UTF-8 output larger than the byte limit was accepted")
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
		Engine:      searchindex.BuildWithAttachments([]model.Document{ko, en}, nil, nil),
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

func TestServiceRejectsUnsupportedLanguage(t *testing.T) {
	doc := model.Document{
		ID:           "rule-1",
		Title:        "코스닥시장 상장규정",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Language:     model.LanguageKorean,
		Body:         "상장 심사",
	}
	service := &Service{Repo: testRepository(doc, nil), DomainLexicon: testDomainLexicon(t)}
	_, _, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, SearchRulesInput{Query: "상장", Language: "jp", Limit: 5})
	if err == nil || !strings.Contains(err.Error(), "unsupported language") {
		t.Fatalf("expected unsupported language error, got %v", err)
	}
}

func TestSearchRulesValidatesContract(t *testing.T) {
	doc := model.Document{ID: "rule-1", Title: "규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "본문"}
	service := &Service{Repo: testRepository(doc, nil), Embedder: &stubEmbedder{vectors: [][]float64{{1}}}, MaxQueryRunes: 3}
	tests := []SearchRulesInput{
		{Query: ""},
		{Query: "1234"},
		{Query: "ok", DocumentType: "attachment"},
		{Query: "ok", EffectiveFrom: "2026-02-30"},
		{Query: "ok", EffectiveFrom: "2026-02-02", EffectiveTo: "2026-02-01"},
		{Query: "ok", Limit: 51},
	}
	for _, input := range tests {
		if _, _, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, input); err == nil {
			t.Fatalf("expected validation error for %#v", input)
		}
	}
}

func TestPublicDTOCarriesStructuredAnchorsWithoutInternalPaths(t *testing.T) {
	service := &Service{Repo: &searchindex.Repository{Attachments: map[string]searchindex.AttachmentDocument{
		"att-1": {Attachment: model.Attachment{ID: "att-1", RawPath: "/private/raw", TextPath: "/private/text", Error: "converter stderr"}},
	}}}
	dtos := service.searchResultDTOs([]searchindex.SearchResult{{
		ID:                "rule-1",
		Title:             "규정",
		MatchedChunkID:    "rule-1#0",
		MatchedChunkIndex: 0,
		ArticleID:         "제1조",
		HeadingPath:       []string{"제1장 총칙", "제1조(목적)"},
		AttachmentMatches: []searchindex.AttachmentMatch{{
			ID:          "att-1",
			ChunkID:     "rule-1#att-att-1-0",
			ChunkIndex:  0,
			ArticleID:   "별표 1",
			HeadingPath: []string{"별표 1", "산정식"},
		}},
	}})
	encoded, err := json.Marshal(dtos)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"article_range", "/private/raw", "/private/text", "converter stderr"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("public DTO leaked %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, `"matched_chunk_index":0`) || !strings.Contains(text, `"chunk_index":0`) {
		t.Fatalf("zero-based chunk indexes must remain explicit: %s", text)
	}
	for _, anchor := range []string{`"article_id":"제1조"`, `"heading_path":["제1장 총칙","제1조(목적)"]`, `"article_id":"별표 1"`} {
		if !strings.Contains(text, anchor) {
			t.Fatalf("public DTO omitted structured anchor %s: %s", anchor, text)
		}
	}
	chunks := service.chunkDTOs([]searchindex.ChunkContext{{
		ID:          "rule-1#0",
		DocumentID:  "rule-1",
		ArticleID:   "제1조",
		HeadingPath: []string{"제1장 총칙", "제1조(목적)"},
		Text:        "본문",
	}}, 100)
	encodedChunks, err := json.Marshal(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedChunks), `"article_id":"제1조"`) || !strings.Contains(string(encodedChunks), `"heading_path":["제1장 총칙","제1조(목적)"]`) {
		t.Fatalf("chunk DTO omitted structured anchors: %s", encodedChunks)
	}
}

func TestPublicDTOCarriesPerSourceQualityContract(t *testing.T) {
	searchable := true
	att := model.Attachment{
		ID:            "att-1",
		Status:        model.AttachmentConverted,
		Searchable:    &searchable,
		QualityStatus: "warning",
		QualityCodes:  []string{"image_content_unindexed"},
	}
	service := &Service{Repo: &searchindex.Repository{
		Documents: map[string]model.Document{},
		Attachments: map[string]searchindex.AttachmentDocument{
			att.ID: {Attachment: att},
		},
	}}
	dtos := service.searchResultDTOs([]searchindex.SearchResult{{
		ID:            "rule-1",
		MatchedSource: "attachment",
		AttachmentMatches: []searchindex.AttachmentMatch{{
			ID: att.ID,
		}},
	}})
	if len(dtos) != 1 || len(dtos[0].AttachmentMatches) != 1 {
		t.Fatalf("missing attachment match: %#v", dtos)
	}
	match := dtos[0].AttachmentMatches[0]
	if !match.Searchable || match.QualityNotice == nil || match.QualityNotice.Code != "attachment_text_degraded" {
		t.Fatalf("missing per-source quality warning: %#v", match)
	}
}

func TestListRulesReturnsTotal(t *testing.T) {
	docs := []model.Document{
		{ID: "rule-2", Title: "규정 2", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Language: model.LanguageKorean, Body: "본문"},
		{ID: "rule-1", Title: "규정 1", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Language: model.LanguageKorean, Body: "본문"},
	}
	repo := &searchindex.Repository{
		Documents:   map[string]model.Document{docs[0].ID: docs[0], docs[1].ID: docs[1]},
		Attachments: map[string]searchindex.AttachmentDocument{},
		Engine:      searchindex.BuildWithAttachments(docs, nil, nil),
	}
	service := &Service{Repo: repo, DomainLexicon: testDomainLexicon(t)}
	_, out, err := service.listRules(context.Background(), &mcpsdk.CallToolRequest{}, ListRulesInput{Language: "ko", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 2 || len(out.Documents) != 1 {
		t.Fatalf("bad list output: %#v", out)
	}
	encoded, err := json.Marshal(out.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"body"`) {
		t.Fatalf("list output should not expose body: %s", encoded)
	}
}

func TestListRecentChangesUsesDocumentedDefaultAndTotal(t *testing.T) {
	docs := make([]model.Document, 0, 25)
	docMap := make(map[string]model.Document, 25)
	for i := 0; i < 25; i++ {
		doc := model.Document{
			ID:           "rule-" + time.Unix(int64(i), 0).UTC().Format("150405"),
			Title:        "규정",
			CollectedAt:  time.Unix(int64(i), 0).UTC(),
			DocumentType: model.DocumentTypeRule,
			Body:         "본문",
		}
		docs = append(docs, doc)
		docMap[doc.ID] = doc
	}
	service := &Service{Repo: &searchindex.Repository{
		Documents: docMap,
		Engine:    searchindex.BuildWithAttachments(docs, nil, nil),
	}}
	_, out, err := service.listRecentChanges(context.Background(), &mcpsdk.CallToolRequest{}, RecentChangesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Limit != 20 || len(out.Documents) != 20 || out.Total != 25 || out.NextOffset != 20 {
		t.Fatalf("bad recent changes contract: %#v", out)
	}
}

func TestResourceTypeAndContentBounds(t *testing.T) {
	doc := model.Document{
		ID:           "notice-1",
		Title:        "예고",
		DocumentType: model.DocumentTypeNotice,
		Body:         strings.Repeat("가", maximumContentChars+10),
	}
	service := &Service{Repo: &searchindex.Repository{Documents: map[string]model.Document{doc.ID: doc}}, ReleaseGeneration: "gen"}
	wrongType := &mcpsdk.ReadResourceRequest{Params: &mcpsdk.ReadResourceParams{URI: "krx-rule://rules/notice-1"}}
	if _, err := service.readResource(context.Background(), wrongType); err == nil {
		t.Fatal("notice must not be readable through the rule resource template")
	}
	request := &mcpsdk.ReadResourceRequest{Params: &mcpsdk.ReadResourceParams{URI: "krx-rule://notices/notice-1"}}
	result, err := service.readResource(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Contents) != 1 || len([]rune(result.Contents[0].Text)) <= maximumContentChars || result.Contents[0].Meta["truncated"] != true {
		t.Fatalf("resource bound metadata missing: %#v", result)
	}
}

func TestAssetDTOAndContentNeverExposeLocalPaths(t *testing.T) {
	searchable := false
	documentAsset := model.Asset{
		ID: "document-chart", SourceKind: "html_inline",
		SourceAnchor: "html-img:https://example.test/dataFile/law/img/chart.png",
		SourceURL:    "https://example.test/dataFile/law/img/chart.png", ReferencePath: "assets/inline/private-chart.png",
		MIMEType: "image/png", RawFileHash: strings.Repeat("a", 64), Size: 24, Width: 2, Height: 3,
		PreservationStatus: "preserved", Searchable: &searchable, QualityCodes: []string{"image_content_unindexed"},
	}
	attachmentAsset := model.Asset{
		ID: "attachment-chart", SourceKind: "hwp_bindata", SourceAnchor: "hwp:BinData/BIN0001.png",
		ReferencePath: "../assets/attachment/private-chart.png", MIMEType: "image/png", RawFileHash: strings.Repeat("b", 64),
		Size: 24, Width: 4, Height: 5, PreservationStatus: "preserved", Searchable: &searchable,
		QualityCodes: []string{"image_content_unindexed"}, Error: "/private/producer/error",
	}
	attachment := model.Attachment{
		ID: "asset-attachment", Title: "자산 첨부", Status: model.AttachmentConverted,
		FileName: `C:\private\attachment.hwp`, SourceURL: "/home/private/attachment",
		RawPath: "/private/raw/attachment.hwp", TextPath: "/private/text/attachment.md",
		Assets: []model.Asset{attachmentAsset},
	}
	doc := model.Document{
		ID: "asset-rule", Title: "자산 규정", DocumentType: model.DocumentTypeRule, Language: model.LanguageKorean,
		SourceURL: "file:///home/private/source.html", FileName: "../../private/source.html",
		Body: "본문\n\n![문서 도표](assets/inline/private-chart.png)", RawPath: "/private/raw/source.html",
		Assets: []model.Asset{documentAsset}, Attachments: []model.Attachment{attachment},
	}
	attachmentDocument := searchindex.AttachmentDocument{
		Attachment: attachment,
		Text:       "첨부\n\n![첨부 도표](../assets/attachment/private-chart.png)",
	}
	repo := &searchindex.Repository{
		Documents:   map[string]model.Document{doc.ID: doc},
		Attachments: map[string]searchindex.AttachmentDocument{attachment.ID: attachmentDocument},
		Engine: searchindex.BuildWithAttachments(
			[]model.Document{doc},
			map[string]searchindex.AttachmentDocument{attachment.ID: attachmentDocument},
			nil,
		),
	}
	service := &Service{Repo: repo}

	_, rule, err := service.getRule(context.Background(), &mcpsdk.CallToolRequest{}, GetRuleInput{ID: doc.ID})
	if err != nil {
		t.Fatal(err)
	}
	assertPublicAssetPayload(t, rule, "krx-asset:document-chart")
	if len(rule.Document.Assets) != 1 || rule.Document.Assets[0].Reference != "krx-asset:document-chart" || rule.Document.AssetCount != 1 {
		t.Fatalf("document asset DTO = %#v", rule.Document)
	}

	_, attachmentOut, err := service.getAttachment(context.Background(), &mcpsdk.CallToolRequest{}, GetAttachmentInput{ID: attachment.ID})
	if err != nil {
		t.Fatal(err)
	}
	assertPublicAssetPayload(t, attachmentOut, "krx-asset:attachment-chart")
	if len(attachmentOut.Attachment.Assets) != 1 || attachmentOut.Attachment.Assets[0].Reference != "krx-asset:attachment-chart" {
		t.Fatalf("attachment asset DTO = %#v", attachmentOut.Attachment)
	}

	results := repo.Engine.Search(searchindex.SearchOptions{Query: "첨부 도표 BIN0001", Limit: 1})
	if len(results) != 1 {
		t.Fatalf("asset context search results = %#v", results)
	}
	_, contextOut, err := service.getContext(context.Background(), &mcpsdk.CallToolRequest{}, GetContextInput{ChunkID: results[0].MatchedChunkID})
	if err != nil {
		t.Fatal(err)
	}
	assertPublicAssetPayload(t, contextOut, "hwp:BinData/BIN0001.png")

	for _, uri := range []string{doc.URI(), attachment.URI()} {
		resource, err := service.readResource(context.Background(), &mcpsdk.ReadResourceRequest{Params: &mcpsdk.ReadResourceParams{URI: uri}})
		if err != nil {
			t.Fatal(err)
		}
		assertPublicAssetPayload(t, resource, "krx-asset:")
	}
}

func assertPublicAssetPayload(t *testing.T, value any, want string) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, want) {
		t.Fatalf("public asset payload %s does not contain %q", text, want)
	}
	for _, forbidden := range []string{"../assets/", "assets/inline/", "/private/", `"path"`, `"error"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("public asset payload leaks %q: %s", forbidden, text)
		}
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

func TestSearchRulesEmbeddingDeadlineFallsBackToBM25(t *testing.T) {
	doc := model.Document{ID: "rule-1", Title: "규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "상장 심사"}
	service := &Service{
		Repo:             testRepository(doc, map[string][]float64{"rule-1#0": {1}}),
		Embedder:         deadlineEmbedder{},
		EmbeddingTimeout: 5 * time.Millisecond,
	}
	_, out, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, SearchRulesInput{Query: "상장"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != "bm25" || len(out.Results) != 1 {
		t.Fatalf("expected BM25 deadline fallback: %#v", out)
	}
}

func TestSearchRulesConcurrencyIsBounded(t *testing.T) {
	doc := model.Document{ID: "rule-1", Title: "규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Body: "상장 심사"}
	embedder := &controlledEmbedder{entered: make(chan struct{}), release: make(chan struct{})}
	service := &Service{
		Repo:               testRepository(doc, map[string][]float64{"rule-1#0": {1}}),
		Embedder:           embedder,
		EmbeddingTimeout:   time.Second,
		ConcurrentSearches: make(chan struct{}, 1),
	}
	firstDone := make(chan error, 1)
	go func() {
		_, _, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, SearchRulesInput{Query: "상장"})
		firstDone <- err
	}()
	<-embedder.entered
	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, _, err := service.searchRules(waitCtx, &mcpsdk.CallToolRequest{}, SearchRulesInput{Query: "심사"}); err == nil || !strings.Contains(err.Error(), "waiting for capacity") {
		t.Fatalf("expected bounded search capacity error, got %v", err)
	}
	close(embedder.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first search failed: %v", err)
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

func TestSearchRulesRejectsInvalidQueryVectorsWithoutAdvertisingVectorMode(t *testing.T) {
	doc := model.Document{
		ID:           "rule-1",
		Title:        "코스닥시장 상장규정",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Body:         "상장신청인은 신규상장 심사를 신청할 수 있다.",
	}
	tests := []struct {
		name       string
		embedder   searchindex.Embedder
		wantReason string
	}{
		{name: "empty", embedder: &stubEmbedder{vectors: [][]float64{{}}}, wantReason: "empty_query_vector"},
		{name: "reported dimensions", embedder: &informedStubEmbedder{vectors: [][]float64{{1, 0}}, dimensions: 3}, wantReason: "query_vector_dimensions"},
		{name: "index dimensions", embedder: &stubEmbedder{vectors: [][]float64{{1}}}, wantReason: "query_vector_rejected"},
		{name: "non finite", embedder: &stubEmbedder{vectors: [][]float64{{math.NaN(), 0}}}, wantReason: "query_vector_non_finite"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observer := &recordingObserver{}
			service := &Service{
				Repo:     testRepository(doc, map[string][]float64{"rule-1#0": {1, 0}}),
				Embedder: test.embedder,
				Observer: observer,
			}
			_, out, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, SearchRulesInput{Query: "상장", Limit: 5})
			if err != nil {
				t.Fatal(err)
			}
			if out.Mode != "bm25" || len(out.Results) != 1 || out.Results[0].VectorScore != 0 {
				t.Fatalf("invalid vector changed the advertised search mode: %#v", out)
			}
			if len(observer.fallbacks) != 1 || observer.fallbacks[0] != test.wantReason {
				t.Fatalf("fallback reasons = %#v, want %q", observer.fallbacks, test.wantReason)
			}
		})
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
		Engine:      searchindex.BuildWithAttachments([]model.Document{doc}, nil, nil),
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
		Engine:      searchindex.BuildWithAttachments([]model.Document{doc}, nil, nil),
	}}
	_, out, err := service.getContext(context.Background(), &mcpsdk.CallToolRequest{}, GetContextInput{
		ChunkID:      "rule-context#1",
		BeforeChunks: intPtr(1),
		AfterChunks:  intPtr(1),
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
}

func TestGetContextAllowsZeroBeforeChunks(t *testing.T) {
	doc := model.Document{
		ID:           "rule-context-zero",
		Title:        "문맥 규정",
		CollectedAt:  time.Now(),
		DocumentType: model.DocumentTypeRule,
		Body:         strings.Repeat("이전 ", 600) + "\n\n목표 증거금\n\n" + strings.Repeat("다음 ", 600),
	}
	service := &Service{Repo: testRepository(doc, nil), DomainLexicon: testDomainLexicon(t)}
	results := service.Repo.Engine.Search(searchindex.SearchOptions{Query: "증거금", Limit: 1})
	if len(results) != 1 {
		t.Fatalf("missing search result: %#v", results)
	}
	_, out, err := service.getContext(context.Background(), &mcpsdk.CallToolRequest{}, GetContextInput{
		ChunkID:      results[0].MatchedChunkID,
		BeforeChunks: intPtr(0),
		AfterChunks:  intPtr(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Chunks) != 1 {
		t.Fatalf("expected only target chunk, got %#v", out.Chunks)
	}
}

func TestListCategories(t *testing.T) {
	docs := []model.Document{
		{ID: "rule-1", Title: "규정 1", Category: "업무규정 / 유가증권시장규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Language: model.LanguageKorean, Body: "본문"},
		{ID: "rule-2", Title: "규정 2", Category: "상장규정 / 코스닥시장규정", CollectedAt: time.Now(), DocumentType: model.DocumentTypeRule, Language: model.LanguageKorean, Body: "본문"},
	}
	repo := &searchindex.Repository{
		Documents:   map[string]model.Document{docs[0].ID: docs[0], docs[1].ID: docs[1]},
		Attachments: map[string]searchindex.AttachmentDocument{},
		Engine:      searchindex.BuildWithAttachments(docs, nil, nil),
	}
	service := &Service{Repo: repo, DomainLexicon: testDomainLexicon(t)}
	_, out, err := service.listCategories(context.Background(), &mcpsdk.CallToolRequest{}, ListCategoriesInput{Language: "ko"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 2 || out.Categories[0] != "상장규정 / 코스닥시장규정" {
		t.Fatalf("bad categories: %#v", out)
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
		Engine:      searchindex.BuildWithAttachments(nil, nil, nil),
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

func TestGetAttachmentReturnsOfficialSourceDescriptorWithoutLocalPath(t *testing.T) {
	sourceHash := model.HashText("official source")
	att := model.Attachment{
		ID:       "att-source",
		Title:    "별표",
		FileName: "appendix.hwp",
		MIMEType: "application/x-hwp",
		Status:   model.AttachmentConverted,
		RawPath:  "/private/raw/appendix.hwp",
		TextPath: "/private/text/appendix.md",
	}
	doc := model.Document{
		ID:                "rule-source",
		Title:             "규정",
		SourceURL:         "https://rule.krx.co.kr/out/regulation/regulationViewPop.do",
		SourceContentHash: sourceHash,
		SourceContentPath: "/private/raw/source.html",
		SourceRequestPath: "/private/raw/request.json",
		SourceRequest: model.SourceRequestDescriptor{
			Endpoint:          "/out/regulation/regulationViewPop.do",
			BookID:            "12345",
			NoFormYN:          "N",
			StateHistoryID:    "67890",
			SourceContentHash: sourceHash,
		},
		DocumentType: model.DocumentTypeRule,
		Body:         "본문",
		Attachments:  []model.Attachment{att},
	}
	service := &Service{Repo: &searchindex.Repository{
		Documents: map[string]model.Document{doc.ID: doc},
		Attachments: map[string]searchindex.AttachmentDocument{
			att.ID: {Attachment: att, Text: "첨부"},
		},
	}}
	_, out, err := service.getAttachment(context.Background(), &mcpsdk.CallToolRequest{}, GetAttachmentInput{ID: att.ID})
	if err != nil {
		t.Fatal(err)
	}
	if out.Attachment.SourceURL != doc.SourceURL || out.Attachment.ParentDocumentID != doc.ID || out.Attachment.ParentDocumentURI != doc.URI() {
		t.Fatalf("missing official source descriptor: %#v", out.Attachment)
	}
	official := out.Attachment.OfficialSource
	if official == nil || official.SourcePageURL != "https://rule.krx.co.kr/out/index.do" || official.Method != "POST" || official.Endpoint != doc.SourceURL || official.SourceContentHash != sourceHash {
		t.Fatalf("invalid official source descriptor: %#v", official)
	}
	if len(official.Parameters) != 3 || official.Parameters["bookid"] != "12345" || official.Parameters["noformyn"] != "N" || official.Parameters["statehistoryid"] != "67890" {
		t.Fatalf("invalid sanitized official source parameters: %#v", official.Parameters)
	}
	_, ruleOut, err := service.getRule(context.Background(), &mcpsdk.CallToolRequest{}, GetRuleInput{ID: doc.ID})
	if err != nil {
		t.Fatal(err)
	}
	if ruleOut.Document.OfficialSource == nil || ruleOut.Document.OfficialSource.Endpoint != doc.SourceURL {
		t.Fatalf("get_rule omitted official source: %#v", ruleOut.Document)
	}
	searchDTOs := service.searchResultDTOs([]searchindex.SearchResult{{ID: doc.ID, SourceURL: doc.SourceURL}})
	if len(searchDTOs) != 1 || searchDTOs[0].OfficialSource == nil {
		t.Fatalf("search result omitted official source: %#v", searchDTOs)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"/private/", "source_request_path", "source_content_path", "_csrf", "cookie", "authorization", "headers"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("source descriptor leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestOfficialSourceDescriptorSupportsNoticeRequest(t *testing.T) {
	hash := model.HashText("notice")
	dto := officialSourceDTO(model.Document{
		SourceURL: "https://rule.krx.co.kr/out/pds/pdsViewPop.do",
		SourceRequest: model.SourceRequestDescriptor{
			Endpoint:          "/out/pds/pdsViewPop.do",
			BBSID:             "notice-1",
			MenuID:            "10000016",
			SourceContentHash: hash,
		},
	})
	if dto == nil || dto.Endpoint != "https://rule.krx.co.kr/out/pds/pdsViewPop.do" || len(dto.Parameters) != 2 || dto.Parameters["BBSID"] != "notice-1" || dto.Parameters["Menuid"] != "10000016" {
		t.Fatalf("notice official source descriptor = %#v", dto)
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
		Engine:      searchindex.BuildWithAttachments(nil, nil, nil),
	}}

	_, out, err := service.getAttachment(context.Background(), &mcpsdk.CallToolRequest{}, GetAttachmentInput{ID: "att-formula-text"})
	if err != nil {
		t.Fatal(err)
	}
	if out.FormulaNotice != nil {
		t.Fatalf("formula-like text without preserved blocks should not emit user-facing notice: %#v", out.FormulaNotice)
	}
}

type stubEmbedder struct {
	calls   int
	vectors [][]float64
	inputs  [][]string
}

type informedStubEmbedder struct {
	vectors    [][]float64
	dimensions int
}

func (e *informedStubEmbedder) Embed(_ context.Context, _ []string) ([][]float64, error) {
	return e.vectors, nil
}

func (e *informedStubEmbedder) EmbeddingInfo() (string, int) {
	return "test", e.dimensions
}

type recordingObserver struct {
	fallbacks []string
}

func (*recordingObserver) ObserveTool(string, time.Duration) {}

func (o *recordingObserver) CountEmbeddingFallback(reason string) {
	o.fallbacks = append(o.fallbacks, reason)
}

type deadlineEmbedder struct{}

func (deadlineEmbedder) Embed(ctx context.Context, _ []string) ([][]float64, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type controlledEmbedder struct {
	entered chan struct{}
	release chan struct{}
}

func (e *controlledEmbedder) Embed(ctx context.Context, _ []string) ([][]float64, error) {
	select {
	case e.entered <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-e.release:
		return [][]float64{{1}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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
		Engine: searchindex.BuildWithAttachments([]model.Document{doc}, nil, vectors),
	}
}

func intPtr(value int) *int {
	return &value
}

func testDomainLexicon(t *testing.T) []searchindex.DomainLexiconEntry {
	t.Helper()
	entries, err := searchindex.LoadDomainLexicon(filepath.Join("..", "..", searchindex.DefaultDomainLexiconPath))
	if err != nil {
		t.Fatalf("load domain lexicon: %v", err)
	}
	return entries
}
