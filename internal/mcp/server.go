package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"strings"
	"time"

	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	"github.com/chromato99/krx-rule-mcp/internal/model"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Service struct {
	Repo               *searchindex.Repository
	Embedder           searchindex.Embedder
	VectorRequired     bool
	DomainLexicon      []searchindex.DomainLexiconEntry
	Logger             *slog.Logger
	ReleaseGeneration  string
	MaxQueryRunes      int
	EmbeddingTimeout   time.Duration
	ConcurrentSearches chan struct{}
	Observer           Observer
	MaxToolOutputBytes int
}

type Observer interface {
	ObserveTool(name string, elapsed time.Duration)
	CountEmbeddingFallback(reason string)
}

const (
	defaultMaxQueryRunes   = 1000
	defaultContentChars    = 20000
	maximumContentChars    = 50000
	defaultToolOutputBytes = 512 << 10
)

// DocumentDTO is the stable public representation of a corpus document. It
// deliberately omits local paths, raw-file hashes, and conversion errors.
type DocumentDTO struct {
	ID              string             `json:"id"`
	Title           string             `json:"title"`
	Category        string             `json:"category,omitempty"`
	SourceURL       string             `json:"source_url"`
	OfficialSource  *OfficialSourceDTO `json:"official_source,omitempty"`
	EffectiveDate   string             `json:"effective_date,omitempty"`
	PublishedDate   string             `json:"published_date,omitempty"`
	CollectedAt     time.Time          `json:"collected_at"`
	ContentHash     string             `json:"content_hash,omitempty"`
	Searchable      bool               `json:"searchable"`
	QualityStatus   string             `json:"quality_status,omitempty"`
	QualityCodes    []string           `json:"quality_codes,omitempty"`
	QualityNotice   *QualityNotice     `json:"quality_notice,omitempty"`
	Language        string             `json:"language"`
	SourceID        string             `json:"source_id,omitempty"`
	FileName        string             `json:"file_name,omitempty"`
	DocumentType    model.DocumentType `json:"document_type"`
	URI             string             `json:"uri"`
	Assets          []AssetDTO         `json:"assets,omitempty"`
	AssetCount      int                `json:"asset_count,omitempty"`
	Attachments     []AttachmentDTO    `json:"attachments,omitempty"`
	AttachmentCount int                `json:"attachment_count,omitempty"`
}

type AttachmentDTO struct {
	ID                string                 `json:"id"`
	Title             string                 `json:"title"`
	FileName          string                 `json:"file_name,omitempty"`
	MIMEType          string                 `json:"mime_type,omitempty"`
	SourceURL         string                 `json:"source_url,omitempty"`
	OfficialSource    *OfficialSourceDTO     `json:"official_source,omitempty"`
	ParentDocumentID  string                 `json:"parent_document_id,omitempty"`
	ParentDocumentURI string                 `json:"parent_document_uri,omitempty"`
	ContentHash       string                 `json:"content_hash,omitempty"`
	Status            model.AttachmentStatus `json:"status,omitempty"`
	Searchable        bool                   `json:"searchable"`
	Size              int64                  `json:"size,omitempty"`
	QualityStatus     string                 `json:"quality_status,omitempty"`
	QualityScore      int                    `json:"quality_score,omitempty"`
	QualityCodes      []string               `json:"quality_codes,omitempty"`
	QualityNotice     *QualityNotice         `json:"quality_notice,omitempty"`
	Assets            []AssetDTO             `json:"assets,omitempty"`
	AssetCount        int                    `json:"asset_count,omitempty"`
	URI               string                 `json:"uri"`
}

// AssetDTO exposes preservation and source metadata without publishing the
// corpus-local path or producer error text. Reference is an opaque identifier;
// this server does not currently expose binary asset resources.
type AssetDTO struct {
	ID                 string   `json:"id"`
	SourceKind         string   `json:"source_kind"`
	SourceAnchor       string   `json:"source_anchor"`
	SourceURL          string   `json:"source_url,omitempty"`
	Reference          string   `json:"reference"`
	MIMEType           string   `json:"mime_type,omitempty"`
	RawFileHash        string   `json:"raw_file_hash,omitempty"`
	Size               int64    `json:"size,omitempty"`
	Width              int64    `json:"width,omitempty"`
	Height             int64    `json:"height,omitempty"`
	PreservationStatus string   `json:"preservation_status"`
	Searchable         bool     `json:"searchable"`
	QualityCodes       []string `json:"quality_codes,omitempty"`
}

type QualityNotice struct {
	Severity   string   `json:"severity"`
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	Codes      []string `json:"quality_codes,omitempty"`
	Searchable bool     `json:"searchable"`
}

// OfficialSourceDTO describes how to reopen the official KRX source without
// exposing collection credentials or local preservation paths. A fresh portal
// session/CSRF value may still be required by the official site.
type OfficialSourceDTO struct {
	SourcePageURL     string            `json:"source_page_url"`
	Method            string            `json:"method"`
	Endpoint          string            `json:"endpoint"`
	Parameters        map[string]string `json:"parameters"`
	SourceContentHash string            `json:"source_content_hash"`
}

type SearchResultDTO struct {
	ID                string               `json:"id"`
	Title             string               `json:"title"`
	Category          string               `json:"category,omitempty"`
	DocumentType      model.DocumentType   `json:"document_type"`
	Language          string               `json:"language"`
	SourceID          string               `json:"source_id,omitempty"`
	SourceURL         string               `json:"source_url"`
	OfficialSource    *OfficialSourceDTO   `json:"official_source,omitempty"`
	EffectiveDate     string               `json:"effective_date,omitempty"`
	PublishedDate     string               `json:"published_date,omitempty"`
	Score             float64              `json:"score"`
	Searchable        bool                 `json:"searchable"`
	BM25Score         float64              `json:"bm25_score,omitempty"`
	VectorScore       float64              `json:"vector_score,omitempty"`
	Snippet           string               `json:"snippet,omitempty"`
	MatchedSource     string               `json:"matched_source,omitempty"`
	MatchedChunkID    string               `json:"matched_chunk_id,omitempty"`
	MatchedChunkIndex int                  `json:"matched_chunk_index"`
	ArticleID         string               `json:"article_id,omitempty"`
	HeadingPath       []string             `json:"heading_path,omitempty"`
	AttachmentMatches []AttachmentMatchDTO `json:"attachment_matches,omitempty"`
	FormulaNotice     *model.FormulaNotice `json:"formula_notice,omitempty"`
	QualityNotice     *QualityNotice       `json:"quality_notice,omitempty"`
	URI               string               `json:"uri"`
}

