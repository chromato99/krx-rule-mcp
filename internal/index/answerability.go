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
	QueryTermCount                     int     `json:"query_term_count"`
	ResultCount                        int     `json:"result_count"`
	SelectedEvidenceMaxLexicalCoverage float64 `json:"selected_evidence_max_lexical_coverage"`
	SelectedEvidenceAnchored           bool    `json:"selected_evidence_anchored"`
	BM25VectorAgreement                bool    `json:"bm25_vector_agreement"`
	DomainExpansion                    bool    `json:"domain_expansion"`
	DomainExpansionMatchedTerms        int     `json:"domain_expansion_matched_terms"`
	ReviewedExpansion                  bool    `json:"reviewed_expansion"`
	ReviewedExpansionMatchedTerms      int     `json:"reviewed_expansion_matched_terms"`
	ReviewedExpansionExactMatch        bool    `json:"reviewed_expansion_exact_match"`
	DistinctTopCategories              int     `json:"distinct_top_categories"`
	TopScoreMargin                     float64 `json:"top_score_margin"`
	FilterApplied                      bool    `json:"filter_applied"`
	QuantitativeClaimsPresent          bool    `json:"quantitative_claims_present"`
	QuantitativeClaimsVerified         bool    `json:"quantitative_claims_verified"`
	ExplicitSourceMatched              bool    `json:"explicit_source_matched"`
	UnknownSpecificTermCount           int     `json:"unknown_specific_term_count"`
	ObligationSubjectVerified          bool    `json:"obligation_subject_verified"`
	CompositeClaimEvidence             bool    `json:"composite_claim_evidence"`
	ExceptionConditionsVerified        bool    `json:"exception_conditions_verified"`
	ContrastiveAlternative             bool    `json:"contrastive_alternative"`
	ContrastiveAlternativeVerified     bool    `json:"contrastive_alternative_verified"`
	NormativeCounterEvidence           bool    `json:"normative_counter_evidence"`
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
	Query                         string
	Filter                        Filter
	DomainExpansionApplied        bool
	DomainExpansionMatchedTerms   int
	ReviewedExpansionApplied      bool
	ReviewedExpansionMatchedTerms int
	ReviewedExpansionExactMatch   bool
	ContractValid                 bool
	UnknownSpecificTermCount      int
	Results                       []SearchResult
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

	topEvidence := input.Results[0].EvidenceMatches
	if features.QueryTermCount <= 1 && !filterDisambiguatesBroadQuery(input.Filter) &&
		(!features.ReviewedExpansionExactMatch || features.SelectedEvidenceMaxLexicalCoverage >= 1.0/3.0) {
		decision.Status = AnswerabilityAmbiguous
		decision.ReasonCodes = []string{"broad_query"}
		decision.Clarification = "질문의 대상 시장, 규정 종류, 상품 또는 행위를 더 구체적으로 지정하세요."
		decision.EvidenceChunkIDs = evidenceChunkIDs(topEvidence)
		return decision
	}
	if len(topEvidence) == 0 {
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
	if !features.SelectedEvidenceAnchored && selectedEvidenceHasVectorScore(input.Results[0].EvidenceMatches) &&
		features.SelectedEvidenceMaxLexicalCoverage < 0.50 {
		decision.Status = AnswerabilityAmbiguous
		decision.ReasonCodes = []string{"unstructured_vector_evidence"}
		decision.Clarification = "검색된 의미상 후보의 규정, 조문 또는 첨부 대상을 더 구체적으로 지정하세요."
		decision.EvidenceChunkIDs = evidenceChunkIDs(topEvidence)
		return decision
	}

	directLexicalEvidence := features.SelectedEvidenceAnchored && features.SelectedEvidenceMaxLexicalCoverage >= 0.60 &&
		features.UnknownSpecificTermCount <= 1
	knownLexicalEvidence := features.SelectedEvidenceAnchored && features.UnknownSpecificTermCount == 0 &&
		features.SelectedEvidenceMaxLexicalCoverage >= 1.0/3.0
	reviewedAliasEvidence := features.ReviewedExpansionExactMatch && features.SelectedEvidenceAnchored
	reviewedExpansionEvidence := features.ReviewedExpansion && features.SelectedEvidenceAnchored &&
		((features.UnknownSpecificTermCount == 0 && features.SelectedEvidenceMaxLexicalCoverage >= 0.25) ||
			(features.QueryTermCount >= 5 && features.UnknownSpecificTermCount*2 <= features.QueryTermCount &&
				features.SelectedEvidenceMaxLexicalCoverage >= 0.25) ||
			(features.ReviewedExpansionMatchedTerms >= 2 && features.QueryTermCount >= 5 &&
				features.SelectedEvidenceMaxLexicalCoverage >= 0.125))
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
	if directLexicalEvidence || knownLexicalEvidence || reviewedAliasEvidence || reviewedExpansionEvidence || strongChannelAgreement ||
		semanticChannelAgreement || quantitativeEvidence || normativeCounterEvidence || documentEvidence {
		decision.Status = AnswerabilitySupported
		decision.ReasonCodes = []string{"direct_evidence"}
		if strongChannelAgreement || semanticChannelAgreement {
			decision.ReasonCodes = append(decision.ReasonCodes, "bm25_vector_agreement")
		}
		if reviewedAliasEvidence || reviewedExpansionEvidence {
			decision.ReasonCodes = append(decision.ReasonCodes, "reviewed_domain_expansion")
		}
		if normativeCounterEvidence {
			decision.ReasonCodes = append(decision.ReasonCodes, "normative_counter_evidence")
		}
		decision.EvidenceChunkIDs = evidenceChunkIDs(topEvidence)
		return decision
	}

	decision.ReasonCodes = []string{"low_query_evidence_coverage", "no_supported_evidence"}
	return decision
}

