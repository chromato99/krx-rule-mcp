package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	"github.com/chromato99/krx-rule-mcp/internal/model"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Service struct {
	Repo     *searchindex.Repository
	Embedder searchindex.Embedder
	Logger   *slog.Logger
}

type SearchRulesInput struct {
	Query         string `json:"query" jsonschema:"Full-text Korean or English query."`
	DocumentType  string `json:"document_type,omitempty" jsonschema:"Optional document type: rule or notice."`
	Language      string `json:"language,omitempty" jsonschema:"Optional language filter: ko or en."`
	Category      string `json:"category,omitempty" jsonschema:"Optional exact category filter."`
	EffectiveFrom string `json:"effective_from,omitempty" jsonschema:"Optional YYYY-MM-DD effective date lower bound."`
	EffectiveTo   string `json:"effective_to,omitempty" jsonschema:"Optional YYYY-MM-DD effective date upper bound."`
	Limit         int    `json:"limit,omitempty" jsonschema:"Maximum number of results, default 10, max 50."`
}

type SearchRulesOutput struct {
	Mode    string                     `json:"mode"`
	Results []searchindex.SearchResult `json:"results"`
}

type GetRuleInput struct {
	ID       string `json:"id" jsonschema:"Rule or notice id."`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"Optional maximum content characters."`
}

type RuleOutput struct {
	Document model.Document `json:"document"`
	Content  string         `json:"content"`
}

type ListRulesInput struct {
	DocumentType string `json:"document_type,omitempty" jsonschema:"Optional document type: rule or notice."`
	Language     string `json:"language,omitempty" jsonschema:"Optional language filter: ko or en."`
	Category     string `json:"category,omitempty" jsonschema:"Optional exact category filter."`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum number of documents, default 50, max 200."`
	Offset       int    `json:"offset,omitempty" jsonschema:"Offset for pagination."`
}

type ListRulesOutput struct {
	Documents []model.Document `json:"documents"`
}

type GetAttachmentInput struct {
	ID       string `json:"id" jsonschema:"Attachment id."`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"Optional maximum text characters."`
}

type AttachmentOutput struct {
	Attachment    model.Attachment     `json:"attachment"`
	Content       string               `json:"content,omitempty"`
	FormulaNotice *model.FormulaNotice `json:"formula_notice,omitempty"`
}

type RecentChangesInput struct {
	DocumentType string `json:"document_type,omitempty" jsonschema:"Optional document type: rule or notice."`
	Language     string `json:"language,omitempty" jsonschema:"Optional language filter: ko or en."`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum number of documents, default 20."`
}

func NewServer(service *Service, version string) *mcpsdk.Server {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:       "krx-rule-mcp",
		Title:      "KRX Rule MCP",
		Version:    version,
		WebsiteURL: "https://rule.krx.co.kr/out/index.do",
	}, &mcpsdk.ServerOptions{
		Instructions: "Use this server to search and read public Korea Exchange rule documents and rule amendment notices collected from the KRX legal portal.",
		Logger:       service.Logger,
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "search_rules",
		Title:       "Search KRX rules",
		Description: "Search public KRX rules and amendment notices. BM25 is always available; vector search is used when configured and indexed.",
	}, service.searchRules)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "get_rule",
		Title:       "Get KRX rule",
		Description: "Read a collected KRX rule or amendment notice by id.",
	}, service.getRule)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "list_rules",
		Title:       "List KRX rules",
		Description: "List collected KRX rules or notices with metadata filters.",
	}, service.listRules)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "get_attachment",
		Title:       "Get KRX attachment",
		Description: "Read converted attachment text and conversion metadata by attachment id.",
	}, service.getAttachment)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "list_recent_changes",
		Title:       "List recent KRX changes",
		Description: "List most recent collected rules or amendment notices.",
	}, service.listRecentChanges)

	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		Name:        "krx-rules",
		Title:       "KRX rule resource",
		URITemplate: "krx-rule://rules/{id}",
		MIMEType:    "text/markdown",
		Description: "Collected KRX rule Markdown.",
	}, service.readResource)
	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		Name:        "krx-notices",
		Title:       "KRX notice resource",
		URITemplate: "krx-rule://notices/{id}",
		MIMEType:    "text/markdown",
		Description: "Collected KRX amendment notice Markdown.",
	}, service.readResource)
	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		Name:        "krx-attachments",
		Title:       "KRX attachment resource",
		URITemplate: "krx-rule://attachments/{id}",
		MIMEType:    "text/markdown",
		Description: "Converted KRX attachment text.",
	}, service.readResource)
	return server
}