type AttachmentMatchDTO struct {
	ID            string                 `json:"id"`
	Title         string                 `json:"title"`
	FileName      string                 `json:"file_name,omitempty"`
	SourceURL     string                 `json:"source_url,omitempty"`
	URI           string                 `json:"uri"`
	Status        model.AttachmentStatus `json:"status,omitempty"`
	Searchable    bool                   `json:"searchable"`
	ChunkID       string                 `json:"chunk_id,omitempty"`
	ChunkIndex    int                    `json:"chunk_index"`
	ArticleID     string                 `json:"article_id,omitempty"`
	HeadingPath   []string               `json:"heading_path,omitempty"`
	Score         float64                `json:"score,omitempty"`
	Snippet       string                 `json:"snippet,omitempty"`
	QualityNotice *QualityNotice         `json:"quality_notice,omitempty"`
	FormulaNotice *model.FormulaNotice   `json:"formula_notice,omitempty"`
}

type ChunkDTO struct {
	ID               string                 `json:"id"`
	DocumentID       string                 `json:"document_id"`
	Index            int                    `json:"index"`
	Source           string                 `json:"source"`
	URI              string                 `json:"uri"`
	AttachmentID     string                 `json:"attachment_id,omitempty"`
	AttachmentTitle  string                 `json:"attachment_title,omitempty"`
	AttachmentFile   string                 `json:"attachment_file,omitempty"`
	AttachmentStatus model.AttachmentStatus `json:"attachment_status,omitempty"`
	ArticleID        string                 `json:"article_id,omitempty"`
	HeadingPath      []string               `json:"heading_path,omitempty"`
	Searchable       bool                   `json:"searchable"`
	QualityNotice    *QualityNotice         `json:"quality_notice,omitempty"`
	Text             string                 `json:"text"`
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
	ReleaseGeneration string                            `json:"release_generation,omitempty"`
	Mode              string                            `json:"mode"`
	ScoreNote         string                            `json:"score_note"`
	QueryExpansion    *searchindex.DomainQueryExpansion `json:"query_expansion,omitempty"`
	Results           []SearchResultDTO                 `json:"results"`
}

type GetRuleInput struct {
	ID       string `json:"id" jsonschema:"Rule or notice id."`
	Offset   int    `json:"offset,omitempty" jsonschema:"Character offset for pagination, default 0."`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"Maximum content characters, default 20000, max 50000."`
}

type RuleOutput struct {
	ReleaseGeneration string      `json:"release_generation,omitempty"`
	Document          DocumentDTO `json:"document"`
	Content           string      `json:"content"`
	Offset            int         `json:"offset"`
	NextOffset        int         `json:"next_offset,omitempty"`
	TotalChars        int         `json:"total_chars"`
	Truncated         bool        `json:"truncated"`
}

type GetContextInput struct {
	ChunkID      string `json:"chunk_id" jsonschema:"Chunk id returned by search_rules as matched_chunk_id or attachment_matches[].chunk_id."`
	BeforeChunks *int   `json:"before_chunks,omitempty" jsonschema:"Number of previous chunks from the same document body or attachment, default 1, max 5. Set 0 for no previous chunks."`
	AfterChunks  *int   `json:"after_chunks,omitempty" jsonschema:"Number of following chunks from the same document body or attachment, default 1, max 5. Set 0 for no following chunks."`
	MaxChars     int    `json:"max_chars,omitempty" jsonschema:"Maximum combined context characters, default 20000, max 50000."`
}

type ContextOutput struct {
	ReleaseGeneration string               `json:"release_generation,omitempty"`
	Document          DocumentDTO          `json:"document"`
	Chunks            []ChunkDTO           `json:"chunks"`
	Content           string               `json:"content"`
	TotalChars        int                  `json:"total_chars"`
	Truncated         bool                 `json:"truncated"`
	FormulaNotice     *model.FormulaNotice `json:"formula_notice,omitempty"`
}

type ListRulesInput struct {
	DocumentType string `json:"document_type,omitempty" jsonschema:"Optional document type: rule or notice."`
	Language     string `json:"language,omitempty" jsonschema:"Optional language filter: ko or en."`
	Category     string `json:"category,omitempty" jsonschema:"Optional exact category filter."`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum number of documents, default 50, max 200."`
	Offset       int    `json:"offset,omitempty" jsonschema:"Offset for pagination."`
}

type ListRulesOutput struct {
	ReleaseGeneration string        `json:"release_generation,omitempty"`
	Documents         []DocumentDTO `json:"documents"`
	Total             int           `json:"total"`
	Limit             int           `json:"limit"`
	Offset            int           `json:"offset"`
	NextOffset        int           `json:"next_offset,omitempty"`
}

type GetAttachmentInput struct {
	ID       string `json:"id" jsonschema:"Attachment id."`
	Offset   int    `json:"offset,omitempty" jsonschema:"Character offset for pagination, default 0."`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"Maximum text characters, default 20000, max 50000."`
}

type AttachmentOutput struct {
	ReleaseGeneration string               `json:"release_generation,omitempty"`
	Attachment        AttachmentDTO        `json:"attachment"`
	Content           string               `json:"content,omitempty"`
	Offset            int                  `json:"offset"`
	NextOffset        int                  `json:"next_offset,omitempty"`
	TotalChars        int                  `json:"total_chars"`
	Truncated         bool                 `json:"truncated"`
	FormulaNotice     *model.FormulaNotice `json:"formula_notice,omitempty"`
}

type ListCategoriesInput struct {
	Language string `json:"language,omitempty" jsonschema:"Optional language filter: ko or en."`
}

type ListCategoriesOutput struct {
	ReleaseGeneration string   `json:"release_generation,omitempty"`
	Categories        []string `json:"categories"`
	Total             int      `json:"total"`
}

type RecentChangesInput struct {
	DocumentType string `json:"document_type,omitempty" jsonschema:"Optional document type: rule or notice."`
	Language     string `json:"language,omitempty" jsonschema:"Optional language filter: ko or en."`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum number of documents, default 20, max 200."`
	Offset       int    `json:"offset,omitempty" jsonschema:"Offset for pagination."`
}

