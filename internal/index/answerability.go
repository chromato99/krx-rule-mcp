package index

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const AnswerabilityGateVersion = "evidence-gate-v3"

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
	QueryTermCount             int     `json:"query_term_count"`
	ResultCount                int     `json:"result_count"`
	TopLexicalCoverage         float64 `json:"top_lexical_coverage"`
	TopEvidenceAnchored        bool    `json:"top_evidence_anchored"`
	BM25VectorAgreement        bool    `json:"bm25_vector_agreement"`
	DomainExpansion            bool    `json:"domain_expansion"`
	DistinctTopCategories      int     `json:"distinct_top_categories"`
	TopScoreMargin             float64 `json:"top_score_margin"`
	FilterApplied              bool    `json:"filter_applied"`
	QuantitativeClaimsVerified bool    `json:"quantitative_claims_verified"`
	ExplicitSourceMatched      bool    `json:"explicit_source_matched"`
	UnknownSpecificTermCount   int     `json:"unknown_specific_term_count"`
	ObligationSubjectVerified  bool    `json:"obligation_subject_verified"`
	CompositeClaimEvidence     bool    `json:"composite_claim_evidence"`
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
	Query                    string
	Filter                   Filter
	DomainExpansionApplied   bool
	ContractValid            bool
	UnknownSpecificTermCount int
	Results                  []SearchResult
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
		(!input.DomainExpansionApplied || !features.TopEvidenceAnchored || features.TopLexicalCoverage >= 0.34) {
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
	if !features.TopEvidenceAnchored && input.Results[0].EvidenceMatches[0].VectorScore > 0 &&
		features.TopLexicalCoverage < 0.50 {
		decision.Status = AnswerabilityAmbiguous
		decision.ReasonCodes = []string{"unstructured_vector_evidence"}
		decision.Clarification = "검색된 의미상 후보의 규정, 조문 또는 첨부 대상을 더 구체적으로 지정하세요."
		decision.EvidenceChunkIDs = evidenceChunkIDs(topEvidence)
		return decision
	}

	directLexicalEvidence := features.TopEvidenceAnchored && features.TopLexicalCoverage >= 0.60
	expandedDirectEvidence := input.DomainExpansionApplied && features.TopEvidenceAnchored && !strings.Contains(input.Query, "대신")
	strongChannelAgreement := features.BM25VectorAgreement && features.TopEvidenceAnchored &&
		features.UnknownSpecificTermCount <= 1 && features.TopLexicalCoverage >= 1.0/3.0
	semanticChannelAgreement := features.BM25VectorAgreement && features.TopEvidenceAnchored &&
		features.UnknownSpecificTermCount <= 1 &&
		((features.QueryTermCount >= 5 && features.TopLexicalCoverage >= 0.20 && !strings.Contains(input.Query, "대신")) ||
			(features.QueryTermCount >= 8 && features.TopLexicalCoverage >= 0.125))
	negatedClaimEvidence := features.BM25VectorAgreement && features.TopEvidenceAnchored &&
		(strings.Contains(input.Query, "안 되") || strings.Contains(input.Query, "안 된") ||
			strings.Contains(input.Query, "금지"))
	documentEvidence := !features.TopEvidenceAnchored && features.QueryTermCount >= 2 && features.TopLexicalCoverage >= 0.50
	if directLexicalEvidence || expandedDirectEvidence || strongChannelAgreement || semanticChannelAgreement || negatedClaimEvidence || documentEvidence {
		decision.Status = AnswerabilitySupported
		decision.ReasonCodes = []string{"direct_evidence"}
		if strongChannelAgreement || semanticChannelAgreement || negatedClaimEvidence {
			decision.ReasonCodes = append(decision.ReasonCodes, "bm25_vector_agreement")
		}
		if input.DomainExpansionApplied {
			decision.ReasonCodes = append(decision.ReasonCodes, "reviewed_domain_expansion")
		}
		decision.EvidenceChunkIDs = evidenceChunkIDs(topEvidence)
		return decision
	}

	decision.ReasonCodes = []string{"low_query_evidence_coverage", "no_supported_evidence"}
	return decision
}

func answerabilityFeatures(input AnswerabilityInput) AnswerabilityFeatures {
	features := AnswerabilityFeatures{
		QueryTermCount:             len(meaningfulQueryTerms(input.Query)),
		ResultCount:                len(input.Results),
		DomainExpansion:            input.DomainExpansionApplied,
		FilterApplied:              filterApplied(input.Filter),
		QuantitativeClaimsVerified: quantitativeClaimsVerified(input.Query, input.Results),
		ExplicitSourceMatched:      explicitSourceMatched(input.Query, input.Results),
		UnknownSpecificTermCount:   input.UnknownSpecificTermCount,
		ObligationSubjectVerified:  obligationSubjectVerified(input.Query, input.Results),
		CompositeClaimEvidence:     compositeClaimEvidence(input.Query, input.Results),
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
	top := input.Results[0].EvidenceMatches[0]
	features.TopLexicalCoverage = top.LexicalCoverage
	features.TopEvidenceAnchored = top.ArticleID != "" || top.AttachmentID != "" || len(top.HeadingPath) > 0
	features.BM25VectorAgreement = top.BM25Score > 0 && top.VectorScore > 0
	return features
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
	quantitativeClaimPattern = regexp.MustCompile(`(?i)(제\s*[0-9]+조(?:의\s*[0-9]+)?|별표\s*[0-9]+|min\s*[0-9]+|[0-9]{1,2}:[0-9]{2}|(?:오전|오후)?\s*[0-9]+(?:\.[0-9]+)?\s*(?:%|％|퍼센트|년|개월|일|시|분|초))`)
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
		for _, alias := range quantitativeClaimAliases(claim) {
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

func normalizeClaimText(value string) string {
	value = strings.ToLower(value)
	value = strings.NewReplacer("%", "퍼센트", "％", "퍼센트").Replace(value)
	return strings.Join(strings.Fields(value), "")
}

func quantitativeClaimAliases(claim string) []string {
	normalized := normalizeClaimText(claim)
	aliases := []string{normalized}
	number := claimNumberPattern.FindString(normalized)
	if strings.HasPrefix(normalized, "min") && number != "" {
		aliases = append(aliases, number)
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
		aliases = append(aliases, strconv.Itoa(hour)+"시")
		if hour >= 12 {
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

func obligationSubjectVerified(query string, results []SearchResult) bool {
	if !strings.Contains(query, "의무") {
		return true
	}
	if len(results) == 0 || len(results[0].EvidenceMatches) == 0 {
		return false
	}
	top := results[0].EvidenceMatches[0]
	evidence := normalizeClaimText(results[0].Title + " " + top.ArticleID + " " +
		strings.Join(top.HeadingPath, " ") + " " + top.Snippet + " " + top.Text)
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
	return len(results) > 0 && len(results[0].EvidenceMatches) > 0 &&
		results[0].EvidenceMatches[0].LexicalCoverage >= 0.60
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
		for _, particle := range []string{"에서", "에는", "으로", "상", "의", "에"} {
			if strings.HasSuffix(field, particle) {
				field = strings.TrimSuffix(field, particle)
				break
			}
		}
		if field == "규정" || field == "법" {
			continue
		}
		if strings.HasSuffix(field, "규정") || strings.HasSuffix(field, "법") {
			sources = append(sources, field)
		}
	}
	return sources
}
