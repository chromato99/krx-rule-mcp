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
			name: "reviewed alias covering the whole query is supported",
			input: AnswerabilityInput{
				Query: "동적상하한가", DomainExpansionApplied: true, ReviewedExpansionApplied: true,
				ReviewedExpansionMatchedTerms: 2, ReviewedExpansionExactMatch: true,
				ContractValid: true, UnknownSpecificTermCount: 1,
				Results: []SearchResult{{EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#alias", ArticleID: "별표25", LexicalCoverage: 0, BM25Score: 2,
				}}}},
			},
			status: AnswerabilitySupported, reason: "reviewed_domain_expansion",
		},
		{
			name: "reviewed alias with direct lexical coverage remains broad",
			input: AnswerabilityInput{
				Query: "가격제한폭", DomainExpansionApplied: true, ReviewedExpansionApplied: true,
				ReviewedExpansionExactMatch: true, ContractValid: true,
				Results: []SearchResult{{EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#limit", ArticleID: "제20조", LexicalCoverage: 1, BM25Score: 2,
				}}}},
			},
			status: AnswerabilityAmbiguous, reason: "broad_query",
		},
		{
			name: "reviewed expansion requires original query evidence",
			input: AnswerabilityInput{
				Query: "일중 변동폭의 적용 기준", DomainExpansionApplied: true, ReviewedExpansionApplied: true,
				ContractValid: true,
				Results: []SearchResult{{EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#expanded", ArticleID: "별표25", LexicalCoverage: 0.25, BM25Score: 2,
				}}}},
			},
			status: AnswerabilitySupported, reason: "reviewed_domain_expansion",
		},
		{
			name: "substring domain expansion does not override unknown terms",
			input: AnswerabilityInput{
				Query: "UTI 외계행성 채굴 허가", DomainExpansionApplied: true, ContractValid: true,
				UnknownSpecificTermCount: 3,
				Results: []SearchResult{{EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#uti", ArticleID: "제18조", LexicalCoverage: 0.25, BM25Score: 2,
					Text: "거래고유식별기호를 포함하여 보고한다.",
				}}}},
			},
			status: AnswerabilityInsufficient, reason: "explicit_identifier_context_mismatch",
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
			name: "channel agreement may come from any returned evidence chunk",
			input: AnswerabilityInput{Query: "결제 물량 계산 방식", ContractValid: true, Results: []SearchResult{{
				EvidenceMatches: []EvidenceMatch{
					{ChunkID: "rule#first", ArticleID: "제818조", LexicalCoverage: 0.10, BM25Score: 1},
					{ChunkID: "rule#second", ArticleID: "제818조", LexicalCoverage: 0.40, BM25Score: 1, VectorScore: 0.8},
				},
			}}},
			status: AnswerabilitySupported, reason: "bm25_vector_agreement",
		},
		{
			name: "unverified conditional term is insufficient",
			input: AnswerabilityInput{Query: "고객이 동의하면 위탁증거금을 운영비로 사용할 수 있나", ContractValid: true, Results: []SearchResult{{
				EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#margin", ArticleID: "제139조", LexicalCoverage: 0.40, BM25Score: 1, VectorScore: 0.8,
					Text: "회원은 위탁증거금을 거래증거금 이외에는 사용하지 못한다.",
				}},
			}}},
			status: AnswerabilityInsufficient, reason: "unverified_exception_condition",
		},
		{
			name: "unverified contrastive alternative is insufficient",
			input: AnswerabilityInput{Query: "UTI 대신 여권번호를 보고해도 되는가", ContractValid: true, Results: []SearchResult{{
				EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#uti", ArticleID: "제18조", LexicalCoverage: 0.40, BM25Score: 1, VectorScore: 0.8,
					Text: "거래고유식별기호를 반드시 포함하여 보고하여야 한다.",
				}},
			}}},
			status: AnswerabilityInsufficient, reason: "unverified_contrastive_relation",
		},
		{
			name: "normative counter evidence supports a denied duty question",
			input: AnswerabilityInput{Query: "UTI를 빼고 보고해도 되는가", ContractValid: true, Results: []SearchResult{{
				EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#uti", ArticleID: "제18조", LexicalCoverage: 0.25, BM25Score: 1, VectorScore: 0.8,
					Text: "거래고유식별기호를 반드시 포함하여 보고하여야 한다.",
				}},
			}}},
			status: AnswerabilitySupported, reason: "normative_counter_evidence",
		},
		{
			name: "normative counter evidence recognizes Korean denied conjugation",
			input: AnswerabilityInput{
				Query: "보고대상거래에는 UTI를 포함하면 안 된다는 규정", ContractValid: true,
				ReviewedExpansionApplied: true, UnknownSpecificTermCount: 0,
				Results: []SearchResult{{EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#uti", ArticleID: "제18조", LexicalCoverage: 0.25, BM25Score: 1, VectorScore: 0.8,
					Text: "거래고유식별기호를 반드시 포함하여 보고하여야 한다.",
				}}}},
			},
			status: AnswerabilitySupported, reason: "normative_counter_evidence",
		},
		{
			name: "multiple matched expansion terms plus channel agreement support aliases",
			input: AnswerabilityInput{
				Query: "customer margin collateral", ContractValid: true,
				DomainExpansionApplied: true, DomainExpansionMatchedTerms: 2, UnknownSpecificTermCount: 1,
				Results: []SearchResult{{EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#margin", ArticleID: "제139조", BM25Score: 1, VectorScore: 0.8,
				}}}},
			},
			status: AnswerabilitySupported, reason: "bm25_vector_agreement",
		},
		{
			name: "explicit comparison can use multiple returned documents",
			input: AnswerabilityInput{
				Query: "유가증권시장 코스닥시장 가격제한폭 비교", ContractValid: true,
				Results: []SearchResult{
					{Title: "유가증권시장 업무규정", EvidenceMatches: []EvidenceMatch{{
						ChunkID: "kospi#1", ArticleID: "제1조", LexicalCoverage: 0.25, BM25Score: 1, VectorScore: 0.8,
						Text: "유가증권시장의 가격제한폭을 정한다.",
					}}},
					{Title: "코스닥시장 업무규정", EvidenceMatches: []EvidenceMatch{{
						ChunkID: "kosdaq#1", ArticleID: "제1조", LexicalCoverage: 0.25, BM25Score: 1, VectorScore: 0.8,
						Text: "코스닥시장의 가격제한폭을 정한다.",
					}}},
				},
			},
			status: AnswerabilitySupported, reason: "multi_document_evidence",
		},
		{
			name: "comparison marker does not override multiple unknown terms",
			input: AnswerabilityInput{
				Query: "NAV 화성 토지 소유권 모두 비교", ContractValid: true, UnknownSpecificTermCount: 3,
				Results: []SearchResult{
					{EvidenceMatches: []EvidenceMatch{{ChunkID: "nav#1", ArticleID: "제1조", LexicalCoverage: 0.2, BM25Score: 1, VectorScore: 0.8, Text: "NAV"}}},
					{EvidenceMatches: []EvidenceMatch{{ChunkID: "nav#2", ArticleID: "제2조", LexicalCoverage: 0.2, BM25Score: 1, VectorScore: 0.8, Text: "순자산가치"}}},
				},
			},
			status: AnswerabilityInsufficient, reason: "explicit_identifier_context_mismatch",
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
			name: "instrumental claim requires its object in selected evidence",
			input: AnswerabilityInput{Query: "파생상품 가격제한폭으로 건강보험료 상한을 계산하는가", ContractValid: true, Results: []SearchResult{{
				EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#limit", ArticleID: "제60조", LexicalCoverage: 0.60, BM25Score: 2, VectorScore: 0.8,
					Text: "파생상품 가격제한폭과 상한을 계산한다.",
				}},
			}}},
			status: AnswerabilityInsufficient, reason: "instrumental_claim_mismatch",
		},
		{
			name: "general prohibition can contradict a novel use example",
			input: AnswerabilityInput{
				Query: "고객 위탁증거금으로 법인세를 납부할 의무가 있나", ContractValid: true,
				DomainExpansionApplied: true, DomainExpansionMatchedTerms: 3,
				ReviewedExpansionApplied: true, ReviewedExpansionMatchedTerms: 1, UnknownSpecificTermCount: 1,
				Results: []SearchResult{{EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#margin", ArticleID: "제139조", LexicalCoverage: 0.15, BM25Score: 2, VectorScore: 0.8,
					Text: "위탁증거금은 정하는 방법 이외에는 사용하지 못한다.",
				}}}},
			},
			status: AnswerabilitySupported, reason: "normative_counter_evidence",
		},
		{
			name: "quantitative claim in another result is not selected evidence",
			input: AnswerabilityInput{Query: "ETF NAV 괴리율 73퍼센트", ContractValid: true, Results: []SearchResult{
				{Title: "유가증권시장 업무규정", EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#3", ArticleID: "제1조", LexicalCoverage: 0.75, BM25Score: 2,
					Text: "순자산가치에서 3퍼센트 이상 벌어진 경우",
				}}},
				{Title: "무관한 규정", EvidenceMatches: []EvidenceMatch{{
					ChunkID: "other#73", ArticleID: "제73조", Text: "73퍼센트",
				}}},
			}},
			status: AnswerabilityInsufficient, reason: "unverified_quantitative_claim",
		},
		{
			name: "numeric units are not interchangeable",
			input: AnswerabilityInput{Query: "상품별 평가항목 10점", ContractValid: true, Results: []SearchResult{{
				Title: "시장조성자 규정", EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#10", ArticleID: "제10조", LexicalCoverage: 0.75, BM25Score: 2,
					Text: "제10조 상품별 평가항목",
				}},
			}}},
			status: AnswerabilityInsufficient, reason: "unverified_quantitative_claim",
		},
		{
			name: "score context gives min claim an explicit point unit",
			input: AnswerabilityInput{Query: "평가 점수 min 10", ContractValid: true, Results: []SearchResult{{
				Title: "시장조성자 규정", EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#score", ArticleID: "별표1", LexicalCoverage: 0.75, BM25Score: 2,
					Text: "상품별 평가항목 점수 = min(10, 거래실적 / 평가기준)",
				}},
			}}},
			status: AnswerabilitySupported, reason: "direct_evidence",
		},
		{
			name: "clock minutes must match",
			input: AnswerabilityInput{Query: "예탁시한 14:30", ContractValid: true, Results: []SearchResult{{
				Title: "청산업무규정", EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#time", ArticleID: "제88조", LexicalCoverage: 0.75, BM25Score: 2,
					Text: "예탁시한은 14시로 한다.",
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
			name: "explicit source in another result is not selected evidence",
			input: AnswerabilityInput{Query: "상법상 주주총회 소집통지", ContractValid: true, Results: []SearchResult{
				{Title: "유가증권시장 업무규정", EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#1", ArticleID: "제1조", LexicalCoverage: 0.75, BM25Score: 2,
				}}},
				{Title: "상법", EvidenceMatches: []EvidenceMatch{{ChunkID: "commercial#1", ArticleID: "제363조"}}},
			}},
			status: AnswerabilityInsufficient, reason: "explicit_source_mismatch",
		},
		{
			name: "method suffix is not an explicit law source",
			input: AnswerabilityInput{Query: "결제수량 계산 방법", ContractValid: true, Results: []SearchResult{{
				Title: "석유시장 운영규정", EvidenceMatches: []EvidenceMatch{{
					ChunkID: "rule#method", ArticleID: "제49조", LexicalCoverage: 0.75, BM25Score: 2,
				}},
			}}},
			status: AnswerabilitySupported, reason: "direct_evidence",
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

func TestSupportedDecisionReturnsEveryChunkUsedForVerification(t *testing.T) {
	decision := EvaluateAnswerability(AnswerabilityInput{
		Query: "ETF NAV 괴리율 73퍼센트", ContractValid: true,
		Results: []SearchResult{{
			Title: "유가증권시장 상장규정",
			EvidenceMatches: []EvidenceMatch{
				{ChunkID: "rule#top", ArticleID: "제20조", LexicalCoverage: 0.75, BM25Score: 2, Text: "ETF NAV 괴리율"},
				{ChunkID: "rule#number", ArticleID: "제20조의4", Text: "괴리율 73퍼센트"},
			},
		}},
	})
	if decision.Status != AnswerabilitySupported {
		t.Fatalf("decision = %#v", decision)
	}
	for _, chunkID := range []string{"rule#top", "rule#number"} {
		if !containsString(decision.EvidenceChunkIDs, chunkID) {
			t.Fatalf("evidence ids = %#v, want %q", decision.EvidenceChunkIDs, chunkID)
		}
	}
}

func TestMultiDocumentDecisionReturnsEverySelectedChunk(t *testing.T) {
	decision := EvaluateAnswerability(AnswerabilityInput{
		Query: "유가증권시장 코스닥시장 가격제한폭 비교", ContractValid: true,
		Results: []SearchResult{
			{Title: "유가증권시장 업무규정", EvidenceMatches: []EvidenceMatch{{
				ChunkID: "kospi#1", ArticleID: "제1조", LexicalCoverage: 0.25, BM25Score: 1, VectorScore: 0.8,
				Text: "유가증권시장의 가격제한폭",
			}}},
			{Title: "코스닥시장 업무규정", EvidenceMatches: []EvidenceMatch{{
				ChunkID: "kosdaq#1", ArticleID: "제1조", LexicalCoverage: 0.25, BM25Score: 1, VectorScore: 0.8,
				Text: "코스닥시장의 가격제한폭",
			}}},
		},
	})
	if decision.Status != AnswerabilitySupported || !containsString(decision.ReasonCodes, "multi_document_evidence") {
		t.Fatalf("decision = %#v", decision)
	}
	for _, chunkID := range []string{"kospi#1", "kosdaq#1"} {
		if !containsString(decision.EvidenceChunkIDs, chunkID) {
			t.Fatalf("evidence ids = %#v, want %q", decision.EvidenceChunkIDs, chunkID)
		}
	}
}

func TestExplicitSourceNamesRequireSourceGrammarOrKnownLaw(t *testing.T) {
	tests := []struct {
		query string
		want  []string
	}{
		{query: "결제수량 계산 방법"},
		{query: "시장조성 평가 기법"},
		{query: "상법상 주주총회 소집통지", want: []string{"상법"}},
		{query: "자본시장법의 투자권유 규정", want: []string{"자본시장법"}},
	}
	for _, test := range tests {
		if got := explicitSourceNames(test.query); !equalStrings(got, test.want) {
			t.Errorf("explicitSourceNames(%q) = %#v, want %#v", test.query, got, test.want)
		}
	}
}

func TestExplicitIdentifierEvidenceTermsHandleKoreanParticles(t *testing.T) {
	terms := ExplicitIdentifierEvidenceTerms("ETF는 NAV에서 LP가 어떤 호가를 내나")
	for _, want := range []string{"상장지수집합투자기구", "순자산가치", "유동성공급호가"} {
		if !containsString(terms, want) {
			t.Fatalf("identifier evidence terms = %#v, want %q", terms, want)
		}
	}
	entries := []DomainLexiconEntry{{
		ID: "nav", Canonical: "순자산가치", Aliases: []string{"NAV"}, Expansions: []string{"괴리율"},
		Confidence: "high", ReviewStatus: "official-glossary",
	}}
	expansion := ExpandDomainQueryWithLexicon("NAV에서 벌어지는가", entries)
	if !expansion.Applied() || expansion.ReviewedMatchCount() != 1 {
		t.Fatalf("particle-suffixed acronym expansion = %#v", expansion)
	}
	concepts := expansion.ReviewedEvidenceConcepts()
	if len(concepts) != 1 || !containsString(concepts[0], "순자산가치") || containsString(concepts[0], "괴리율") {
		t.Fatalf("reviewed evidence concepts = %#v", concepts)
	}
}

func TestMeaningfulQueryTermsNormalizeKoreanParticles(t *testing.T) {
	got := meaningfulQueryTerms("ETF 종가가 NAV에서 호가를 내야 하나")
	want := []string{"etf", "종가", "nav", "호가", "내야"}
	if !equalStrings(got, want) {
		t.Fatalf("meaningfulQueryTerms() = %#v, want %#v", got, want)
	}
}

func TestContrastiveAlternativeMarkersDoNotTrustAliasOnlyEvidence(t *testing.T) {
	evidence := []SearchResult{{EvidenceMatches: []EvidenceMatch{{Text: "UTI를 반드시 포함하여 보고하여야 한다."}}}}
	for _, query := range []string{
		"UTI 대신 여권번호", "UTI 말고 여권번호", "UTI가 아니라 여권번호",
		"UTI를 여권번호로 대체하여 보고", "UTI를 쓰지 않고 여권번호 보고",
	} {
		if !queryContainsContrastiveAlternative(query) {
			t.Errorf("contrastive marker not detected: %q", query)
		}
		if contrastiveAlternativeVerified(query, evidence) {
			t.Errorf("alias-only evidence verified contrastive relation: %q", query)
		}
	}
	if queryContainsContrastiveAlternative("대신증권 관련 규정") {
		t.Fatal("organization name was mistaken for a contrastive marker")
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
