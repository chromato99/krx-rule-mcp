package index

import (
	"testing"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/model"
)

// TestFixedRetrievalBenchmark is intentionally small and hermetic so it can
// gate every CI run. The clearing expectation mirrors the current collected
// corpus: "clearing settlement 최종결제가격" should resolve to 파생상품시장
// 업무규정 시행세칙 제5조, not merely to a document with "청산" in its title.
func TestFixedRetrievalBenchmark(t *testing.T) {
	searchable := true
	documents := []model.Document{
		{
			ID: "derivatives-detail", Title: "파생상품시장 업무규정 시행세칙", Category: "파생상품시장",
			DocumentType: model.DocumentTypeRule, Language: model.LanguageKorean, CollectedAt: benchmarkTime(),
			Body: "제1장 총칙\n\n**제5조(최종거래일, 최종결제일 및 최종결제가격)**① 주가지수선물거래의 최종거래일과 최종결제일을 정한다.\n\n③ 최종결제가격은 주가지수의 최종 수치로 하며 필요한 경우 특별최종결제가격을 사용한다.\n\n**제6조(권리행사)** 옵션 권리행사를 정한다.",
		},
		{
			ID: "otc-clearing", Title: "장외파생상품 청산업무규정", Category: "청산",
			DocumentType: model.DocumentTypeRule, Language: model.LanguageKorean, CollectedAt: benchmarkTime(),
			Body: "**제1조(목적)** 장외파생상품의 일반적인 청산 및 결제 절차를 정한다.",
		},
		{
			ID: "listing-rule", Title: "유가증권시장 상장규정", Category: "상장",
			DocumentType: model.DocumentTypeRule, Language: model.LanguageKorean, CollectedAt: benchmarkTime(),
			Body: "**제2조(상장심사)** 신규상장 신청 전 사전협의와 상장 심사 절차를 정한다.",
		},
		{
			ID: "listing-notice", Title: "상장 심사 절차 변경 예고", Category: "상장",
			DocumentType: model.DocumentTypeNotice, Language: model.LanguageKorean, CollectedAt: benchmarkTime(),
			Body: "상장 심사 제출서류 변경을 예고한다.",
		},
		{
			ID: "listing-rule-en", Title: "KOSPI Market Listing Regulation", Category: "Listing",
			DocumentType: model.DocumentTypeRule, Language: model.LanguageEnglish, CollectedAt: benchmarkTime(),
			Body: "Article 2 Preliminary listing review and consultation requirements.",
		},
		{
			ID: "margin-guideline", Title: "증권파생상품시장 증거금 관리지침", Category: "증거금",
			DocumentType: model.DocumentTypeRule, Language: model.LanguageKorean, CollectedAt: benchmarkTime(),
			Body: "**제1조(목적)** 증거금 관리의 일반 원칙을 정한다.",
			Attachments: []model.Attachment{{
				ID: "margin-annex-4", Title: "별표 4 증거금 감면액 산출변수", FileName: "별표4.hwp",
				Status: model.AttachmentConverted, Searchable: &searchable,
			}},
		},
	}
	attachments := map[string]AttachmentDocument{
		"margin-annex-4": {
			Attachment: documents[len(documents)-1].Attachments[0],
			Text:       "## 별표 4\n\n### 증거금 감면액\n\n감면계수 알파고유값은 위험상쇄비율을 사용하여 산출한다.",
		},
	}
	engine := BuildWithAttachments(documents, attachments, nil)

	cases := []struct {
		name             string
		query            string
		filter           Filter
		wantDocumentID   string
		wantArticleID    string
		wantAttachmentID string
		wantHeadingPath  []string
	}{
		{
			name: "current clearing expectation", query: "clearing settlement 최종결제가격",
			filter:         Filter{DocumentType: model.DocumentTypeRule, Language: model.LanguageKorean},
			wantDocumentID: "derivatives-detail", wantArticleID: "제5조",
		},
		{
			name: "listing anchor", query: "신규상장 사전협의",
			filter:         Filter{DocumentType: model.DocumentTypeRule, Language: model.LanguageKorean},
			wantDocumentID: "listing-rule", wantArticleID: "제2조",
		},
		{
			name: "attachment hit", query: "감면계수 알파고유값 위험상쇄비율",
			filter:         Filter{DocumentType: model.DocumentTypeRule, Language: model.LanguageKorean},
			wantDocumentID: "margin-guideline", wantAttachmentID: "margin-annex-4",
			wantHeadingPath: []string{"별표 4", "증거금 감면액"},
		},
		{
			name: "notice filter", query: "상장 심사 제출서류 변경 예고",
			filter:         Filter{DocumentType: model.DocumentTypeNotice, Language: model.LanguageKorean},
			wantDocumentID: "listing-notice",
		},
		{
			name: "english filter", query: "preliminary listing review consultation",
			filter:         Filter{DocumentType: model.DocumentTypeRule, Language: model.LanguageEnglish},
			wantDocumentID: "listing-rule-en",
		},
	}

	hits := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results := engine.Search(SearchOptions{Query: tc.query, Filter: tc.filter, Limit: 5})
			for _, result := range results {
				if tc.filter.DocumentType != "" && result.DocumentType != tc.filter.DocumentType {
					t.Fatalf("document_type filter leak: %#v", result)
				}
				if tc.filter.Language != "" && result.Language != tc.filter.Language {
					t.Fatalf("language filter leak: %#v", result)
				}
			}
			result, ok := benchmarkResult(results, tc.wantDocumentID)
			if !ok {
				t.Fatalf("Recall@5 miss for %q: %#v", tc.wantDocumentID, results)
			}
			hits++
			if tc.wantArticleID != "" && result.ArticleID != tc.wantArticleID {
				t.Fatalf("article_id = %q, want %q: %#v", result.ArticleID, tc.wantArticleID, result)
			}
			if tc.wantAttachmentID != "" {
				match, ok := benchmarkAttachment(result.AttachmentMatches, tc.wantAttachmentID)
				if !ok {
					t.Fatalf("attachment hit missing: %#v", result)
				}
				if !stringSlicesEqual(match.HeadingPath, tc.wantHeadingPath) {
					t.Fatalf("attachment heading_path = %#v, want %#v", match.HeadingPath, tc.wantHeadingPath)
				}
			}
		})
	}
	recallAt5 := float64(hits) / float64(len(cases))
	if recallAt5 != 1 {
		t.Fatalf("Recall@5 = %.2f, want 1.00", recallAt5)
	}
}

func benchmarkTime() time.Time {
	return time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
}

func benchmarkResult(results []SearchResult, id string) (SearchResult, bool) {
	for _, result := range results {
		if result.ID == id {
			return result, true
		}
	}
	return SearchResult{}, false
}

func benchmarkAttachment(matches []AttachmentMatch, id string) (AttachmentMatch, bool) {
	for _, match := range matches {
		if match.ID == id {
			return match, true
		}
	}
	return AttachmentMatch{}, false
}
