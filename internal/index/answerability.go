package index

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const AnswerabilityGateVersion = "evidence-gate-v4"

type AnswerabilityStatus string

const (
	AnswerabilitySupported    AnswerabilityStatus = "supported"
	AnswerabilityInsufficient AnswerabilityStatus = "insufficient"
	AnswerabilityAmbiguous    AnswerabilityStatus = "ambiguous"
	AnswerabilityUnknown      AnswerabilityStatus = "unknown"
)

// AnswerabilityFeatures are observable retrieval facts, not probabilities.
// They are recorded so a versioned golden set can calibrate later gate versions
// without reinterpreting one BM25, vector, or RRF score as confidence.
type AnswerabilityFeatures struct {
	QueryTermCount                     int      `json:"query_term_count"`
	ResultCount                        int      `json:"result_count"`
	SelectedEvidenceMaxLexicalCoverage float64  `json:"selected_evidence_max_lexical_coverage"`
	SelectedEvidenceBundleCoverage     float64  `json:"selected_evidence_bundle_lexical_coverage"`
	SelectedEvidenceAnchored           bool     `json:"selected_evidence_anchored"`
	SelectedEvidenceDocuments          int      `json:"selected_evidence_documents"`
	SelectedEvidenceAnchoredDocuments  int      `json:"selected_evidence_anchored_documents"`
	SelectedEvidenceAgreementDocuments int      `json:"selected_evidence_agreement_documents"`
	BM25VectorAgreement                bool     `json:"bm25_vector_agreement"`
	MultiDocumentIntent                bool     `json:"multi_document_intent"`
	DomainExpansion                    bool     `json:"domain_expansion"`
	DomainExpansionMatchedTerms        int      `json:"domain_expansion_matched_terms"`
	ReviewedExpansion                  bool     `json:"reviewed_expansion"`
	ReviewedExpansionMatchedTerms      int      `json:"reviewed_expansion_matched_terms"`
	ReviewedExpansionMatchedGroups     int      `json:"reviewed_expansion_matched_groups"`
	ReviewedExpansionEvidenceCoverage  float64  `json:"reviewed_expansion_evidence_coverage"`
	ReviewedExpansionExactMatch        bool     `json:"reviewed_expansion_exact_match"`
	DistinctTopCategories              int      `json:"distinct_top_categories"`
	TopScoreMargin                     float64  `json:"top_score_margin"`
	FilterApplied                      bool     `json:"filter_applied"`
	QuantitativeClaimsPresent          bool     `json:"quantitative_claims_present"`
	QuantitativeClaimsVerified         bool     `json:"quantitative_claims_verified"`
	ExplicitSourceMatched              bool     `json:"explicit_source_matched"`
	UnknownSpecificTermCount           int      `json:"unknown_specific_term_count"`
	ObligationSubjectVerified          bool     `json:"obligation_subject_verified"`
	CompositeClaimEvidence             bool     `json:"composite_claim_evidence"`
	ExceptionConditionsVerified        bool     `json:"exception_conditions_verified"`
	ContrastiveAlternative             bool     `json:"contrastive_alternative"`
	ContrastiveAlternativeVerified     bool     `json:"contrastive_alternative_verified"`
	NormativeCounterEvidence           bool     `json:"normative_counter_evidence"`
	ExplicitIdentifiersPresent         bool     `json:"explicit_identifiers_present"`
	ExplicitIdentifiersMatched         bool     `json:"explicit_identifiers_matched"`
	ExplicitIdentifierMissing          []string `json:"explicit_identifier_missing,omitempty"`
	ExplicitIdentifierContextVerified  bool     `json:"explicit_identifier_context_verified"`
	ClaimLikeQuery                     bool     `json:"claim_like_query"`
	ClaimCoverageVerified              bool     `json:"claim_coverage_verified"`
	InstrumentalClaimVerified          bool     `json:"instrumental_claim_verified"`
}

type AnswerabilityDecision struct {
	Status           AnswerabilityStatus   `json:"status"`
	ReasonCodes      []string              `json:"reason_codes"`
	GateVersion      string                `json:"gate_version"`
	EvidenceChunkIDs []string              `json:"evidence_chunk_ids"`
	Clarification    string                `json:"clarification,omitempty"`
	Features         AnswerabilityFeatures `json:"features"`
}

type AnswerabilityInput struct {
	Query                          string
	Filter                         Filter
	DomainExpansionApplied         bool
	DomainExpansionMatchedTerms    int
	ReviewedExpansionApplied       bool
	ReviewedExpansionMatchedTerms  int
	ReviewedExpansionMatchedGroups int
	ReviewedExpansionEvidenceTerms []string
	ReviewedExpansionExactMatch    bool
	ContractValid                  bool
	UnknownSpecificTermCount       int
	Results                        []SearchResult
}