func NewServer(service *Service, version string) *mcpsdk.Server {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:       "krx-rule-mcp",
		Title:      "KRX Rule MCP",
		Version:    version,
		WebsiteURL: "https://rule.krx.co.kr/out/index.do",
	}, &mcpsdk.ServerOptions{
		Instructions: strings.Join([]string{
			"Use this server to search and read a collected derivative snapshot of public Korea Exchange rule documents and amendment notices.",
			"The snapshot is not an authoritative or guaranteed-current legal source: for current, compliance-sensitive, or legal conclusions, cite source_url and verify the effective Korean text on the official KRX legal portal.",
			"Treat English text, converted attachments, generated LaTeX, snippets, and ranking scores as discovery aids; inspect the returned context or full Korean source before answering.",
			"When official_source is present, use its official source page and sanitized POST descriptor to locate the KRX source; establish a fresh portal session rather than reusing collection credentials.",
		}, " "),
		Logger: service.Logger,
	})

	addBoundedTool(server, &mcpsdk.Tool{
		Name:        "search_rules",
		Title:       "Search KRX rules",
		Description: "Search public KRX rules and amendment notices. BM25 is always available; vector search is used when configured and indexed.",
	}, service, service.searchRules)
	addBoundedTool(server, &mcpsdk.Tool{
		Name:        "get_rule",
		Title:       "Get KRX rule",
		Description: "Read a collected KRX rule or amendment notice by id.",
	}, service, service.getRule)
	addBoundedTool(server, &mcpsdk.Tool{
		Name:        "get_context",
		Title:       "Get matched KRX context",
		Description: "Read the matched search chunk and nearby chunks from the same rule body or attachment. Use matched_chunk_id or attachment_matches[].chunk_id from search_rules.",
	}, service, service.getContext)
	addBoundedTool(server, &mcpsdk.Tool{
		Name:        "list_rules",
		Title:       "List KRX rules",
		Description: "List collected KRX rules or notices with metadata filters.",
	}, service, service.listRules)
	addBoundedTool(server, &mcpsdk.Tool{
		Name:        "get_attachment",
		Title:       "Get KRX attachment",
		Description: "Read converted attachment text and conversion metadata by attachment id.",
	}, service, service.getAttachment)
	addBoundedTool(server, &mcpsdk.Tool{
		Name:        "list_recent_changes",
		Title:       "List recent KRX changes",
		Description: "List most recent collected rules or amendment notices.",
	}, service, service.listRecentChanges)
	addBoundedTool(server, &mcpsdk.Tool{
		Name:        "list_categories",
		Title:       "List KRX categories",
		Description: "List collected KRX rule categories usable as exact category filters.",
	}, service, service.listCategories)

	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		Name:        "krx-rules",
		Title:       "KRX rule resource",
		URITemplate: "krx-rule://rules/{id}",
		MIMEType:    "text/markdown",
		Description: "Collected KRX rule Markdown, bounded to 50000 characters; use get_rule offset pagination when truncated.",
	}, service.readResource)
	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		Name:        "krx-notices",
		Title:       "KRX notice resource",
		URITemplate: "krx-rule://notices/{id}",
		MIMEType:    "text/markdown",
		Description: "Collected KRX amendment notice Markdown, bounded to 50000 characters; use get_rule offset pagination when truncated.",
	}, service.readResource)
	server.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		Name:        "krx-attachments",
		Title:       "KRX attachment resource",
		URITemplate: "krx-rule://attachments/{id}",
		MIMEType:    "text/markdown",
		Description: "Converted KRX attachment text, bounded to 50000 characters; use get_attachment offset pagination when truncated.",
	}, service.readResource)
	return server
}

func addBoundedTool[In, Out any](
	server *mcpsdk.Server,
	tool *mcpsdk.Tool,
	service *Service,
	handler func(context.Context, *mcpsdk.CallToolRequest, In) (*mcpsdk.CallToolResult, Out, error),
) {
	mcpsdk.AddTool(server, tool, func(ctx context.Context, req *mcpsdk.CallToolRequest, in In) (*mcpsdk.CallToolResult, Out, error) {
		result, out, err := handler(ctx, req, in)
		if err != nil {
			return result, out, err
		}
		limit := service.MaxToolOutputBytes
		if limit <= 0 {
			limit = defaultToolOutputBytes
		}
		if err := validateToolOutput(out, limit); err != nil {
			var zero Out
			return nil, zero, err
		}
		if result == nil {
			result = &mcpsdk.CallToolResult{}
		}
		// A nil Content slice makes the typed SDK repeat the entire structured
		// output as TextContent. An explicit empty slice keeps one authoritative
		// representation on the wire and makes the configured byte bound useful.
		if result.Content == nil {
			result.Content = []mcpsdk.Content{}
		}
		return result, out, nil
	})
}

func validateToolOutput(output any, maxBytes int) error {
	encoded, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("encode tool output for size check: %w", err)
	}
	if maxBytes > 0 && len(encoded) > maxBytes {
		return fmt.Errorf("tool output is %d bytes and exceeds the %d-byte structured output limit; request a smaller page", len(encoded), maxBytes)
	}
	return nil
}