func answerabilityFeatures(input AnswerabilityInput) AnswerabilityFeatures {
	selectedResults := selectedEvidenceResults(input.Results)
	features := AnswerabilityFeatures{
		QueryTermCount:                 len(meaningfulQueryTerms(input.Query)),
		ResultCount:                    len(input.Results),
		DomainExpansion:                input.DomainExpansionApplied,
		DomainExpansionMatchedTerms:    input.DomainExpansionMatchedTerms,
		ReviewedExpansion:              input.ReviewedExpansionApplied,
		ReviewedExpansionMatchedTerms:  input.ReviewedExpansionMatchedTerms,
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
	}
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
	if len(input.Results) == 0 || len(input.Results[0].EvidenceMatches) == 0 {
		return features
	}
	for _, match := range input.Results[0].EvidenceMatches {
		if match.LexicalCoverage > features.SelectedEvidenceMaxLexicalCoverage {
			features.SelectedEvidenceMaxLexicalCoverage = match.LexicalCoverage
		}
		if match.ArticleID != "" || match.AttachmentID != "" || len(match.HeadingPath) > 0 {
			features.SelectedEvidenceAnchored = true
		}
		if match.BM25Score > 0 && match.VectorScore > 0 {
			features.BM25VectorAgreement = true
		}
	}
	return features
}

func selectedEvidenceHasVectorScore(matches []EvidenceMatch) bool {
	for _, match := range matches {
		if match.VectorScore > 0 {
			return true
		}
	}
	return false
}

func selectedEvidenceResults(results []SearchResult) []SearchResult {
	if len(results) == 0 {
		return nil
	}
	return results[:1]
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
	evidence := normalizeClaimText(searchEvidenceText(results))
	for _, field := range strings.Fields(query) {
		field = strings.Trim(field, ".,?!:;()[]{}\"'")
		for _, suffix := range []string{"이라면", "라면", "이면", "하면"} {
			if !strings.HasSuffix(field, suffix) {
				continue
			}
			stem := strings.TrimSuffix(field, suffix)
			if exceptionConditionStem(stem) && !strings.Contains(evidence, normalizeClaimText(stem)) {
				return false
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
	for _, marker := range []string{"대신", "말고", "아니라", "대체하여", "대체해서", "대체해", "쓰지않고"} {
		if strings.Contains(evidence, marker) {
			return true
		}
	}
	return false
}

func normativeCounterEvidence(query string, results []SearchResult) bool {
	query = normalizeClaimText(query)
	queryRequestsPermissionOrDeniesDuty := false
	for _, marker := range []string{
		"안되", "안된", "안돼", "필요가없", "하지않아도", "하지않고", "없이", "사용할수", "써도", "해도", "빼고", "가능",
	} {
		if strings.Contains(query, marker) {
			queryRequestsPermissionOrDeniesDuty = true
			break
		}
	}
	if !queryRequestsPermissionOrDeniesDuty {
		return false
	}
	evidence := normalizeClaimText(searchEvidenceText(results))
	for _, marker := range []string{
		"해야한다", "하여야한다", "할수없다", "하지못한다", "해서는아니된다", "금지", "의무", "반드시",
	} {
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
