package index

import "testing"

func TestEvaluateAnswerabilityContracts(t *testing.T) {
	anchored := SearchResult{Category: "시장규정", EvidenceMatches: []EvidenceMatch{{ChunkID: "rule#1", ArticleID: "제1조", LexicalCoverage: 0.75, BM25Score: 2}}}
	tests := []struct {
		name   string
		input  AnswerabilityInput
		status AnswerabilityStatus
		reason string
	}{
		{
			name:   "contract mismatch is unknown",
			input:  AnswerabilityInput{Query: "상장 요건", ContractValid: false, Results: []SearchResult{anchored}},
			status: AnswerabilityUnknown, reason: "retrieval_contract_mismatch",
		},
		{
			name:   "no retrieval evidence is insufficient",
			input:  AnswerabilityInput{Query: "상장 요건", ContractValid: true},
			status: AnswerabilityInsufficient, reason: "no_results",
		},
		{
			name:   "one-term query asks for clarification",
			input:  AnswerabilityInput{Query: "상장", ContractValid: true, Results: []SearchResult{anchored}},
			status: AnswerabilityAmbiguous, reason: "broad_query",
		},
		{
			name:   "anchored lexical evidence is supported",
			input:  AnswerabilityInput{Query: "상장 요건", ContractValid: true, Results: []SearchResult{anchored}},
			status: AnswerabilitySupported, reason: "direct_evidence",
		},
		{
			name:   "low coverage remains insufficient",
			input:  AnswerabilityInput{Query: "화성 거래소 이사회 특허", ContractValid: true, Results: []SearchResult{{EvidenceMatches: []EvidenceMatch{{ChunkID: "rule#1", ArticleID: "제1조", LexicalCoverage: 0.10, BM25Score: 1}}}}},
			status: AnswerabilityInsufficient, reason: "low_query_evidence_coverage",
		},
		{
			name:   "channel agreement supports paraphrase",
			input:  AnswerabilityInput{Query: "결제 물량 계산 방식", ContractValid: true, Results: []SearchResult{{EvidenceMatches: []EvidenceMatch{{ChunkID: "rule#1", ArticleID: "제818조", LexicalCoverage: 0.40, BM25Score: 1, VectorScore: 0.8}}}}},
			status: AnswerabilitySupported, reason: "bm25_vector_agreement",
		},
		{
			name: "unverified quantitative claim is insufficient",
			input: AnswerabilityInput{Query: "ETF NAV 괴리율 73퍼센트", ContractValid: true, Results: []SearchResult{{
				Title: "유가증권시장 업무규정", EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#1", ArticleID: "제1조", LexicalCoverage: 0.75, BM25Score: 2, VectorScore: 0.8,
					Snippet: "순자산가치에서 3퍼센트 이상 벌어진 경우",
				}},
			}}},
			status: AnswerabilityInsufficient, reason: "unverified_quantitative_claim",
		},
		{
			name: "explicit external source mismatch is insufficient",
			input: AnswerabilityInput{Query: "상법상 주주총회 소집통지", ContractValid: true, Results: []SearchResult{{
				Title: "유가증권시장 업무규정", EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#1", ArticleID: "제1조", LexicalCoverage: 0.75, BM25Score: 2, VectorScore: 0.8,
				}},
			}}},
			status: AnswerabilityInsufficient, reason: "explicit_source_mismatch",
		},
		{
			name: "document level lexical evidence is supported",
			input: AnswerabilityInput{Query: "코스닥 기업 이중상장 예고", ContractValid: true, Results: []SearchResult{{
				Title: "코스닥 기업 이중상장 제도 예고", EvidenceMatches: []EvidenceMatch{{
					ChunkID: "notice#0", LexicalCoverage: 0.50, BM25Score: 2, VectorScore: 0.8,
				}},
			}}},
			status: AnswerabilitySupported, reason: "direct_evidence",
		},
		{
			name: "explicit filter disambiguates one term query",
			input: AnswerabilityInput{
				Query: "상장", Filter: Filter{Category: "유가증권시장 상장규정"}, ContractValid: true,
				Results: []SearchResult{anchored},
			},
			status: AnswerabilitySupported, reason: "direct_evidence",
		},
		{
			name: "equivalent clock claims use full evidence text",
			input: AnswerabilityInput{Query: "일중 증거금 오후 2시 14:00 예탁", ContractValid: true, Results: []SearchResult{{
				Title: "청산업무규정", EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#2", ArticleID: "제88조", LexicalCoverage: 0.75, BM25Score: 2, VectorScore: 0.8,
					Text: "일중청산증거금의 예탁시한은 계산일의 14시로 한다.",
				}},
			}}},
			status: AnswerabilitySupported, reason: "direct_evidence",
		},
		{
			name: "unanchored obligation subject is insufficient",
			input: AnswerabilityInput{Query: "거래소 직원이 고객 위탁증거금을 개인 투자에 사용할 의무", DomainExpansionApplied: true, ContractValid: true, Results: []SearchResult{{
				Title: "파생상품시장 업무규정", EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#3", ArticleID: "제93조", LexicalCoverage: 0.25, BM25Score: 2, VectorScore: 0.8,
					Text: "회원은 위탁증거금을 거래증거금으로 사용해서는 아니 된다.",
				}},
			}}},
			status: AnswerabilityInsufficient, reason: "obligation_subject_mismatch",
		},
		{
			name: "composite claim requires one coanchored chunk",
			input: AnswerabilityInput{Query: "상장 협의와 특허 심사를 동시에 신청", ContractValid: true, Results: []SearchResult{{
				Title: "상장규정", EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#4", ArticleID: "제8조", LexicalCoverage: 0.30, BM25Score: 2, VectorScore: 0.8,
					Text: "상장 신청인은 거래소와 사전협의를 한다.",
				}},
			}}},
			status: AnswerabilityInsufficient, reason: "composite_claim_not_coanchored",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := EvaluateAnswerability(test.input)
			if decision.Status != test.status {
				t.Fatalf("status = %q, want %q: %#v", decision.Status, test.status, decision)
			}
			if !containsString(decision.ReasonCodes, test.reason) {
				t.Fatalf("reason codes = %#v, want %q", decision.ReasonCodes, test.reason)
			}
			if decision.Status == AnswerabilityAmbiguous && decision.Clarification == "" {
				t.Fatal("ambiguous decision omitted clarification")
			}
			if decision.Status == AnswerabilitySupported && len(decision.EvidenceChunkIDs) == 0 {
				t.Fatal("supported decision omitted evidence chunk ids")
			}
		})
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