// EvaluateAnswerability classifies whether the current release returned direct
// evidence. It deliberately uses a conjunction of retrieval features rather
// than a threshold over one ranking score.
func EvaluateAnswerability(input AnswerabilityInput) AnswerabilityDecision {
	features := answerabilityFeatures(input)
	decision := AnswerabilityDecision{
		Status:      AnswerabilityInsufficient,
		ReasonCodes: []string{"no_supported_evidence"},
		GateVersion: AnswerabilityGateVersion,
		Features:    features,
	}
	if !input.ContractValid {
		decision.Status = AnswerabilityUnknown
		decision.ReasonCodes = []string{"retrieval_contract_mismatch"}
		return decision
	}
	if len(input.Results) == 0 {
		decision.ReasonCodes = []string{"no_results", "no_supported_evidence"}
		return decision
	}

	selectedResults := selectedEvidenceResults(input.Query, input.Results)
	selectedEvidence := collectEvidenceMatches(selectedResults)
	if features.QueryTermCount <= 1 && !filterDisambiguatesBroadQuery(input.Filter) &&
		(!features.ReviewedExpansionExactMatch || features.ReviewedExpansionMatchedTerms < 2 ||
			features.SelectedEvidenceMaxLexicalCoverage >= 1.0/3.0) {
		decision.Status = AnswerabilityAmbiguous
		decision.ReasonCodes = []string{"broad_query"}
		decision.Clarification = "질문의 대상 시장, 규정 종류, 상품 또는 행위를 더 구체적으로 지정하세요."
		decision.EvidenceChunkIDs = evidenceChunkIDs(selectedEvidence)
		return decision
	}
	if len(selectedEvidence) == 0 {
		decision.ReasonCodes = []string{"no_context_addressable_evidence"}
		return decision
	}
	if !features.QuantitativeClaimsVerified {
		decision.ReasonCodes = []string{"unverified_quantitative_claim", "no_supported_evidence"}
		return decision
	}
	if !features.ExplicitSourceMatched {
		decision.ReasonCodes = []string{"explicit_source_mismatch", "no_supported_evidence"}
		return decision
	}
	if !features.ExplicitIdentifiersMatched {
		decision.ReasonCodes = []string{"explicit_identifier_mismatch", "no_supported_evidence"}
		return decision
	}
	if !features.ExplicitIdentifierContextVerified {
		decision.ReasonCodes = []string{"explicit_identifier_context_mismatch", "no_supported_evidence"}
		return decision
	}
	if !features.ObligationSubjectVerified {
		decision.ReasonCodes = []string{"obligation_subject_mismatch", "no_supported_evidence"}
		return decision
	}
	if !features.CompositeClaimEvidence {
		decision.ReasonCodes = []string{"composite_claim_not_coanchored", "no_supported_evidence"}
		return decision
	}
	if !features.ExceptionConditionsVerified {
		decision.ReasonCodes = []string{"unverified_exception_condition", "no_supported_evidence"}
		return decision
	}
	if !features.ContrastiveAlternativeVerified {
		decision.ReasonCodes = []string{"unverified_contrastive_relation", "no_supported_evidence"}
		return decision
	}
	if !features.InstrumentalClaimVerified {
		decision.ReasonCodes = []string{"instrumental_claim_mismatch", "no_supported_evidence"}
		return decision
	}
	if !features.ClaimCoverageVerified {
		decision.ReasonCodes = []string{"claim_evidence_coverage_mismatch", "no_supported_evidence"}
		return decision
	}
	if !features.SelectedEvidenceAnchored && selectedEvidenceHasVectorScore(selectedEvidence) &&
		features.SelectedEvidenceMaxLexicalCoverage < 0.50 {
		decision.Status = AnswerabilityAmbiguous
		decision.ReasonCodes = []string{"unstructured_vector_evidence"}
		decision.Clarification = "검색된 의미상 후보의 규정, 조문 또는 첨부 대상을 더 구체적으로 지정하세요."
		decision.EvidenceChunkIDs = evidenceChunkIDs(selectedEvidence)
		return decision
	}

	directLexicalEvidence := features.SelectedEvidenceAnchored && features.SelectedEvidenceMaxLexicalCoverage >= 0.60 &&
		features.UnknownSpecificTermCount <= 1
	knownLexicalEvidence := features.SelectedEvidenceAnchored && features.UnknownSpecificTermCount == 0 &&
		features.SelectedEvidenceMaxLexicalCoverage >= 1.0/3.0
	reviewedAliasEvidence := features.ReviewedExpansionExactMatch && features.SelectedEvidenceAnchored
	reviewedExpansionEvidence := features.ReviewedExpansion && features.SelectedEvidenceAnchored &&
		((features.UnknownSpecificTermCount == 0 && features.SelectedEvidenceMaxLexicalCoverage >= 0.25) ||
			(features.UnknownSpecificTermCount <= 3 && features.SelectedEvidenceMaxLexicalCoverage >= 0.40) ||
			(features.QueryTermCount >= 5 && features.UnknownSpecificTermCount*2 <= features.QueryTermCount &&
				features.SelectedEvidenceMaxLexicalCoverage >= 0.25) ||
			(features.ReviewedExpansionMatchedTerms >= 2 && features.QueryTermCount >= 5 &&
				features.SelectedEvidenceMaxLexicalCoverage >= 0.125) ||
			(features.ReviewedExpansionMatchedTerms >= 3 && features.DomainExpansionMatchedTerms >= 3 &&
				features.SelectedEvidenceMaxLexicalCoverage >= 0.10) ||
			(features.ReviewedExpansionMatchedTerms >= 2 && features.DomainExpansionMatchedTerms >= 3 &&
				features.BM25VectorAgreement && features.UnknownSpecificTermCount*2 < features.QueryTermCount))
	compositionalExpansionEvidence := features.ReviewedExpansionMatchedGroups >= 3 &&
		features.ReviewedExpansionEvidenceCoverage >= 0.25 && features.SelectedEvidenceAnchored &&
		features.UnknownSpecificTermCount <= 1
	strongChannelAgreement := features.BM25VectorAgreement && features.SelectedEvidenceAnchored &&
		((features.QueryTermCount >= 4 && features.UnknownSpecificTermCount <= 1 &&
			features.SelectedEvidenceMaxLexicalCoverage >= 0.25) ||
			(features.UnknownSpecificTermCount <= 2 && features.SelectedEvidenceMaxLexicalCoverage >= 0.40) ||
			(features.DomainExpansionMatchedTerms >= 2 && features.UnknownSpecificTermCount <= 1))
	semanticChannelAgreement := features.BM25VectorAgreement && features.SelectedEvidenceAnchored &&
		features.QueryTermCount >= 5 && features.SelectedEvidenceMaxLexicalCoverage >= 0.20 &&
		features.UnknownSpecificTermCount*4 < features.QueryTermCount
	quantitativeEvidence := features.QuantitativeClaimsPresent && features.QuantitativeClaimsVerified &&
		features.BM25VectorAgreement && features.SelectedEvidenceAnchored &&
		features.UnknownSpecificTermCount <= 3 && features.SelectedEvidenceMaxLexicalCoverage >= 0.125
	normativeCounterEvidence := features.NormativeCounterEvidence && features.BM25VectorAgreement &&
		features.SelectedEvidenceAnchored && (features.UnknownSpecificTermCount <= 2 ||
		features.UnknownSpecificTermCount*2 <= features.QueryTermCount) &&
		(features.SelectedEvidenceMaxLexicalCoverage >= 0.125 || features.ReviewedExpansion)
	documentEvidence := !features.SelectedEvidenceAnchored && features.QueryTermCount >= 2 &&
		features.SelectedEvidenceMaxLexicalCoverage >= 0.50
	multiDocumentEvidence := features.MultiDocumentIntent && features.SelectedEvidenceDocuments >= 2 &&
		features.SelectedEvidenceAnchoredDocuments >= 2 && features.SelectedEvidenceAgreementDocuments >= 2 &&
		features.UnknownSpecificTermCount <= 2 && features.SelectedEvidenceBundleCoverage >= 0.40
	if directLexicalEvidence || knownLexicalEvidence || reviewedAliasEvidence || reviewedExpansionEvidence || compositionalExpansionEvidence || strongChannelAgreement ||
		semanticChannelAgreement || quantitativeEvidence || normativeCounterEvidence || documentEvidence || multiDocumentEvidence {
		decision.Status = AnswerabilitySupported
		decision.ReasonCodes = []string{"direct_evidence"}
		if strongChannelAgreement || semanticChannelAgreement {
			decision.ReasonCodes = append(decision.ReasonCodes, "bm25_vector_agreement")
		}
		if reviewedAliasEvidence || reviewedExpansionEvidence || compositionalExpansionEvidence {
			decision.ReasonCodes = append(decision.ReasonCodes, "reviewed_domain_expansion")
		}
		if normativeCounterEvidence {
			decision.ReasonCodes = append(decision.ReasonCodes, "normative_counter_evidence")
		}
		if multiDocumentEvidence {
			decision.ReasonCodes = append(decision.ReasonCodes, "multi_document_evidence")
		}
		decision.EvidenceChunkIDs = evidenceChunkIDs(selectedEvidence)
		return decision
	}

	decision.ReasonCodes = []string{"low_query_evidence_coverage", "no_supported_evidence"}
	return decision
}