func (s *Service) searchRules(ctx context.Context, _ *mcpsdk.CallToolRequest, in SearchRulesInput) (*mcpsdk.CallToolResult, SearchRulesOutput, error) {
	started := time.Now()
	defer func() { s.observeTool("search_rules", started) }()
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return nil, SearchRulesOutput{}, fmt.Errorf("query is required")
	}
	maxQueryRunes := s.MaxQueryRunes
	if maxQueryRunes <= 0 {
		maxQueryRunes = defaultMaxQueryRunes
	}
	if len([]rune(query)) > maxQueryRunes {
		return nil, SearchRulesOutput{}, fmt.Errorf("query exceeds maximum length of %d characters", maxQueryRunes)
	}
	if in.Limit < 0 || in.Limit > 50 {
		return nil, SearchRulesOutput{}, fmt.Errorf("limit must be between 1 and 50, or 0 for the default")
	}
	if err := validateDateRange(in.EffectiveFrom, in.EffectiveTo); err != nil {
		return nil, SearchRulesOutput{}, err
	}
	documentType, err := parseDocumentType(in.DocumentType)
	if err != nil {
		return nil, SearchRulesOutput{}, err
	}
	language, err := parseLanguage(in.Language)
	if err != nil {
		return nil, SearchRulesOutput{}, err
	}
	if err := s.acquireSearch(ctx); err != nil {
		return nil, SearchRulesOutput{}, err
	}
	defer s.releaseSearch()
	expansion := searchindex.ExpandDomainQueryWithLexicon(query, s.DomainLexicon)
	searchQuery := query
	var queryExpansion *searchindex.DomainQueryExpansion
	var tokenWeights map[string]float64
	if expansion.Applied() {
		searchQuery = expansion.ExpandedQuery
		tokenWeights = expansion.TokenWeights(0.4)
		queryExpansion = &expansion
	}
	filter := searchindex.Filter{
		DocumentType:  documentType,
		Language:      language,
		Category:      in.Category,
		EffectiveFrom: in.EffectiveFrom,
		EffectiveTo:   in.EffectiveTo,
	}
	var queryVector []float64
	queryVectorAdopted := false
	vectorAvailable := s.Embedder != nil && s.Repo != nil && s.Repo.Engine != nil && s.Repo.Engine.HasVectors()
	if s.VectorRequired && !vectorAvailable {
		return nil, SearchRulesOutput{}, fmt.Errorf("vector search is required but the embedder or vector index is unavailable")
	}
	if vectorAvailable && strings.TrimSpace(searchQuery) != "" {
		embeddingTimeout := s.EmbeddingTimeout
		if embeddingTimeout <= 0 {
			embeddingTimeout = 3 * time.Second
		}
		embedCtx, cancel := context.WithTimeout(ctx, embeddingTimeout)
		vectors, err := s.Embedder.Embed(embedCtx, []string{searchQuery})
		cancel()
		if err != nil {
			reason := "embedding_error"
			if errors.Is(err, context.DeadlineExceeded) {
				reason = "deadline"
			}
			if s.VectorRequired {
				if s.Logger != nil {
					s.Logger.Warn("required query embedding failed", "reason", reason, "error", err)
				}
				return nil, SearchRulesOutput{}, fmt.Errorf("required query embedding failed: %s", reason)
			}
			s.countEmbeddingFallback(reason)
			if s.Logger != nil {
				s.Logger.Warn("embedding query failed; falling back to BM25", "error", err)
			}
		} else if len(vectors) == 1 {
			if reason := validateQueryVector(s.Embedder, s.Repo.Engine, vectors[0]); reason != "" {
				if s.VectorRequired {
					return nil, SearchRulesOutput{}, fmt.Errorf("required query embedding is unusable: %s", reason)
				}
				s.countEmbeddingFallback(reason)
				if s.Logger != nil {
					s.Logger.Warn("embedding query returned an unusable vector; falling back to BM25", "reason", reason)
				}
			} else {
				queryVector = vectors[0]
				queryVectorAdopted = true
			}
		} else {
			if s.VectorRequired {
				return nil, SearchRulesOutput{}, fmt.Errorf("required query embedding returned %d vectors; expected 1", len(vectors))
			}
			s.countEmbeddingFallback("invalid_vector_count")
		}
	}
	results := s.Repo.Engine.Search(searchindex.SearchOptions{
		Query:        searchQuery,
		Limit:        in.Limit,
		Filter:       filter,
		QueryVector:  queryVector,
		TokenWeights: tokenWeights,
	})
	mode, vectorScored := refineSearchMode(queryExpansion != nil, queryVectorAdopted, results)
	if queryVectorAdopted && !vectorScored {
		s.countEmbeddingFallback("no_vector_scores")
	}
	s.addFormulaNotices(results)
	return nil, SearchRulesOutput{
		ReleaseGeneration: s.ReleaseGeneration,
		Mode:              mode,
		ScoreNote:         "Scores are ranking signals for ordering results; they are not confidence probabilities. Verify current Korean text at source_url for authoritative use.",
		QueryExpansion:    queryExpansion,
		Results:           s.searchResultDTOs(results),
	}, nil
}

func validateQueryVector(embedder searchindex.Embedder, engine *searchindex.Engine, vector []float64) string {
	if len(vector) == 0 {
		return "empty_query_vector"
	}
	if info, ok := embedder.(searchindex.EmbedderInfo); ok {
		_, expectedDimensions := info.EmbeddingInfo()
		if expectedDimensions > 0 && len(vector) != expectedDimensions {
			return "query_vector_dimensions"
		}
	}
	hasNonZeroFloat32 := false
	for _, value := range vector {
		value32 := float32(value)
		if math.IsNaN(value) || math.IsInf(value, 0) || math.IsInf(float64(value32), 0) {
			return "query_vector_non_finite"
		}
		if value32 != 0 {
			hasNonZeroFloat32 = true
		}
	}
	if !hasNonZeroFloat32 {
		return "query_vector_zero_norm"
	}
	if engine == nil || engine.ValidateQueryVector(vector) != nil {
		return "query_vector_rejected"
	}
	return ""
}

func refineSearchMode(expanded, queryVectorAdopted bool, results []searchindex.SearchResult) (string, bool) {
	hasBM25Score := false
	hasVectorScore := false
	for _, result := range results {
		if result.BM25Score > 0 {
			hasBM25Score = true
		}
		if result.VectorScore > 0 {
			hasVectorScore = true
		}
	}
	mode := "bm25"
	if queryVectorAdopted && hasVectorScore {
		if hasBM25Score {
			mode = "bm25+vector-rrf"
		} else {
			mode = "vector"
		}
	}
	if expanded {
		mode += "+domain-expansion"
	}
	return mode, queryVectorAdopted && hasVectorScore
}

func (s *Service) getRule(_ context.Context, _ *mcpsdk.CallToolRequest, in GetRuleInput) (*mcpsdk.CallToolResult, RuleOutput, error) {
	started := time.Now()
	defer func() { s.observeTool("get_rule", started) }()
	doc, ok := s.Repo.Documents[in.ID]
	if !ok {
		return nil, RuleOutput{}, fmt.Errorf("document %q not found", in.ID)
	}
	publicBody := model.PublicAssetText(doc.Body, doc.Assets)
	content, total, truncated, nextOffset, err := pageRunes(publicBody, in.Offset, in.MaxChars)
	if err != nil {
		return nil, RuleOutput{}, err
	}
	return nil, RuleOutput{
		ReleaseGeneration: s.ReleaseGeneration,
		Document:          documentDetailDTO(doc),
		Content:           content,
		Offset:            in.Offset,
		NextOffset:        nextOffset,
		TotalChars:        total,
		Truncated:         truncated,
	}, nil
}