func (s *Service) searchRules(ctx context.Context, _ *mcpsdk.CallToolRequest, in SearchRulesInput) (*mcpsdk.CallToolResult, SearchRulesOutput, error) {
	if strings.TrimSpace(in.Query) == "" && s.Embedder == nil {
		return nil, SearchRulesOutput{}, fmt.Errorf("query is required")
	}
	filter := searchindex.Filter{
		DocumentType:  parseDocumentType(in.DocumentType),
		Language:      parseLanguage(in.Language),
		Category:      in.Category,
		EffectiveFrom: in.EffectiveFrom,
		EffectiveTo:   in.EffectiveTo,
	}
	var queryVector []float64
	mode := "bm25"
	if s.Embedder != nil && s.Repo.Engine.HasVectors() && strings.TrimSpace(in.Query) != "" {
		vectors, err := s.Embedder.Embed(ctx, []string{in.Query})
		if err != nil {
			if s.Logger != nil {
				s.Logger.Warn("embedding query failed; falling back to BM25", "error", err)
			}
		} else if len(vectors) == 1 {
			queryVector = vectors[0]
			mode = "bm25+vector-rrf"
		}
	}
	results := s.Repo.Engine.Search(searchindex.SearchOptions{
		Query:       in.Query,
		Limit:       in.Limit,
		Filter:      filter,
		QueryVector: queryVector,
	})
	s.addFormulaNotices(results)
	return nil, SearchRulesOutput{Mode: mode, Results: results}, nil
}

func (s *Service) getRule(_ context.Context, _ *mcpsdk.CallToolRequest, in GetRuleInput) (*mcpsdk.CallToolResult, RuleOutput, error) {
	doc, ok := s.Repo.Documents[in.ID]
	if !ok {
		return nil, RuleOutput{}, fmt.Errorf("document %q not found", in.ID)
	}
	content := limitRunes(doc.Body, in.MaxChars)
	meta := doc
	meta.Body = ""
	return nil, RuleOutput{Document: meta, Content: content}, nil
}

func (s *Service) listRules(_ context.Context, _ *mcpsdk.CallToolRequest, in ListRulesInput) (*mcpsdk.CallToolResult, ListRulesOutput, error) {
	docs := s.Repo.Engine.Documents(searchindex.Filter{
		DocumentType: parseDocumentType(in.DocumentType),
		Language:     parseLanguage(in.Language),
		Category:     in.Category,
	}, in.Limit, in.Offset)
	for i := range docs {
		docs[i].Body = ""
	}
	return nil, ListRulesOutput{Documents: docs}, nil
}

func (s *Service) getAttachment(_ context.Context, _ *mcpsdk.CallToolRequest, in GetAttachmentInput) (*mcpsdk.CallToolResult, AttachmentOutput, error) {
	att, ok := s.Repo.Attachments[in.ID]
	if !ok {
		return nil, AttachmentOutput{}, fmt.Errorf("attachment %q not found", in.ID)
	}
	return nil, AttachmentOutput{
		Attachment:    att.Attachment,
		Content:       limitRunes(att.Text, in.MaxChars),
		FormulaNotice: formulaNoticeForAttachment(att),
	}, nil
}