func answerabilityFeatures(input AnswerabilityInput) AnswerabilityFeatures {
	selectedResults := selectedEvidenceResults(input.Query, input.Results)
	features := AnswerabilityFeatures{
		QueryTermCount:                 len(meaningfulQueryTerms(input.Query)),
		ResultCount:                    len(input.Results),
		DomainExpansion:                input.DomainExpansionApplied,
		DomainExpansionMatchedTerms:    input.DomainExpansionMatchedTerms,
		ReviewedExpansion:              input.ReviewedExpansionApplied,
		ReviewedExpansionMatchedTerms:  input.ReviewedExpansionMatchedTerms,
		ReviewedExpansionMatchedGroups: input.ReviewedExpansionMatchedGroups,
		ReviewedExpansionExactMatch:    input.ReviewedExpansionExactMatch,
		FilterApplied:                  filterApplied(input.Filter),
		QuantitativeClaimsPresent:      quantitativeClaimsPresent(input.Query),
		QuantitativeClaimsVerified:     quantitativeClaimsVerified(input.Query, selectedResults),
		ExplicitSourceMatched:          explicitSourceMatched(input.Query, selectedResults),
		UnknownSpecificTermCount:       input.UnknownSpecificTermCount,
		ObligationSubjectVerified:      obligationSubjectVerified(input.Query, selectedResults),
		CompositeClaimEvidence:         compositeClaimEvidence(input.Query, selectedResults),
		ExceptionConditionsVerified:    exceptionConditionsVerified(input.Query, selectedResults),
		ContrastiveAlternative:         queryContainsContrastiveAlternative(input.Query),
		ContrastiveAlternativeVerified: contrastiveAlternativeVerified(input.Query, selectedResults),
		NormativeCounterEvidence:       normativeCounterEvidence(input.Query, selectedResults),
		MultiDocumentIntent:            multiDocumentEvidenceIntent(input.Query),
	}
	features.SelectedEvidenceDocuments = len(selectedResults)
	features.SelectedEvidenceBundleCoverage = termCoverage(meaningfulQueryTerms(input.Query), searchEvidenceText(selectedResults))
	features.ReviewedExpansionEvidenceCoverage = evidenceTermCoverage(input.ReviewedExpansionEvidenceTerms, selectedResults)
	categories := map[string]struct{}{}
	for i, result := range input.Results {
		if i >= 5 {
			break
		}
		category := strings.TrimSpace(result.Category)
		if category != "" {
			categories[category] = struct{}{}
		}
	}
	features.DistinctTopCategories = len(categories)
	if len(input.Results) > 1 {
		features.TopScoreMargin = input.Results[0].Score - input.Results[1].Score
	}
	if len(selectedResults) == 0 {
		return features
	}
	for _, result := range selectedResults {
		anchored := false
		agreement := false
		for _, match := range result.EvidenceMatches {
			if match.LexicalCoverage > features.SelectedEvidenceMaxLexicalCoverage {
				features.SelectedEvidenceMaxLexicalCoverage = match.LexicalCoverage
			}
			if match.ArticleID != "" || match.AttachmentID != "" || len(match.HeadingPath) > 0 {
				features.SelectedEvidenceAnchored = true
				anchored = true
			}
			if match.BM25Score > 0 && match.VectorScore > 0 {
				features.BM25VectorAgreement = true
				agreement = true
			}
		}
		if anchored {
			features.SelectedEvidenceAnchoredDocuments++
		}
		if agreement {
			features.SelectedEvidenceAgreementDocuments++
		}
	}
	identifiers := explicitIdentifierTerms(input.Query)
	features.ExplicitIdentifiersPresent = len(identifiers) > 0
	features.ExplicitIdentifierMissing = unmatchedExplicitIdentifiers(identifiers, selectedResults)
	features.ExplicitIdentifiersMatched = len(features.ExplicitIdentifierMissing) == 0
	features.ExplicitIdentifierContextVerified = !features.ExplicitIdentifiersPresent ||
		features.SelectedEvidenceBundleCoverage >= 0.40 ||
		(features.QuantitativeClaimsPresent && features.QuantitativeClaimsVerified) ||
		features.ReviewedExpansionMatchedTerms >= 2 || features.NormativeCounterEvidence ||
		(features.MultiDocumentIntent && features.ExplicitIdentifiersMatched &&
			features.SelectedEvidenceAnchoredDocuments >= 2 && features.SelectedEvidenceBundleCoverage >= 0.30)
	features.ClaimLikeQuery = claimLikeQuery(input.Query)
	features.InstrumentalClaimVerified = instrumentalClaimVerified(input.Query, selectedResults)
	features.ClaimCoverageVerified = !features.ClaimLikeQuery || features.SelectedEvidenceBundleCoverage >= 0.40 ||
		(features.QuantitativeClaimsPresent && features.QuantitativeClaimsVerified &&
			(features.SelectedEvidenceBundleCoverage >= 0.40 || features.ReviewedExpansion)) || features.NormativeCounterEvidence ||
		(features.ReviewedExpansion && features.SelectedEvidenceBundleCoverage >= 0.25 &&
			features.UnknownSpecificTermCount*2 < features.QueryTermCount) ||
		(features.ReviewedExpansionMatchedTerms >= 2 && features.DomainExpansionMatchedTerms >= 3 &&
			features.UnknownSpecificTermCount*2 < features.QueryTermCount) ||
		(features.MultiDocumentIntent && features.SelectedEvidenceBundleCoverage >= 0.30)
	return features
}