func (s *Service) getContext(_ context.Context, _ *mcpsdk.CallToolRequest, in GetContextInput) (*mcpsdk.CallToolResult, ContextOutput, error) {
	started := time.Now()
	defer func() { s.observeTool("get_context", started) }()
	chunkID := strings.TrimSpace(in.ChunkID)
	if chunkID == "" {
		return nil, ContextOutput{}, fmt.Errorf("chunk_id is required")
	}
	before, err := boundedContextWindow(in.BeforeChunks, 1)
	if err != nil {
		return nil, ContextOutput{}, fmt.Errorf("before_chunks: %w", err)
	}
	after, err := boundedContextWindow(in.AfterChunks, 1)
	if err != nil {
		return nil, ContextOutput{}, fmt.Errorf("after_chunks: %w", err)
	}
	maxChars, err := normalizedMaxChars(in.MaxChars)
	if err != nil {
		return nil, ContextOutput{}, err
	}
	doc, chunks, ok := s.Repo.Engine.ContextAround(chunkID, before, after)
	if !ok {
		return nil, ContextOutput{}, fmt.Errorf("chunk %q not found", chunkID)
	}
	chunks = s.publicChunkContexts(chunks)
	rawContent := contextContent(chunks)
	content := limitRunes(rawContent, maxChars)
	totalChars := len([]rune(rawContent))
	var notice *model.FormulaNotice
	for _, chunk := range chunks {
		if chunk.AttachmentID == "" {
			continue
		}
		att, ok := s.Repo.Attachments[chunk.AttachmentID]
		if !ok {
			continue
		}
		if candidate := formulaNoticeForAttachment(att); candidate != nil {
			notice = candidate
			break
		}
	}
	return nil, ContextOutput{
		ReleaseGeneration: s.ReleaseGeneration,
		Document:          documentDTO(doc),
		Chunks:            s.chunkDTOs(chunks, maxChars),
		Content:           content,
		TotalChars:        totalChars,
		Truncated:         totalChars > maxChars,
		FormulaNotice:     notice,
	}, nil
}

func (s *Service) listRules(_ context.Context, _ *mcpsdk.CallToolRequest, in ListRulesInput) (*mcpsdk.CallToolResult, ListRulesOutput, error) {
	started := time.Now()
	defer func() { s.observeTool("list_rules", started) }()
	documentType, err := parseDocumentType(in.DocumentType)
	if err != nil {
		return nil, ListRulesOutput{}, err
	}
	language, err := parseLanguage(in.Language)
	if err != nil {
		return nil, ListRulesOutput{}, err
	}
	limit, offset, err := normalizedPage(in.Limit, in.Offset, 50, 200)
	if err != nil {
		return nil, ListRulesOutput{}, err
	}
	docs, total := s.Repo.Engine.DocumentsPage(searchindex.Filter{
		DocumentType: documentType,
		Language:     language,
		Category:     in.Category,
	}, limit, offset)
	return nil, ListRulesOutput{
		ReleaseGeneration: s.ReleaseGeneration,
		Documents:         documentDTOs(docs),
		Total:             total,
		Limit:             limit,
		Offset:            offset,
		NextOffset:        nextPageOffset(offset, len(docs), total),
	}, nil
}

func (s *Service) getAttachment(_ context.Context, _ *mcpsdk.CallToolRequest, in GetAttachmentInput) (*mcpsdk.CallToolResult, AttachmentOutput, error) {
	started := time.Now()
	defer func() { s.observeTool("get_attachment", started) }()
	att, ok := s.Repo.Attachments[in.ID]
	if !ok {
		return nil, AttachmentOutput{}, fmt.Errorf("attachment %q not found", in.ID)
	}
	publicText := model.PublicAssetText(att.Text, att.Attachment.Assets)
	content, total, truncated, nextOffset, err := pageRunes(publicText, in.Offset, in.MaxChars)
	if err != nil {
		return nil, AttachmentOutput{}, err
	}
	return nil, AttachmentOutput{
		ReleaseGeneration: s.ReleaseGeneration,
		Attachment:        s.attachmentDTO(att.Attachment),
		Content:           content,
		Offset:            in.Offset,
		NextOffset:        nextOffset,
		TotalChars:        total,
		Truncated:         truncated,
		FormulaNotice:     formulaNoticeForAttachment(att),
	}, nil
}

func (s *Service) listCategories(_ context.Context, _ *mcpsdk.CallToolRequest, in ListCategoriesInput) (*mcpsdk.CallToolResult, ListCategoriesOutput, error) {
	started := time.Now()
	defer func() { s.observeTool("list_categories", started) }()
	language, err := parseLanguage(in.Language)
	if err != nil {
		return nil, ListCategoriesOutput{}, err
	}
	categories := s.Repo.Engine.Categories(language)
	return nil, ListCategoriesOutput{ReleaseGeneration: s.ReleaseGeneration, Categories: categories, Total: len(categories)}, nil
}

