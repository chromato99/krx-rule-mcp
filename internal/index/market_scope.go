package index

import (
	"regexp"
	"strings"
)

var titleDateSuffix = regexp.MustCompile(`[0-9]{8}$`)

// An explicitly named complete title chooses the searchable source before
// top-K truncation. A longer title (e.g. enforcement rules) wins over its
// contained parent title. Partial market/topic names never use this path.
func (e *Engine) exactNamedSourceIDs(query string, filter Filter) map[string]struct{} {
	if multiDocumentEvidenceIntent(query) {
		return nil
	}
	query = normalizeClaimText(query)
	matched := map[string]map[string]struct{}{}
	for _, doc := range e.docs {
		if !matchesFilter(doc, filter) {
			continue
		}
		for _, raw := range e.sourceTitles[doc.ID] {
			title := titleDateSuffix.ReplaceAllString(normalizeClaimText(raw), "")
			length := runeLen(title)
			if !legalDocumentTitle(title) || length < 4 || !strings.Contains(query, title) {
				continue
			}
			if matched[title] == nil {
				matched[title] = map[string]struct{}{}
			}
			matched[title][doc.ID] = struct{}{}
		}
	}
	var selected map[string]struct{}
	for title, ids := range matched {
		contained := false
		for other := range matched {
			if title != other && strings.Contains(other, title) {
				contained = true
				break
			}
		}
		if contained {
			continue
		}
		// Different full titles can express a cross-reference even without a
		// comparison word. Do not choose one simply because its name is longer.
		if selected != nil {
			return nil
		}
		selected = ids
	}
	return selected
}

func legalDocumentTitle(title string) bool {
	title = titleDateSuffix.ReplaceAllString(normalizeClaimText(title), "")
	for _, suffix := range []string{"규정", "시행세칙", "지침", "기준", "계약서", "정관", "regulation", "regulations", "rules", "guidelines", "articlesofincorporation"} {
		if strings.HasSuffix(title, suffix) {
			return true
		}
	}
	for _, prefix := range []string{"enforcementrulesof", "enforcementrulesfor", "regulationson", "regulationon", "guidelinesfor", "guidelineson"} {
		if strings.HasPrefix(title, prefix) {
			return true
		}
	}
	return false
}

func removeSourceTitle(query, title string) string {
	title = titleDateSuffix.ReplaceAllString(strings.TrimSpace(title), "")
	var parts []string
	for _, letter := range strings.Join(strings.Fields(title), "") {
		parts = append(parts, regexp.QuoteMeta(string(letter)))
	}
	if len(parts) == 0 {
		return query
	}
	pattern := regexp.MustCompile(`(?i)` + strings.Join(parts, `\s*`) + `(?:에서의|에서|에는|으로|의|상|에|은|는)?`)
	return strings.TrimSpace(pattern.ReplaceAllString(query, " "))
}

func namedDocumentScope(query, title string) bool {
	core := titleDateSuffix.ReplaceAllString(normalizeClaimText(title), "")
	legalTitle := false
	for _, prefix := range []string{"enforcementrulesof", "enforcementrulesfor", "guidelinesforthemanagementof", "guidelinesforthe", "guidelinesfor", "guidelineson"} {
		trimmed := strings.TrimPrefix(core, prefix)
		legalTitle = legalTitle || trimmed != core
		core = trimmed
	}
	for _, suffix := range []string{"시행세칙", "업무규정", "운영규정", "운영지침", "관리지침", "규정", "지침", "businessregulations", "businessregulation", "regulations", "regulation", "rules"} {
		trimmed := strings.TrimSuffix(core, suffix)
		legalTitle = legalTitle || trimmed != core
		core = trimmed
	}
	if strings.HasSuffix(core, "s") && !strings.HasSuffix(core, "ss") {
		core = strings.TrimSuffix(core, "s")
	}
	// A market name alone does not choose its business rule over its listing,
	// disclosure or other rules; that constraint is handled by market scope.
	if strings.HasSuffix(core, "시장") || strings.HasSuffix(core, "market") {
		return false
	}
	return legalTitle && runeLen(core) >= 3 && strings.Contains(normalizeClaimText(query), core)
}