func evidenceTermCoverage(terms []string, results []SearchResult) float64 {
	if len(terms) == 0 {
		return 0
	}
	evidence := normalizeClaimText(searchEvidenceText(results))
	seen := map[string]struct{}{}
	matched := 0
	for _, term := range terms {
		normalized := normalizeClaimText(term)
		if normalized == "" {
			continue
		}
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		if strings.Contains(evidence, normalized) {
			matched++
		}
	}
	if len(seen) == 0 {
		return 0
	}
	return float64(matched) / float64(len(seen))
}

func selectedEvidenceHasVectorScore(matches []EvidenceMatch) bool {
	for _, match := range matches {
		if match.VectorScore > 0 {
			return true
		}
	}
	return false
}

func selectedEvidenceResults(query string, results []SearchResult) []SearchResult {
	if len(results) == 0 {
		return nil
	}
	limit := 1
	if multiDocumentEvidenceIntent(query) {
		limit = 3
	}
	if limit > len(results) {
		limit = len(results)
	}
	return results[:limit]
}

func collectEvidenceMatches(results []SearchResult) []EvidenceMatch {
	var matches []EvidenceMatch
	for _, result := range results {
		matches = append(matches, result.EvidenceMatches...)
	}
	return matches
}

func multiDocumentEvidenceIntent(query string) bool {
	normalized := normalizeClaimText(query)
	for _, marker := range []string{"비교", "각각", "모두", "함께", "동시에", "양쪽", "둘다", "두규정", "세시장", "복수"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func evidenceChunkIDs(matches []EvidenceMatch) []string {
	ids := make([]string, 0, len(matches))
	seen := map[string]struct{}{}
	for _, match := range matches {
		if match.ChunkID == "" {
			continue
		}
		if _, exists := seen[match.ChunkID]; exists {
			continue
		}
		seen[match.ChunkID] = struct{}{}
		ids = append(ids, match.ChunkID)
	}
	sort.Strings(ids)
	return ids
}

func filterApplied(filter Filter) bool {
	return filter.DocumentType != "" || filter.Language != "" || strings.TrimSpace(filter.Category) != "" ||
		filter.EffectiveFrom != "" || filter.EffectiveTo != "" || filter.PublishedFrom != "" || filter.PublishedTo != ""
}

func filterDisambiguatesBroadQuery(filter Filter) bool {
	return filter.DocumentType != "" || strings.TrimSpace(filter.Category) != ""
}

func (e *Engine) UnknownSpecificTermCount(query string) int {
	unknown := 0
	for _, term := range meaningfulQueryTerms(query) {
		known := false
		for _, token := range Tokenize(term) {
			tokenLength := runeLen(token)
			if tokenLength < 3 && token != term {
				continue
			}
			if e.df[token] > 0 {
				known = true
				break
			}
		}
		if !known {
			unknown++
		}
	}
	return unknown
}

var (
	quantitativeClaimPattern = regexp.MustCompile(`(?i)(제\s*[0-9]+조(?:의\s*[0-9]+)?|별표\s*[0-9]+|min\s*[0-9]+|[0-9]{1,2}:[0-9]{2}|(?:오전|오후)?\s*[0-9]+(?:\.[0-9]+)?\s*(?:%|％|퍼센트|년|개월|일|시|분|초|점))`)
	claimNumberPattern       = regexp.MustCompile(`[0-9]+`)
)

func quantitativeClaimsVerified(query string, results []SearchResult) bool {
	claims := quantitativeClaimPattern.FindAllString(query, -1)
	if len(claims) == 0 {
		return true
	}
	haystack := normalizeClaimText(searchEvidenceText(results))
	for _, claim := range claims {
		matched := false
		for _, alias := range quantitativeClaimAliases(claim, query) {
			if strings.Contains(haystack, alias) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func quantitativeClaimsPresent(query string) bool {
	return quantitativeClaimPattern.MatchString(query)
}

// QuantitativeEvidenceTerms returns normalized-equivalent surface forms that
// evidence reranking can keep in the selected bundle for later verification.
func QuantitativeEvidenceTerms(query string) []string {
	seen := map[string]struct{}{}
	var terms []string
	for _, claim := range quantitativeClaimPattern.FindAllString(query, -1) {
		for _, alias := range quantitativeClaimAliases(claim, query) {
			normalized := normalizeClaimText(alias)
			if normalized == "" {
				continue
			}
			if _, exists := seen[normalized]; exists {
				continue
			}
			seen[normalized] = struct{}{}
			terms = append(terms, alias)
		}
	}
	return terms
}

func normalizeClaimText(value string) string {
	value = strings.ToLower(value)
	value = strings.NewReplacer("%", "퍼센트", "％", "퍼센트").Replace(value)
	return strings.Join(strings.Fields(value), "")
}

func quantitativeClaimAliases(claim, query string) []string {
	normalized := normalizeClaimText(claim)
	aliases := []string{normalized}
	number := claimNumberPattern.FindString(normalized)
	if strings.HasPrefix(normalized, "min") && number != "" {
		aliases = append(aliases, "최소"+number, number+"이상", "min("+number)
		if strings.Contains(normalizeClaimText(query), "점수"+normalized) {
			aliases = append(aliases, number+"점")
		}
	}
	if strings.HasPrefix(normalized, "오후") && strings.HasSuffix(normalized, "시") && number != "" {
		hour, _ := strconv.Atoi(number)
		if hour < 12 {
			hour += 12
		}
		aliases = append(aliases, strconv.Itoa(hour)+":00", strconv.Itoa(hour)+"시")
	}
	if len(normalized) == 5 && normalized[2] == ':' {
		hour := int(normalized[0]-'0')*10 + int(normalized[1]-'0')
		minute := int(normalized[3]-'0')*10 + int(normalized[4]-'0')
		aliases = append(aliases, strconv.Itoa(hour)+"시"+strconv.Itoa(minute)+"분")
		if minute == 0 {
			aliases = append(aliases, strconv.Itoa(hour)+"시")
		}
		if hour >= 12 && minute == 0 {
			displayHour := hour
			if displayHour > 12 {
				displayHour -= 12
			}
			aliases = append(aliases, "오후"+strconv.Itoa(displayHour)+"시")
		}
	}
	return aliases
}

func searchEvidenceText(results []SearchResult) string {
	var text strings.Builder
	for resultIndex, result := range results {
		if resultIndex >= 5 {
			break
		}
		text.WriteString(result.Title)
		text.WriteByte(' ')
		text.WriteString(result.Snippet)
		text.WriteByte(' ')
		for _, match := range result.EvidenceMatches {
			text.WriteString(match.ArticleID)
			text.WriteByte(' ')
			text.WriteString(strings.Join(match.HeadingPath, " "))
			text.WriteByte(' ')
			text.WriteString(match.Snippet)
			text.WriteByte(' ')
			text.WriteString(match.Text)
			text.WriteByte(' ')
		}
	}
	return text.String()
}

func exceptionConditionsVerified(query string, results []SearchResult) bool {
	if exhaustiveRestrictionOverridesCondition(query, results) {
		return true
	}
	evidence := normalizeClaimText(searchEvidenceText(results))
	fields := strings.Fields(query)
	for fieldIndex, field := range fields {
		field = strings.Trim(field, ".,?!:;()[]{}\"'")
		for _, suffix := range []string{"이라면", "라면", "이면", "하면"} {
			if !strings.HasSuffix(field, suffix) {
				continue
			}
			stem := strings.TrimSuffix(field, suffix)
			if exceptionConditionStem(stem) {
				if !strings.Contains(evidence, normalizeClaimText(stem)) {
					return false
				}
				if fieldIndex > 0 {
					subject := strings.Trim(fields[fieldIndex-1], ".,?!:;()[]{}\"'")
					subject = trimKoreanSubjectParticle(subject)
					if runeLen(subject) >= 2 && !strings.Contains(evidence, normalizeClaimText(subject)) {
						return false
					}
				}
			}
			break
		}
	}
	return true
}

func exceptionConditionStem(stem string) bool {
	stem = normalizeClaimText(stem)
	for _, material := range []string{"동의", "승인", "허가", "합의", "승낙", "요청", "신청", "예외"} {
		if strings.HasSuffix(stem, material) {
			return true
		}
	}
	return false
}

func queryContainsContrastiveAlternative(query string) bool {
	lower := strings.ToLower(query)
	for _, marker := range []string{"instead of", "rather than", "in place of", "without using"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	for _, field := range strings.Fields(query) {
		field = strings.Trim(field, ".,?!:;()[]{}\"'")
		for _, suffix := range []string{"대신", "말고", "아니라", "대체하여", "대체해서", "대체해"} {
			if strings.HasSuffix(field, suffix) {
				return true
			}
		}
	}
	return strings.Contains(normalizeClaimText(query), "쓰지않고")
}

func contrastiveAlternativeVerified(query string, results []SearchResult) bool {
	if !queryContainsContrastiveAlternative(query) {
		return true
	}
	evidence := normalizeClaimText(searchEvidenceText(results))
	lowerEvidence := strings.ToLower(searchEvidenceText(results))
	for _, marker := range []string{"instead of", "rather than", "in place of", "without using"} {
		if strings.Contains(lowerEvidence, marker) {
			return true
		}
	}
	for _, marker := range []string{"대신", "말고", "아니라", "대체하여", "대체해서", "대체해", "쓰지않고"} {
		if strings.Contains(evidence, marker) {
			return true
		}
	}
	return requiredContrastiveSourceVerified(query, results)
}

func requiredContrastiveSourceVerified(query string, results []SearchResult) bool {
	sources := contrastiveSourceTerms(query)
	if len(sources) == 0 {
		return false
	}
	for _, result := range results {
		for _, match := range result.EvidenceMatches {
			evidence := normalizeClaimText(match.ArticleID + " " + strings.Join(match.HeadingPath, " ") + " " + match.Snippet + " " + match.Text)
			required := containsAny(evidence, []string{"반드시", "shalluse", "mustuse", "shallinclude", "mustinclude", "requiredtoinclude"})
			prohibited := containsAny(evidence, []string{"하지말", "하지못", "해서는아니", "할수없", "shallnot", "mustnot", "maynot", "prohibit"})
			if !required || prohibited {
				continue
			}
			for _, source := range sources {
				aliases := append([]string{source}, ExplicitIdentifierEvidenceTerms(source)...)
				for _, alias := range aliases {
					if normalized := normalizeClaimText(alias); normalized != "" && strings.Contains(evidence, normalized) {
						return true
					}
				}
			}
		}
	}
	return false
}

func contrastiveSourceTerms(query string) []string {
	fields := strings.Fields(query)
	for index, raw := range fields {
		field := strings.Trim(raw, ".,?!:;()[]{}\"'")
		for _, suffix := range []string{"대신", "말고", "아니라"} {
			if !strings.HasSuffix(field, suffix) {
				continue
			}
			candidate := trimKoreanContrastiveParticle(strings.TrimSuffix(field, suffix))
			if runeLen(candidate) >= 2 {
				return []string{candidate}
			}
			if index > 0 {
				candidate = trimKoreanContrastiveParticle(strings.Trim(fields[index-1], ".,?!:;()[]{}\"'"))
				if runeLen(candidate) >= 2 {
					return []string{candidate}
				}
			}
		}
		if strings.Contains(field, "대체") {
			for previous := 0; previous < index; previous++ {
				candidate := strings.Trim(fields[previous], ".,?!:;()[]{}\"'")
				for _, suffix := range []string{"을", "를"} {
					if strings.HasSuffix(candidate, suffix) {
						candidate = strings.TrimSuffix(candidate, suffix)
						if runeLen(candidate) >= 2 {
							return []string{candidate}
						}
					}
				}
			}
		}
		if strings.HasPrefix(field, "쓰지") && index > 0 {
			candidate := trimKoreanContrastiveParticle(strings.Trim(fields[index-1], ".,?!:;()[]{}\"'"))
			if runeLen(candidate) >= 2 {
				return []string{candidate}
			}
		}
	}
	lower := strings.ToLower(query)
	for _, marker := range []string{"instead of", "rather than", "in place of", "without using"} {
		position := strings.Index(lower, marker)
		if position < 0 {
			continue
		}
		terms := meaningfulQueryTerms(query[position+len(marker):])
		if len(terms) > 0 {
			return []string{terms[0]}
		}
	}
	return nil
}

func trimKoreanContrastiveParticle(value string) string {
	value = trimKoreanSubjectParticle(value)
	for _, suffix := range []string{"을", "를"} {
		if strings.HasSuffix(value, suffix) {
			return strings.TrimSuffix(value, suffix)
		}
	}
	return value
}

func exhaustiveRestrictionOverridesCondition(query string, results []SearchResult) bool {
	normalizedQuery := normalizeClaimText(query)
	if !containsAny(normalizedQuery, []string{"사용할수", "써도", "해도", "가능", "can", "could", "may"}) {
		return false
	}
	queryTerms := meaningfulQueryTerms(query)
	for _, result := range results {
		for _, match := range result.EvidenceMatches {
			evidence := normalizeClaimText(match.ArticleID + " " + strings.Join(match.HeadingPath, " ") + " " + match.Snippet + " " + match.Text)
			exhaustive := containsAny(evidence, []string{"이외에는", "외에는", "이외의용도", "외의용도", "otherthan"})
			prohibited := containsAny(evidence, []string{"사용하지못", "사용할수없", "해서는아니", "shallnot", "mustnot", "maynot", "prohibit"})
			if !exhaustive || !prohibited {
				continue
			}
			for _, term := range queryTerms {
				if runeLen(term) >= 3 && strings.Contains(evidence, normalizeClaimText(term)) {
					return true
				}
			}
		}
	}
	return false
}

func containsAny(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func trimKoreanSubjectParticle(value string) string {
	for _, suffix := range []string{"에서는", "에게서", "으로", "에서", "에게", "에는", "은", "는", "이", "가", "의"} {
		if !strings.HasSuffix(value, suffix) {
			continue
		}
		trimmed := strings.TrimSuffix(value, suffix)
		if runeLen(trimmed) >= 2 {
			return trimmed
		}
	}
	return value
}

var explicitIdentifierPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9-]{1,7}`)

var explicitIdentifierEvidenceAliases = map[string][]string{
	"AP":  {"AP", "authorized participant", "지정참가회사"},
	"ETF": {"ETF", "exchange traded fund", "상장지수펀드", "상장지수집합투자기구"},
	"ETN": {"ETN", "exchange traded note", "상장지수증권"},
	"LEI": {"LEI", "legal entity identifier", "법인식별기호"},
	"LP":  {"LP", "liquidity provider", "유동성공급자", "유동성공급회원", "유동성공급호가"},
	"NAV": {"NAV", "net asset value", "순자산가치"},
	"PDF": {"PDF", "portfolio deposit file", "납부자산구성내역"},
	"UTI": {"UTI", "unique transaction identifier", "거래고유식별기호"},
}

func explicitIdentifierTerms(query string) []string {
	allowed := map[string]struct{}{
		"AP": {}, "ETF": {}, "ETN": {}, "LEI": {}, "LP": {}, "NAV": {}, "PDF": {}, "UTI": {},
	}
	seen := map[string]struct{}{}
	var terms []string
	for _, candidate := range explicitIdentifierPattern.FindAllString(query, -1) {
		candidate = strings.ToUpper(candidate)
		if _, ok := allowed[candidate]; !ok {
			continue
		}
		if _, duplicate := seen[candidate]; duplicate {
			continue
		}
		seen[candidate] = struct{}{}
		terms = append(terms, candidate)
	}
	return terms
}

func ExplicitIdentifierEvidenceTerms(query string) []string {
	seen := map[string]struct{}{}
	var terms []string
	for _, identifier := range explicitIdentifierTerms(query) {
		for _, alias := range explicitIdentifierEvidenceAliases[identifier] {
			key := normalizeClaimText(alias)
			if key == "" {
				continue
			}
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			terms = append(terms, alias)
		}
	}
	return terms
}

func ExplicitIdentifierEvidenceConcepts(query string) [][]string {
	identifiers := explicitIdentifierTerms(query)
	concepts := make([][]string, 0, len(identifiers))
	for _, identifier := range identifiers {
		concepts = append(concepts, append([]string(nil), explicitIdentifierEvidenceAliases[identifier]...))
	}
	return concepts
}

func unmatchedExplicitIdentifiers(identifiers []string, results []SearchResult) []string {
	if len(identifiers) == 0 {
		return nil
	}
	evidence := normalizeClaimText(searchEvidenceText(results))
	var missing []string
	for _, identifier := range identifiers {
		matched := false
		for _, alias := range explicitIdentifierEvidenceAliases[identifier] {
			if strings.Contains(evidence, normalizeClaimText(alias)) {
				matched = true
				break
			}
		}
		if !matched {
			missing = append(missing, identifier)
		}
	}
	return missing
}

func claimLikeQuery(query string) bool {
	lower := strings.ToLower(strings.TrimSpace(query))
	if lower == "" {
		return false
	}
	if strings.ContainsAny(lower, "?？") {
		return true
	}
	for _, marker := range []string{
		"해야", "하여야", "의무", "되는가", "하는가", "인가", "써도", "해도", "가능", "금지", "제한하는가",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	first := strings.ToLower(strings.Trim(strings.Fields(lower)[0], ".,?!:;()[]{}\"'"))
	switch first {
	case "can", "could", "does", "do", "is", "are", "may", "must", "should", "when", "which", "what", "how":
		return true
	default:
		return false
	}
}

func instrumentalClaimVerified(query string, results []SearchResult) bool {
	if normativeCounterEvidence(query, results) {
		return true
	}
	evidence := normalizeClaimText(searchEvidenceText(results))
	if objectTerms := englishCalculationObjectTerms(query); len(objectTerms) > 0 && !englishObjectTermsVerified(objectTerms, evidence) {
		return false
	}
	fields := strings.Fields(query)
	for fieldIndex, field := range fields {
		field = strings.Trim(field, ".,?!:;()[]{}\"'")
		instrument := ""
		for _, suffix := range []string{"으로", "로"} {
			if strings.HasSuffix(field, suffix) {
				instrument = strings.TrimSuffix(field, suffix)
				break
			}
		}
		if runeLen(instrument) < 2 || numericClaimToken(instrument) || fieldIndex+1 >= len(fields) {
			continue
		}
		next := strings.Trim(fields[fieldIndex+1], ".,?!:;()[]{}\"'")
		if nonInstrumentalParticlePhrase(instrument, next) {
			continue
		}
		object := next
		object = trimKoreanSubjectParticle(object)
		for _, suffix := range []string{"을", "를"} {
			object = strings.TrimSuffix(object, suffix)
		}
		if runeLen(object) >= 2 && !strings.Contains(evidence, normalizeClaimText(object)) {
			return false
		}
	}
	return true
}

var englishClaimWordPattern = regexp.MustCompile(`[A-Za-z]+(?:'[A-Za-z]+)?`)

func englishCalculationObjectTerms(query string) []string {
	words := englishClaimWordPattern.FindAllString(strings.ToLower(query), -1)
	verbIndex := -1
	verb := ""
	for index, word := range words {
		switch word {
		case "calculate", "calculates", "calculated", "compute", "computes", "computed", "derive", "derives", "derived", "determine", "determines", "determined", "set", "sets":
			verbIndex = index
			verb = word
		}
	}
	if verbIndex < 0 || verbIndex+1 >= len(words) {
		return nil
	}
	stop := map[string]struct{}{
		"a": {}, "an": {}, "the": {}, "its": {}, "their": {}, "this": {}, "that": {},
		"of": {}, "for": {}, "from": {}, "under": {}, "using": {}, "with": {}, "per": {},
	}
	var terms []string
	for _, word := range words[verbIndex+1:] {
		word = strings.TrimSuffix(word, "'s")
		if _, ignored := stop[word]; ignored || word == "" {
			continue
		}
		terms = append(terms, word)
	}
	if len(terms) == 0 {
		return nil
	}
	if strings.HasPrefix(verb, "determin") || verb == "set" || verb == "sets" {
		outcome := false
		for _, term := range terms {
			switch term {
			case "amount", "assessment", "fee", "limit", "margin", "premium", "price", "rate", "ratio", "score", "tax", "value":
				outcome = true
			}
		}
		if !outcome {
			return nil
		}
	}
	return terms
}

func englishObjectTermsVerified(terms []string, normalizedEvidence string) bool {
	if len(terms) == 0 {
		return true
	}
	if len(terms) == 1 {
		return strings.Contains(normalizedEvidence, terms[0])
	}
	for index := 0; index+1 < len(terms); index++ {
		if strings.Contains(normalizedEvidence, terms[index]+terms[index+1]) {
			return true
		}
	}
	return false
}

func nonInstrumentalParticlePhrase(instrument, next string) bool {
	instrument = normalizeClaimText(instrument)
	next = normalizeClaimText(next)
	for _, direction := range []string{"위", "아래", "앞", "뒤", "안", "밖", "내부", "외부"} {
		if instrument == direction {
			return true
		}
	}
	for _, predicate := range []string{"삼", "전용", "사용", "이용", "쓰", "간주", "취급", "분류", "지급"} {
		if strings.HasPrefix(next, predicate) {
			return true
		}
	}
	return false
}

func numericClaimToken(value string) bool {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", ""))
	if value == "" {
		return false
	}
	_, err := strconv.ParseFloat(value, 64)
	return err == nil
}

func normativeCounterEvidence(query string, results []SearchResult) bool {
	query = normalizeClaimText(query)
	queryRequestsPermissionOrDeniesDuty := false
	for _, marker := range []string{
		"안되", "안된", "안돼", "필요가없", "하지않아도", "하지않고", "없이", "사용할수", "이용할수", "전용할수", "쓸수", "써도", "해도", "빼고", "가능",
	} {
		if strings.Contains(query, marker) {
			queryRequestsPermissionOrDeniesDuty = true
			break
		}
	}
	queryAssertsDuty := false
	for _, marker := range []string{"의무가있", "의무인가", "해야하", "하여야하"} {
		if strings.Contains(query, marker) {
			queryAssertsDuty = true
			break
		}
	}
	if !queryRequestsPermissionOrDeniesDuty && !queryAssertsDuty {
		return false
	}
	evidence := normalizeClaimText(searchEvidenceText(results))
	markers := []string{"할수없다", "하지못한다", "해서는아니된다", "금지"}
	if queryRequestsPermissionOrDeniesDuty {
		markers = append(markers, "해야한다", "하여야한다", "의무", "반드시")
	}
	for _, marker := range markers {
		if strings.Contains(evidence, marker) {
			return true
		}
	}
	return false
}

func obligationSubjectVerified(query string, results []SearchResult) bool {
	if !strings.Contains(query, "의무") {
		return true
	}
	if len(results) == 0 || len(results[0].EvidenceMatches) == 0 {
		return false
	}
	evidence := normalizeClaimText(searchEvidenceText(results))
	for _, field := range strings.Fields(query) {
		field = strings.Trim(field, ".,?!:;()[]{}\"'")
		if runeLen(field) < 3 {
			continue
		}
		for _, particle := range []string{"이", "가"} {
			if strings.HasSuffix(field, particle) {
				subject := strings.TrimSuffix(field, particle)
				if !obligationSubjectRole(subject) {
					continue
				}
				return strings.Contains(evidence, normalizeClaimText(subject))
			}
		}
	}
	return true
}

func obligationSubjectRole(subject string) bool {
	for _, role := range []string{"직원", "임직원", "회원", "거래소", "법인", "투자자", "고객", "위탁자", "청산회원"} {
		if strings.HasSuffix(subject, role) {
			return true
		}
	}
	return false
}

func compositeClaimEvidence(query string, results []SearchResult) bool {
	if !strings.Contains(query, "동시에") {
		return true
	}
	if len(results) == 0 {
		return false
	}
	for _, match := range results[0].EvidenceMatches {
		if match.LexicalCoverage >= 0.60 {
			return true
		}
	}
	return false
}

func explicitSourceMatched(query string, results []SearchResult) bool {
	sources := explicitSourceNames(query)
	if len(sources) == 0 {
		return true
	}
	var titles strings.Builder
	for resultIndex, result := range results {
		if resultIndex >= 5 {
			break
		}
		titles.WriteString(normalizeClaimText(result.Title))
		titles.WriteByte(' ')
	}
	haystack := titles.String()
	for _, source := range sources {
		if strings.Contains(haystack, normalizeClaimText(source)) {
			return true
		}
	}
	return false
}

func explicitSourceNames(query string) []string {
	var sources []string
	for _, field := range strings.Fields(query) {
		field = strings.Trim(field, ".,?!:;()[]{}\"'")
		hasSourceParticle := false
		for _, particle := range []string{"에서", "에는", "으로", "상", "의", "에"} {
			if strings.HasSuffix(field, particle) {
				field = strings.TrimSuffix(field, particle)
				hasSourceParticle = true
				break
			}
		}
		if field == "규정" || field == "법" {
			continue
		}
		if strings.HasSuffix(field, "규정") || (strings.HasSuffix(field, "법") &&
			(hasSourceParticle || isKnownBareLawName(field))) {
			sources = append(sources, field)
		}
	}
	return sources
}

func isKnownBareLawName(value string) bool {
	switch value {
	case "상법", "민법", "형법", "자본시장법":
		return true
	default:
		return false
	}
}
