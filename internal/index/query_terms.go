package index

import (
	"regexp"
	"strconv"
	"strings"
)

// Query term extraction supports candidate ranking only; it does not decide answerability.
func multiDocumentEvidenceIntent(query string) bool {
	normalized := normalizeClaimText(query)
	for _, marker := range []string{"비교", "각각", "모두", "함께", "동시에", "양쪽", "둘다", "두규정", "세시장", "복수규정", "복수문서", "복수시장"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

var (
	quantitativeClaimPattern = regexp.MustCompile(`(?i)(제\s*[0-9]+조(?:의\s*[0-9]+)?|별표\s*[0-9]+|min\s*[0-9]+|[0-9]{1,2}:[0-9]{2}|(?:오전|오후)?\s*[0-9]+(?:\.[0-9]+)?\s*(?:%|％|퍼센트|년|개월|일|시|분|초|점))`)
	claimNumberPattern       = regexp.MustCompile(`[0-9]+`)
)

// Both arguments use normalizeClaimText. Whitespace normalization must not
// turn a suffix of a number into a matching value (4시 in 14시, 3% in 73%).
// An hour alone must also not match an explicitly different minute value.
func containsQuantitativeClaim(text, claim string) bool {
	if claim == "" {
		return false
	}
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
	for offset := 0; offset < len(text); {
		position := strings.Index(text[offset:], claim)
		if position < 0 {
			return false
		}
		start := offset + position
		end := start + len(claim)
		leftOK := start == 0 || !isDigit(text[start-1])
		if start >= 2 && text[start-1] == '.' && isDigit(text[start-2]) {
			leftOK = false
		}
		rightOK := end == len(text) || !isDigit(text[end])
		if end < len(text) && text[end] == '.' && end+1 < len(text) && isDigit(text[end+1]) {
			rightOK = false
		}
		if leftOK && rightOK {
			return true
		}
		offset = start + 1
	}
	return false
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

var explicitIdentifierPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9-]{1,7}`)

var explicitIdentifierEvidenceAliases = map[string][]string{
	"ESG": {"ESG"},
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
	seen := map[string]struct{}{}
	var terms []string
	for _, candidate := range explicitIdentifierPattern.FindAllString(query, -1) {
		candidate = strings.ToUpper(candidate)
		if _, ok := explicitIdentifierEvidenceAliases[candidate]; !ok {
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
		if len(explicitIdentifierEvidenceAliases[identifier]) == 1 {
			continue
		}
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
		if len(explicitIdentifierEvidenceAliases[identifier]) == 1 {
			continue
		}
		concepts = append(concepts, append([]string(nil), explicitIdentifierEvidenceAliases[identifier]...))
	}
	return concepts
}