func filterNamedDocumentResults(query string, results []SearchResult) []SearchResult {
	if multiDocumentEvidenceIntent(query) || len(explicitMarketScopes(query)) > 1 {
		return results
	}
	var matched []SearchResult
	for _, result := range results {
		if namedDocumentScope(query, result.Title) {
			matched = append(matched, result)
		}
	}
	if len(matched) > 0 {
		return matched
	}
	return results
}

// Market names constrain the scope of a rule, independently of a chunk's
// lexical/similarity score. These are market-name aliases, not query templates.
func explicitMarketScopes(query string) [][]string {
	var names []string
	if strings.Contains(query, "배출권") {
		names = append(names, "배출권시장")
	}
	fields := strings.Fields(strings.ToLower(query))
	for i, field := range fields {
		field = strings.Trim(field, ".,?!:;()[]{}\"'")
		if position := strings.Index(field, "시장"); position > 0 {
			name := field[:position+len("시장")]
			if name == "현물시장" && i > 0 {
				name = fields[i-1] + "시장"
			}
			name = strings.ReplaceAll(name, "현물시장", "시장")
			name = strings.TrimPrefix(name, "krx")
			if name == "파생상품시장" && i > 0 && fields[i-1] == "장외" {
				name = "장외파생상품시장"
			}
			names = append(names, name)
		}
		for _, name := range []string{"kospi", "kosdaq", "konex"} {
			if field == name || field == name+"의" {
				// KOSPI 200 / KOSDAQ 150 name indexes and their products, not
				// an explicit cash-market scope. Keep those candidates available.
				if i+1 < len(fields) {
					next := strings.Trim(fields[i+1], ".,?!:;()[]{}\"'")
					if next != "" && next[0] >= '0' && next[0] <= '9' {
						continue
					}
				}
				names = append(names, name)
			}
		}
	}
	var scopes [][]string
	seen := map[string]bool{}
	for _, name := range names {
		var terms []string
		switch name {
		case "금시장":
			terms = []string{"금시장", "goldmarket"}
		case "배출권시장", "배출권거래시장":
			terms = []string{"배출권", "emissions"}
		case "유가증권시장", "코스피시장", "kospi":
			terms = []string{"유가증권시장", "securitiesmarket", "kospi"}
		case "코스닥시장", "kosdaq":
			terms = []string{"코스닥", "kosdaq"}
		case "코넥스시장", "konex":
			terms = []string{"코넥스", "konex"}
		case "주식시장":
			terms = []string{"유가증권시장", "코스닥", "코넥스", "securitiesmarket", "kosdaq", "konex"}
		case "파생상품시장", "선물시장":
			terms = []string{"파생상품시장", "derivativesmarket"}
		case "장외파생상품시장":
			terms = []string{"장외파생상품", "otcderivatives"}
		default:
			terms = []string{name}
		}
		if !seen[terms[0]] {
			seen[terms[0]] = true
			scopes = append(scopes, terms)
		}
	}
	return scopes
}

func resultMatchesMarket(result SearchResult, scope []string) bool {
	text := normalizeClaimText(result.Title + " " + result.Category + " " + searchEvidenceText([]SearchResult{result}))
	for _, term := range scope {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func filterExplicitMarketResults(query string, results []SearchResult) []SearchResult {
	scopes := explicitMarketScopes(query)
	if len(scopes) == 0 {
		return results
	}
	filtered := make([]SearchResult, 0, len(results))
	for _, result := range results {
		for _, scope := range scopes {
			if resultMatchesMarket(result, scope) {
				filtered = append(filtered, result)
				break
			}
		}
	}
	return filtered
}
