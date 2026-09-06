package index

import (
	"github.com/chromato99/krx-rule-mcp/internal/model"
	"strings"
	"testing"
)

func TestExactNamedSourceRestrictsBeforeTopK(t *testing.T) {
	e := buildTestEngine([]model.Document{
		{ID: "parent", Title: "청산업무규정", Language: "ko", DocumentType: model.DocumentTypeRule, Body: "**제1조(신청)** 신청 신청 신청 기준"},
		{ID: "detail", Title: "청산업무규정 시행세칙", Language: "ko", DocumentType: model.DocumentTypeRule, Body: "**제2조(신청)** 신청 서류를 제출한다."},
		{ID: "report", Title: "보고업무규정", Language: "ko", DocumentType: model.DocumentTypeRule, Body: "**제1조(보고)** 신고한다."},
	}, nil, nil)
	opts := SearchOptions{Query: "청산업무규정 시행세칙의 신청 서류", CandidateLimit: 1, Limit: 5}
	candidates := e.RetrieveCandidates(opts)
	if len(candidates.Chunks) != 1 || candidates.Chunks[0].DocumentID != "detail" {
		t.Fatalf("named rule lost before grouping: %+v", candidates.Chunks)
	}
	if got := e.exactNamedSourceIDs("청산업무 신청", Filter{}); len(got) != 0 {
		t.Fatalf("partial title narrowed source: %v", got)
	}
	if got := e.exactNamedSourceIDs("청산업무규정과 청산업무규정 시행세칙 비교", Filter{}); len(got) != 0 {
		t.Fatal("comparison was narrowed")
	}
	if got := e.exactNamedSourceIDs("청산업무규정 시행세칙은 보고업무규정에 따라 어떤 절차를 거치나", Filter{}); len(got) != 0 {
		t.Fatal("independent named sources were collapsed")
	}
	if got := e.exactNamedSourceIDs(opts.Query, Filter{Language: "en"}); len(got) != 0 {
		t.Fatal("language filter bypassed")
	}
}

func TestPrintedEnglishTitleAndSourceTopicSeparation(t *testing.T) {
	e := buildTestEngine([]model.Document{{ID: "rule-en", Title: "회사정관", Language: "en", Body: "Articles of Incorporation\n\n§1. Shares\n\nRegistered shares may be issued."}}, nil, nil)
	query := "Which shares may be issued under the Articles of Incorporation?"
	if ids := e.exactNamedSourceIDs(query, Filter{Language: "en"}); len(ids) != 1 {
		t.Fatal("printed source title lost")
	}
	if len(e.exactNamedSourceIDs("incorporation shares", Filter{})) != 0 {
		t.Fatal("topic words treated as complete source")
	}
	if got := removeSourceTitle("청산 규정 시행세칙에서 신청 내용을 알려줘", "청산규정 시행세칙"); got != "신청 내용을 알려줘" {
		t.Fatalf("source not separated: %q", got)
	}
	if got := removeSourceTitle(query, "Articles of Incorporation"); strings.Contains(got, "Articles") || !strings.Contains(got, "shares") {
		t.Fatal("substantive query lost")
	}
}

func TestMarketScopePrecedesGenericDocumentHint(t *testing.T) {
	e := buildTestEngine([]model.Document{
		{ID: "general", Title: "분쟁조정규정", Body: "**제3조(조정기일)** 조정기일 변경 절차를 정한다."},
		{ID: "emissions", Title: "배출권 거래시장 시장감시 및 분쟁조정 규정 시행세칙", Body: "**제39조(조정기일 변경)** 배출권시장 조정기일을 당사자 동의로 변경한다."},
	}, nil, nil)
	for _, query := range []string{"배출권시장 분쟁조정에서 조정기일 변경"} {
		got := e.Search(SearchOptions{Query: query, Limit: 5})
		if len(got) != 1 || got[0].ID != "emissions" {
			t.Fatalf("generic hint displaced market: %q %+v", query, got)
		}
	}
	if len(explicitMarketScopes("코스피200 선물")) != 0 {
		t.Fatal("index product mistaken for cash-market scope")
	}
}

func TestNamedRuleScopeUsesTitleWithoutGenericSuffixes(t *testing.T) {
	for _, pair := range [][2]string{
		{"분쟁조정 사건의 기록 보관", "분쟁조정규정 시행세칙"},
		{"ESG 채권 정보플랫폼 등록 결정", "ESG채권 정보플랫폼 운영지침"},
		{"When may the joint compensation fund be used?", "Guidelines for the Management of Joint Compensation Funds 20250227"},
	} {
		if !namedDocumentScope(pair[0], pair[1]) {
			t.Fatalf("named scope not recognized: %v", pair)
		}
	}
	if namedDocumentScope("회원의 의무", "회원규정") {
		t.Fatal("generic short role treated as explicit rule name")
	}
	if namedDocumentScope("유가증권시장 공정공시", "유가증권시장 업무규정") {
		t.Fatal("market name chose a particular rule")
	}
	if namedDocumentScope("KOSDAQ Market Committee objection", "KOSDAQ Market Business Regulation") {
		t.Fatal("market name excluded its listing rules")
	}
}

func TestExplicitMarketScopeRequiresRelevantEvidence(t *testing.T) {
	results := []SearchResult{
		{ID: "securities", Title: "유가증권시장 업무규정 시행세칙", EvidenceMatches: []EvidenceMatch{{Text: "정규시장 매매거래시간과 채권 조성시간"}}},
		{ID: "gold", Title: "KRX금시장 운영규정", EvidenceMatches: []EvidenceMatch{{Text: "매매거래시간은 9시부터 15시 30분까지"}}},
	}
	for _, query := range []string{"KRX 금 현물시장의 매매시간", "금시장 정규 거래시간", "KRX금시장 운영규정"} {
		got := filterExplicitMarketResults(query, results)
		if len(got) != 1 || got[0].ID != "gold" {
			t.Fatalf("%q scope results=%#v", query, got)
		}
	}
	if got := filterExplicitMarketResults("매매시간 규정", results); len(got) != 2 {
		t.Fatal("unscoped query was restricted")
	}
	if got := filterExplicitMarketResults("가상자산시장 업무규정", results); len(got) != 0 {
		t.Fatal("unknown market was silently substituted")
	}
}

func TestMarketComparisonKeepsBothCandidateScopes(t *testing.T) {
	query := "유가증권시장과 코스닥시장 규정을 각각 비교"
	results := []SearchResult{{Title: "유가증권시장 업무규정"}, {Title: "코스닥시장 업무규정"}, {Title: "KRX금시장 운영규정"}}
	got := filterExplicitMarketResults(query, results)
	if len(got) != 2 || got[0].Title != results[0].Title || got[1].Title != results[1].Title {
		t.Fatalf("comparison candidates lost scope: %#v", got)
	}
}

func TestIndexProductNamesDoNotImposeCashMarketScope(t *testing.T) {
	for _, query := range []string{
		"daily price limit KOSPI 200 futures",
		"KOSDAQ 150 options margin",
		"KOSPI (200) index constituents",
		"Compare KOSPI 200 futures and KOSDAQ 150 options",
	} {
		if scopes := explicitMarketScopes(query); len(scopes) != 0 {
			t.Fatalf("index name became a cash-market filter: %q %+v", query, scopes)
		}
	}
	for _, query := range []string{"KOSPI market trading hours", "KOSDAQ listing review"} {
		if len(explicitMarketScopes(query)) != 1 {
			t.Fatalf("cash-market scope was lost: %q", query)
		}
	}
}