func (s *Service) listRecentChanges(_ context.Context, _ *mcpsdk.CallToolRequest, in RecentChangesInput) (*mcpsdk.CallToolResult, ListRulesOutput, error) {
	started := time.Now()
	defer func() { s.observeTool("list_recent_changes", started) }()
	documentType, err := parseDocumentType(in.DocumentType)
	if err != nil {
		return nil, ListRulesOutput{}, err
	}
	language, err := parseLanguage(in.Language)
	if err != nil {
		return nil, ListRulesOutput{}, err
	}
	limit, offset, err := normalizedPage(in.Limit, in.Offset, 20, 200)
	if err != nil {
		return nil, ListRulesOutput{}, err
	}
	docs, total := s.Repo.Engine.DocumentsPage(searchindex.Filter{DocumentType: documentType, Language: language}, limit, offset)
	return nil, ListRulesOutput{
		ReleaseGeneration: s.ReleaseGeneration,
		Documents:         documentDTOs(docs),
		Total:             total,
		Limit:             limit,
		Offset:            offset,
		NextOffset:        nextPageOffset(offset, len(docs), total),
	}, nil
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
		if strings.HasPrefix(uri, "krx-rule://rules/") && doc.DocumentType != model.DocumentTypeRule {
			return nil, mcpsdk.ResourceNotFoundError(uri)
		}
		if strings.HasPrefix(uri, "krx-rule://notices/") && doc.DocumentType != model.DocumentTypeNotice {
			return nil, mcpsdk.ResourceNotFoundError(uri)
		}
		publicBody := model.PublicAssetText(doc.Body, doc.Assets)
		return &mcpsdk.ReadResourceResult{Contents: []*mcpsdk.ResourceContents{{
			URI:      uri,
			MIMEType: "text/markdown",
			Text:     boundedResourceText(publicBody),
			Meta:     resourceMeta(s.ReleaseGeneration, publicBody),
		}}}, nil
	case strings.HasPrefix(uri, "krx-rule://attachments/"):
		id := strings.TrimPrefix(uri, "krx-rule://attachments/")
		att, ok := s.Repo.Attachments[id]
		if !ok {
			return nil, mcpsdk.ResourceNotFoundError(uri)
		}
		publicText := model.PublicAssetText(att.Text, att.Attachment.Assets)
		return &mcpsdk.ReadResourceResult{Contents: []*mcpsdk.ResourceContents{{
			URI:      uri,
			MIMEType: "text/markdown",
			Text:     boundedResourceText(publicText),
			Meta:     resourceMeta(s.ReleaseGeneration, publicText),
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
	sourceCount := int64(strings.Count(att.Text, "```hwp-equation"))
	latexCount := int64(strings.Count(att.Text, "```math"))
	sourceAvailable := sourceCount > 0
	latexAvailable := latexCount > 0
	count := att.Attachment.FormulaBlockCount
	if count == 0 {
		count = sourceCount
	}
	if count == 0 {
		count = latexCount
	}
	if count == 0 && !sourceAvailable && !latexAvailable {
		return nil
	}
	if !sourceAvailable && !latexAvailable {
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

func boundedContextWindow(value *int, fallback int) (int, error) {
	actual := fallback
	if value != nil {
		actual = *value
	}
	if actual < 0 || actual > 5 {
		return 0, fmt.Errorf("must be between 0 and 5")
	}
	return actual, nil
}

func contextContent(chunks []searchindex.ChunkContext) string {
	var b strings.Builder
	for i, chunk := range chunks {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("<!-- chunk_id: ")
		b.WriteString(chunk.ID)
		b.WriteString(" source: ")
		b.WriteString(chunk.Source)
		if chunk.AttachmentID != "" {
			b.WriteString(" attachment_id: ")
			b.WriteString(chunk.AttachmentID)
		}
		b.WriteString(" -->\n")
		b.WriteString(chunk.Text)
	}
	return b.String()
}

func parseDocumentType(value string) (model.DocumentType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "", nil
	case "notice", "notices":
		return model.DocumentTypeNotice, nil
	case "rule", "rules":
		return model.DocumentTypeRule, nil
	default:
		return "", fmt.Errorf("unsupported document_type %q; expected rule or notice", value)
	}
}

func parseLanguage(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "_", "-")))
	if value == "" {
		return "", nil
	}
	switch value {
	case "ko", "kor", "korean", "ko-kr":
		return model.LanguageKorean, nil
	case "en", "eng", "english", "en-us", "en-gb":
		return model.LanguageEnglish, nil
	default:
		return "", fmt.Errorf("unsupported language %q; expected ko or en", value)
	}
}

func validateDateRange(from, to string) error {
	parse := func(field, value string) (time.Time, error) {
		if strings.TrimSpace(value) == "" {
			return time.Time{}, nil
		}
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s must use YYYY-MM-DD", field)
		}
		return parsed, nil
	}
	fromDate, err := parse("effective_from", from)
	if err != nil {
		return err
	}
	toDate, err := parse("effective_to", to)
	if err != nil {
		return err
	}
	if !fromDate.IsZero() && !toDate.IsZero() && fromDate.After(toDate) {
		return fmt.Errorf("effective_from must not be after effective_to")
	}
	return nil
}

func normalizedPage(limit, offset, fallback, maximum int) (int, int, error) {
	if limit < 0 || limit > maximum {
		return 0, 0, fmt.Errorf("limit must be between 1 and %d, or 0 for the default", maximum)
	}
	if offset < 0 {
		return 0, 0, fmt.Errorf("offset must not be negative")
	}
	if limit == 0 {
		limit = fallback
	}
	return limit, offset, nil
}

func nextPageOffset(offset, returned, total int) int {
	next := offset + returned
	if returned == 0 || next >= total {
		return 0
	}
	return next
}

func normalizedMaxChars(max int) (int, error) {
	if max < 0 || max > maximumContentChars {
		return 0, fmt.Errorf("max_chars must be between 1 and %d, or 0 for the default", maximumContentChars)
	}
	if max == 0 {
		return defaultContentChars, nil
	}
	return max, nil
}

