package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRealDataDomainLexiconProbe(t *testing.T) {
	if os.Getenv("KRX_DOMAIN_LEXICON_EVAL") != "1" {
		t.Skip("set KRX_DOMAIN_LEXICON_EVAL=1 to run collected-data domain lexicon probes")
	}
	dataRoot := os.Getenv("KRX_RULE_DATA_DIR")
	if dataRoot == "" {
		dataRoot = filepath.Join("..", "..", "data")
	}
	indexPath := realDataIndexPath()
	repo, err := searchindex.LoadRepository(dataRoot, indexPath)
	if err != nil {
		t.Fatalf("load repository: %v", err)
	}
	service := &Service{Repo: repo, DomainLexicon: testDomainLexicon(t)}
	cases := []struct {
		name          string
		query         string
		language      string
		documentType  string
		wantExpansion string
		wantTitle     string
	}{
		{
			name:          "curated realtime price limit alias",
			query:         "동적상하한가",
			language:      "ko",
			documentType:  "rule",
			wantExpansion: "derivatives_realtime_price_limit",
			wantTitle:     "파생상품시장 업무규정 시행세칙",
		},
		{
			name:          "official realtime price limit phrasing",
			query:         "실시간 상하한가 가격변동폭",
			language:      "ko",
			documentType:  "rule",
			wantExpansion: "derivatives_realtime_price_limit",
			wantTitle:     "파생상품시장 업무규정 시행세칙",
		},
		{
			name:          "english daily price limit",
			query:         "daily price limit kospi 200 futures",
			documentType:  "rule",
			wantExpansion: "price_limit",
			wantTitle:     "파생상품시장 업무규정 시행세칙",
		},
		{
			name:          "listing review bilingual",
			query:         "listing review preliminary",
			documentType:  "rule",
			wantExpansion: "listing_review",
			wantTitle:     "Listing Regulation",
		},
		{
			name:          "disclosure english",
			query:         "disclosure timely disclosure",
			documentType:  "rule",
			wantExpansion: "disclosure",
			wantTitle:     "공시",
		},
		{
			name:          "margin bilingual",
			query:         "margin collateral 증거금",
			documentType:  "rule",
			wantExpansion: "margin_collateral",
			wantTitle:     "파생상품시장",
		},
		{
			name:          "clearing settlement",
			query:         "clearing settlement 최종결제가격",
			documentType:  "rule",
			wantExpansion: "clearing_settlement",
			wantTitle:     "청산",
		},
		{
			name:          "etf liquidity provider",
			query:         "LP 유동성공급자 의무",
			language:      "ko",
			documentType:  "rule",
			wantExpansion: "etf_liquidity_provider",
			wantTitle:     "상장규정",
		},
		{
			name:          "etf nav premium discount",
			query:         "NAV 괴리율 순자산가치",
			language:      "ko",
			documentType:  "rule",
			wantExpansion: "etf_nav",
			wantTitle:     "상장규정",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, out, err := service.searchRules(context.Background(), &mcpsdk.CallToolRequest{}, SearchRulesInput{
				Query:        tc.query,
				Language:     tc.language,
				DocumentType: tc.documentType,
				Limit:        5,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("query=%q mode=%s expansion=%s results=%s", tc.query, out.Mode, expansionIDs(out.QueryExpansion), resultTitles(out.Results))
			if !expansionHas(out.QueryExpansion, tc.wantExpansion) {
				t.Fatalf("missing expansion %q: %#v", tc.wantExpansion, out.QueryExpansion)
			}
			if !resultsContainTitle(out.Results, tc.wantTitle) {
				t.Fatalf("missing result title containing %q: %#v", tc.wantTitle, out.Results)
			}
		})
	}
}

func expansionHas(expansion *searchindex.DomainQueryExpansion, id string) bool {
	if expansion == nil {
		return false
	}
	for _, term := range expansion.AppliedTerms {
		if term.ID == id {
			return true
		}
	}
	return false
}

func expansionIDs(expansion *searchindex.DomainQueryExpansion) string {
	if expansion == nil {
		return ""
	}
	var ids []string
	for _, term := range expansion.AppliedTerms {
		ids = append(ids, term.ID)
	}
	return strings.Join(ids, ",")
}

func resultTitles(results []SearchResultDTO) string {
	var titles []string
	for _, result := range results {
		titles = append(titles, result.Title)
	}
	return strings.Join(titles, " | ")
}

func resultsContainTitle(results []SearchResultDTO, value string) bool {
	for _, result := range results {
		if strings.Contains(result.Title, value) {
			return true
		}
	}
	return false
}

func realDataIndexPath() string {
	if value := strings.TrimSpace(os.Getenv("KRX_INDEX_PATH")); value != "" {
		return resolveRealDataPath(value)
	}
	if value := strings.TrimSpace(os.Getenv("KRX_RULE_INDEX_DIR")); value != "" {
		return searchindex.DefaultBM25Path(value)
	}
	if value := strings.TrimSpace(os.Getenv("KRX_INDEX_DIR")); value != "" {
		return searchindex.DefaultBM25Path(value)
	}
	return resolveRealDataPath(filepath.Join("..", "..", searchindex.DefaultIndexDir, searchindex.BM25SnapshotFile))
}

func resolveRealDataPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	repoRelative := filepath.Join("..", "..", path)
	if _, err := os.Stat(repoRelative); err == nil {
		return repoRelative
	}
	return path
}