func (s *Service) listRecentChanges(_ context.Context, _ *mcpsdk.CallToolRequest, in RecentChangesInput) (*mcpsdk.CallToolResult, ListRulesOutput, error) {
	docs := s.Repo.Engine.Recent(in.Limit, parseDocumentType(in.DocumentType), parseLanguage(in.Language))
	for i := range docs {
		docs[i].Body = ""
	}
	return nil, ListRulesOutput{Documents: docs}, nil
}

func (s *Service) readResource(_ context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
	uri := req.Params.URI
	switch {
	case strings.HasPrefix(uri, "krx-rule://rules/"), strings.HasPrefix(uri, "krx-rule://notices/"):
		id := uri[strings.LastIndex(uri, "/")+1:]
		doc, ok := s.Repo.Documents[id]
		if !ok {
			return nil, mcpsdk.ResourceNotFoundError(uri)
		}
		return &mcpsdk.ReadResourceResult{Contents: []*mcpsdk.ResourceContents{{
			URI:      uri,
			MIMEType: "text/markdown",
			Text:     doc.Body,
		}}}, nil
	case strings.HasPrefix(uri, "krx-rule://attachments/"):
		id := strings.TrimPrefix(uri, "krx-rule://attachments/")
		att, ok := s.Repo.Attachments[id]
		if !ok {
			return nil, mcpsdk.ResourceNotFoundError(uri)
		}
		return &mcpsdk.ReadResourceResult{Contents: []*mcpsdk.ResourceContents{{
			URI:      uri,
			MIMEType: "text/markdown",
			Text:     att.Text,
		}}}, nil
	default:
		return nil, mcpsdk.ResourceNotFoundError(uri)
	}
}

func (s *Service) addFormulaNotices(results []searchindex.SearchResult) {
	if s == nil || s.Repo == nil {
		return
	}
	for i := range results {
		for j := range results[i].AttachmentMatches {
			match := &results[i].AttachmentMatches[j]
			att, ok := s.Repo.Attachments[match.ID]
			if !ok {
				continue
			}
			notice := formulaNoticeForAttachment(att)
			if notice == nil {
				continue
			}
			match.FormulaNotice = notice
			if results[i].FormulaNotice == nil {
				results[i].FormulaNotice = notice
			}
		}
	}
}

func formulaNoticeForAttachment(att searchindex.AttachmentDocument) *model.FormulaNotice {
	sourceAvailable := strings.Contains(att.Text, "```hwp-equation")
	latexAvailable := strings.Contains(att.Text, "```math") || strings.Contains(att.Text, "LaTeX(best-effort)")
	count := att.Attachment.FormulaHintCount
	if count == 0 {
		count = int64(strings.Count(att.Text, "```hwp-equation"))
	}
	if count == 0 && !sourceAvailable && !latexAvailable {
		return nil
	}
	return &model.FormulaNotice{
		Severity:                "info",
		Code:                    "hwp_formula_latex_best_effort",
		Message:                 "This result contains HWP EqEdit formulas. LaTeX math blocks are best-effort conversions; verify exact formulas against the adjacent hwp-equation source or original HWP attachment.",
		SourceEquationAvailable: sourceAvailable,
		GeneratedLatexAvailable: latexAvailable,
		FormulaCount:            count,
	}
}

func parseDocumentType(value string) model.DocumentType {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "notice", "notices":
		return model.DocumentTypeNotice
	case "rule", "rules":
		return model.DocumentTypeRule
	default:
		return ""
	}
}

func parseLanguage(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "_", "-")))
	if value == "" {
		return ""
	}
	switch value {
	case "ko", "kor", "korean", "ko-kr":
		return model.LanguageKorean
	case "en", "eng", "english", "en-us", "en-gb":
		return model.LanguageEnglish
	default:
		return value
	}
}

func limitRunes(text string, max int) string {
	if max <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "\n\n[truncated]"
}