func pageRunes(text string, offset, max int) (content string, total int, truncated bool, nextOffset int, err error) {
	if offset < 0 {
		return "", 0, false, 0, fmt.Errorf("offset must not be negative")
	}
	max, err = normalizedMaxChars(max)
	if err != nil {
		return "", 0, false, 0, err
	}
	runes := []rune(text)
	total = len(runes)
	if offset >= total {
		return "", total, false, 0, nil
	}
	end := offset + max
	if end > total {
		end = total
	}
	content = string(runes[offset:end])
	truncated = end < total
	if truncated {
		nextOffset = end
	}
	return content, total, truncated, nextOffset, nil
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

func (s *Service) acquireSearch(ctx context.Context) error {
	if s.ConcurrentSearches == nil {
		return nil
	}
	select {
	case s.ConcurrentSearches <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("search cancelled while waiting for capacity: %w", ctx.Err())
	}
}

func (s *Service) releaseSearch() {
	if s.ConcurrentSearches != nil {
		<-s.ConcurrentSearches
	}
}

func (s *Service) observeTool(name string, started time.Time) {
	if s != nil && s.Observer != nil {
		s.Observer.ObserveTool(name, time.Since(started))
	}
}

func (s *Service) countEmbeddingFallback(reason string) {
	if s != nil && s.Observer != nil {
		s.Observer.CountEmbeddingFallback(reason)
	}
}

func documentDTO(doc model.Document) DocumentDTO {
	return DocumentDTO{
		ID:              doc.ID,
		Title:           doc.Title,
		Category:        doc.Category,
		SourceURL:       publicDocumentSourceURL(doc.SourceURL),
		OfficialSource:  officialSourceDTO(doc),
		EffectiveDate:   doc.EffectiveDate,
		PublishedDate:   doc.PublishedDate,
		CollectedAt:     doc.CollectedAt,
		ContentHash:     doc.ContentHash,
		Searchable:      doc.IsSearchable(),
		QualityStatus:   doc.QualityStatus,
		QualityCodes:    doc.EffectiveQualityCodes(),
		QualityNotice:   documentQualityNotice(doc),
		Language:        doc.Language,
		SourceID:        doc.SourceID,
		FileName:        publicFileName(doc.FileName),
		DocumentType:    doc.DocumentType,
		URI:             doc.URI(),
		AssetCount:      len(doc.Assets),
		AttachmentCount: len(doc.Attachments),
	}
}

func documentDetailDTO(doc model.Document) DocumentDTO {
	dto := documentDTO(doc)
	dto.Assets = assetDTOs(doc.Assets)
	dto.Attachments = attachmentDTOs(doc.Attachments, doc)
	return dto
}

func documentDTOs(docs []model.Document) []DocumentDTO {
	out := make([]DocumentDTO, 0, len(docs))
	for _, doc := range docs {
		out = append(out, documentDTO(doc))
	}
	return out
}

func attachmentDTO(att model.Attachment, parent model.Document) AttachmentDTO {
	sourceURL := publicAttachmentSourceURL(att.SourceURL, parent.SourceURL)
	var parentURI string
	if parent.ID != "" {
		parentURI = parent.URI()
	}
	return AttachmentDTO{
		ID:                att.ID,
		Title:             att.Title,
		FileName:          publicFileName(att.FileName),
		MIMEType:          att.MIMEType,
		SourceURL:         sourceURL,
		OfficialSource:    officialSourceDTO(parent),
		ParentDocumentID:  parent.ID,
		ParentDocumentURI: parentURI,
		ContentHash:       att.ContentHash,
		Status:            att.Status,
		Searchable:        att.IsSearchable(),
		Size:              att.Size,
		QualityStatus:     att.QualityStatus,
		QualityScore:      att.QualityScore,
		QualityCodes:      att.EffectiveQualityCodes(),
		QualityNotice:     qualityNotice(att),
		Assets:            assetDTOs(att.Assets),
		AssetCount:        len(att.Assets),
		URI:               att.URI(),
	}
}

func assetDTOs(assets []model.Asset) []AssetDTO {
	out := make([]AssetDTO, 0, len(assets))
	for _, asset := range assets {
		searchable := false
		if asset.Searchable != nil {
			searchable = *asset.Searchable
		}
		out = append(out, AssetDTO{
			ID:                 asset.ID,
			SourceKind:         asset.SourceKind,
			SourceAnchor:       asset.SourceAnchor,
			SourceURL:          publicDocumentSourceURL(asset.SourceURL),
			Reference:          asset.Reference(),
			MIMEType:           asset.MIMEType,
			RawFileHash:        asset.RawFileHash,
			Size:               asset.Size,
			Width:              asset.Width,
			Height:             asset.Height,
			PreservationStatus: asset.PreservationStatus,
			Searchable:         searchable,
			QualityCodes:       asset.EffectiveQualityCodes(),
		})
	}
	return out
}

func attachmentDTOs(attachments []model.Attachment, parent model.Document) []AttachmentDTO {
	out := make([]AttachmentDTO, 0, len(attachments))
	for _, att := range attachments {
		out = append(out, attachmentDTO(att, parent))
	}
	return out
}

func officialSourceDTO(doc model.Document) *OfficialSourceDTO {
	request := doc.SourceRequest
	if strings.TrimSpace(request.Endpoint) == "" || strings.TrimSpace(request.SourceContentHash) == "" {
		return nil
	}
	endpoint := strings.TrimSpace(request.Endpoint)
	sourcePageURL := publicDocumentSourceURL(doc.SourceURL)
	if base, err := url.Parse(sourcePageURL); err == nil && base.IsAbs() && base.Host != "" {
		if reference, err := url.Parse(endpoint); err == nil {
			endpoint = base.ResolveReference(reference).String()
		}
		sourcePageURL = (&url.URL{Scheme: base.Scheme, Host: base.Host, Path: "/out/index.do"}).String()
	}
	parameters := map[string]string{}
	for key, value := range map[string]string{
		"bookid":         request.BookID,
		"noformyn":       request.NoFormYN,
		"statehistoryid": request.StateHistoryID,
		"BBSID":          request.BBSID,
		"Menuid":         request.MenuID,
	} {
		if value = strings.TrimSpace(value); value != "" {
			parameters[key] = value
		}
	}
	return &OfficialSourceDTO{
		SourcePageURL:     sourcePageURL,
		Method:            "POST",
		Endpoint:          endpoint,
		Parameters:        parameters,
		SourceContentHash: request.SourceContentHash,
	}
}

func publicDocumentSourceURL(value string) string {
	value, _ = model.SafeAbsoluteHTTPURL(value)
	return value
}

func publicAttachmentSourceURL(value, parent string) string {
	if value, ok := model.SafeAttachmentSourceURL(value); ok && value != "" {
		return value
	}
	return publicDocumentSourceURL(parent)
}

func publicFileName(value string) string {
	value, _ = model.PortableFileName(value)
	return value
}

func (s *Service) attachmentDTO(att model.Attachment) AttachmentDTO {
	if s != nil && s.Repo != nil {
		for _, doc := range s.Repo.Documents {
			for _, candidate := range doc.Attachments {
				if candidate.ID == att.ID {
					return attachmentDTO(att, doc)
				}
			}
		}
	}
	return attachmentDTO(att, model.Document{})
}

func qualityNotice(att model.Attachment) *QualityNotice {
	codes := att.EffectiveQualityCodes()
	searchable := att.IsSearchable()
	failed := !searchable || att.Status == model.AttachmentFailed || (att.Status != "" && att.Status != model.AttachmentConverted)
	degraded := failed || qualityStatusWarns(att.QualityStatus) || len(codes) > 0
	if !degraded {
		return nil
	}
	if failed || strings.EqualFold(att.QualityStatus, "fail") {
		return &QualityNotice{
			Severity:   "error",
			Code:       "attachment_text_unreliable",
			Message:    "Converted attachment text is unavailable or failed quality checks; verify the original attachment at source_url.",
			Codes:      codes,
			Searchable: false,
		}
	}
	return &QualityNotice{
		Severity:   "warning",
		Code:       "attachment_text_degraded",
		Message:    "Converted attachment text has quality warnings; verify important tables, images, and formulas against the original attachment at source_url.",
		Codes:      codes,
		Searchable: searchable,
	}
}

func documentQualityNotice(doc model.Document) *QualityNotice {
	codes := doc.EffectiveQualityCodes()
	searchable := doc.IsSearchable()
	if searchable && !qualityStatusWarns(doc.QualityStatus) && len(codes) == 0 {
		return nil
	}
	if !searchable || strings.EqualFold(doc.QualityStatus, "fail") || strings.EqualFold(doc.QualityStatus, "error") {
		return &QualityNotice{
			Severity:   "error",
			Code:       "document_text_unreliable",
			Message:    "Collected document text is not searchable or failed quality checks; verify the official Korean source at source_url.",
			Codes:      codes,
			Searchable: false,
		}
	}
	return &QualityNotice{
		Severity:   "warning",
		Code:       "document_text_degraded",
		Message:    "Collected document text has quality warnings; verify important content against the official Korean source at source_url.",
		Codes:      codes,
		Searchable: true,
	}
}

func qualityStatusWarns(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status != "" && status != "pass" && status != "passed" && status != "ok" && status != "good"
}

func (s *Service) searchResultDTOs(results []searchindex.SearchResult) []SearchResultDTO {
	out := make([]SearchResultDTO, 0, len(results))
	for _, result := range results {
		matches := make([]AttachmentMatchDTO, 0, len(result.AttachmentMatches))
		for _, match := range result.AttachmentMatches {
			var notice *QualityNotice
			searchable := true
			sourceURL := publicDocumentSourceURL(result.SourceURL)
			if att, ok := s.Repo.Attachments[match.ID]; ok {
				notice = qualityNotice(att.Attachment)
				searchable = att.Attachment.IsSearchable()
				sourceURL = publicAttachmentSourceURL(att.Attachment.SourceURL, result.SourceURL)
			}
			matches = append(matches, AttachmentMatchDTO{
				ID:            match.ID,
				Title:         match.Title,
				FileName:      publicFileName(match.FileName),
				SourceURL:     sourceURL,
				URI:           match.URI,
				Status:        match.Status,
				Searchable:    searchable,
				ChunkID:       match.ChunkID,
				ChunkIndex:    match.ChunkIndex,
				ArticleID:     match.ArticleID,
				HeadingPath:   append([]string(nil), match.HeadingPath...),
				Score:         match.Score,
				Snippet:       match.Snippet,
				QualityNotice: notice,
				FormulaNotice: match.FormulaNotice,
			})
		}
		var resultNotice *QualityNotice
		var resultOfficialSource *OfficialSourceDTO
		if doc, ok := s.Repo.Documents[result.ID]; ok {
			resultOfficialSource = officialSourceDTO(doc)
			if result.MatchedSource != "attachment" {
				resultNotice = documentQualityNotice(doc)
			}
		}
		out = append(out, SearchResultDTO{
			ID:                result.ID,
			Title:             result.Title,
			Category:          result.Category,
			DocumentType:      result.DocumentType,
			Language:          result.Language,
			SourceID:          result.SourceID,
			SourceURL:         publicDocumentSourceURL(result.SourceURL),
			OfficialSource:    resultOfficialSource,
			EffectiveDate:     result.EffectiveDate,
			PublishedDate:     result.PublishedDate,
			Score:             result.Score,
			Searchable:        true,
			BM25Score:         result.BM25Score,
			VectorScore:       result.VectorScore,
			Snippet:           result.Snippet,
			MatchedSource:     result.MatchedSource,
			MatchedChunkID:    result.MatchedChunkID,
			MatchedChunkIndex: result.MatchedChunkIndex,
			ArticleID:         result.ArticleID,
			HeadingPath:       append([]string(nil), result.HeadingPath...),
			AttachmentMatches: matches,
			FormulaNotice:     result.FormulaNotice,
			QualityNotice:     resultNotice,
			URI:               result.URI,
		})
	}
	return out
}

func (s *Service) chunkDTOs(chunks []searchindex.ChunkContext, maxChars int) []ChunkDTO {
	remaining := maxChars
	out := make([]ChunkDTO, 0, len(chunks))
	for _, chunk := range chunks {
		text := ""
		if remaining > 0 {
			runes := []rune(chunk.Text)
			used := len(runes)
			if used > remaining {
				used = remaining
			}
			text = string(runes[:used])
			if used < len(runes) {
				text += "\n\n[truncated]"
			}
			remaining -= used
		}
		var notice *QualityNotice
		searchable := true
		if chunk.AttachmentID != "" {
			if att, ok := s.Repo.Attachments[chunk.AttachmentID]; ok {
				notice = qualityNotice(att.Attachment)
				searchable = att.Attachment.IsSearchable()
			}
		} else if doc, ok := s.Repo.Documents[chunk.DocumentID]; ok {
			notice = documentQualityNotice(doc)
			searchable = doc.IsSearchable()
		}
		out = append(out, ChunkDTO{
			ID:               chunk.ID,
			DocumentID:       chunk.DocumentID,
			Index:            chunk.Index,
			Source:           chunk.Source,
			URI:              chunk.URI,
			AttachmentID:     chunk.AttachmentID,
			AttachmentTitle:  chunk.AttachmentTitle,
			AttachmentFile:   chunk.AttachmentFile,
			AttachmentStatus: chunk.AttachmentStatus,
			ArticleID:        chunk.ArticleID,
			HeadingPath:      append([]string(nil), chunk.HeadingPath...),
			Searchable:       searchable,
			QualityNotice:    notice,
			Text:             text,
		})
	}
	return out
}

func (s *Service) publicChunkContexts(chunks []searchindex.ChunkContext) []searchindex.ChunkContext {
	out := make([]searchindex.ChunkContext, len(chunks))
	copy(out, chunks)
	for i := range out {
		if out[i].AttachmentID != "" {
			if attachment, ok := s.Repo.Attachments[out[i].AttachmentID]; ok {
				out[i].Text = model.PublicAssetText(out[i].Text, attachment.Attachment.Assets)
			}
			continue
		}
		if document, ok := s.Repo.Documents[out[i].DocumentID]; ok {
			out[i].Text = model.PublicAssetText(out[i].Text, document.Assets)
		}
	}
	return out
}

func boundedResourceText(text string) string {
	return limitRunes(text, maximumContentChars)
}

func resourceMeta(generation, text string) mcpsdk.Meta {
	total := len([]rune(text))
	return mcpsdk.Meta{
		"release_generation": generation,
		"total_chars":        total,
		"truncated":          total > maximumContentChars,
		"max_chars":          maximumContentChars,
		"continuation_tool":  "Use get_rule or get_attachment with offset pagination for longer content.",
	}
}
