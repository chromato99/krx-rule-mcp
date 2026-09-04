package index

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

	defaultconfig "github.com/chromato99/krx-rule-mcp/config"
	"github.com/chromato99/krx-rule-mcp/internal/model"
	"gopkg.in/yaml.v3"
)

const DefaultDomainLexiconPath = "config/domain-lexicon.yaml"

type DomainLexiconEntry struct {
	ID            string     `json:"id" yaml:"id"`
	Canonical     string     `json:"canonical" yaml:"canonical"`
	Aliases       []string   `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	MatchGroups   [][]string `json:"match_groups,omitempty" yaml:"match_groups,omitempty"`
	Expansions    []string   `json:"expansions,omitempty" yaml:"expansions,omitempty"`
	EvidenceTerms []string   `json:"evidence_terms,omitempty" yaml:"evidence_terms,omitempty"`
	SourceURLs    []string   `json:"source_urls,omitempty" yaml:"source_urls,omitempty"`
	Confidence    string     `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	ReviewStatus  string     `json:"review_status,omitempty" yaml:"review_status,omitempty"`
	Note          string     `json:"note,omitempty" yaml:"note,omitempty"`
}

type DomainLexiconMatch struct {
	ID            string   `json:"id"`
	Canonical     string   `json:"canonical"`
	MatchedTerms  []string `json:"matched_terms,omitempty"`
	MatchedGroups int      `json:"matched_groups,omitempty"`
	AddedTerms    []string `json:"added_terms,omitempty"`
	EvidenceTerms []string `json:"evidence_terms,omitempty"`
	SourceURLs    []string `json:"source_urls,omitempty"`
	Confidence    string   `json:"confidence,omitempty"`
	ReviewStatus  string   `json:"review_status,omitempty"`
	Note          string   `json:"note,omitempty"`
}

type DomainQueryExpansion struct {
	OriginalQuery string               `json:"original_query"`
	ExpandedQuery string               `json:"expanded_query"`
	AppliedTerms  []DomainLexiconMatch `json:"applied_terms,omitempty"`
}

func (e DomainQueryExpansion) Applied() bool {
	return len(e.AppliedTerms) > 0
}

// Reviewed reports whether the expansion contains at least one curated,
// high-confidence lexicon match. Unreviewed matches may still improve ranking,
// but are not answerability evidence.
func (e DomainQueryExpansion) Reviewed() bool {
	return e.ReviewedMatchCount() > 0
}

func (e DomainQueryExpansion) MatchedTermCount() int {
	return e.matchedTermCount(false)
}

func (e DomainQueryExpansion) ReviewedMatchCount() int {
	return e.matchedTermCount(true)
}

func (e DomainQueryExpansion) ReviewedMatchedGroupCount() int {
	maxGroups := 0
	for _, match := range e.AppliedTerms {
		if reviewedLexiconMatch(match) && match.MatchedGroups > maxGroups {
			maxGroups = match.MatchedGroups
		}
	}
	return maxGroups
}

func (e DomainQueryExpansion) ReviewedAppliedTerms() []DomainLexiconMatch {
	var reviewed []DomainLexiconMatch
	for _, match := range e.AppliedTerms {
		if reviewedLexiconMatch(match) {
			reviewed = append(reviewed, match)
		}
	}
	return reviewed
}

func (e DomainQueryExpansion) ReviewedEvidenceConcepts() [][]string {
	var concepts [][]string
	for _, match := range e.ReviewedAppliedTerms() {
		// AddedTerms may contain broad recall expansions. EvidenceTerms are a
		// separate, reviewed set used for final evidence selection.
		concept := uniqueTerms(append([]string{match.Canonical}, match.EvidenceTerms...))
		if len(concept) > 0 {
			concepts = append(concepts, concept)
		}
	}
	return concepts
}

func (e DomainQueryExpansion) matchedTermCount(reviewedOnly bool) int {
	seen := map[string]struct{}{}
	for _, match := range e.AppliedTerms {
		if reviewedOnly && !reviewedLexiconMatch(match) {
			continue
		}
		for _, term := range match.MatchedTerms {
			if normalized := normalizeLexiconTerm(term); normalized != "" {
				seen[normalized] = struct{}{}
			}
		}
	}
	return len(seen)
}

// ReviewedExactMatch reports whether a high-confidence reviewed lexicon term
// covers the whole user query. This is intentionally stricter than Applied:
// substring matches may improve recall, but must not by themselves make a
// mixed or out-of-domain query answerable.
func (e DomainQueryExpansion) ReviewedExactMatch() bool {
	query := normalizeLexiconTerm(e.OriginalQuery)
	if query == "" {
		return false
	}
	for _, match := range e.AppliedTerms {
		if !reviewedLexiconMatch(match) {
			continue
		}
		for _, term := range match.MatchedTerms {
			if normalizeLexiconTerm(term) == query {
				return true
			}
		}
	}
	return false
}

func reviewedLexiconMatch(match DomainLexiconMatch) bool {
	if !strings.EqualFold(strings.TrimSpace(match.Confidence), "high") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(match.ReviewStatus)) {
	case "curated", "curated-corpus", "corpus-derived", "official-glossary", "official-source":
		return true
	default:
		return false
	}
}

func (e DomainQueryExpansion) TokenWeights(expansionWeight float64) map[string]float64 {
	if expansionWeight <= 0 || expansionWeight > 1 {
		expansionWeight = 0.4
	}
	weights := map[string]float64{}
	for _, token := range Tokenize(e.OriginalQuery) {
		weights[token] = 1
	}
	for _, term := range e.AppliedTerms {
		for _, added := range term.AddedTerms {
			for _, token := range Tokenize(added) {
				if _, ok := weights[token]; ok {
					continue
				}
				weights[token] = expansionWeight
			}
		}
	}
	if len(weights) == 0 {
		return nil
	}
	return weights
}

func LoadDomainLexicon(path string) ([]DomainLexiconEntry, error) {
	entries, _, err := LoadDomainLexiconWithDigest(path)
	return entries, err
}

// LoadDomainLexiconWithDigest returns the SHA-256 digest of the exact YAML
// bytes parsed, including the embedded fallback when the default file is not
// present.
func LoadDomainLexiconWithDigest(path string) ([]DomainLexiconEntry, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if strings.TrimSpace(path) != "" && path != DefaultDomainLexiconPath {
			return nil, "", fmt.Errorf("read domain lexicon %q: %w", path, err)
		}
		data = defaultconfig.DomainLexiconYAML
	}
	var doc struct {
		Entries []DomainLexiconEntry `yaml:"entries"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, "", fmt.Errorf("parse domain lexicon %q: %w", path, err)
	}
	entries := doc.Entries
	if len(entries) == 0 {
		if err := yaml.Unmarshal(data, &entries); err != nil {
			return nil, "", fmt.Errorf("parse domain lexicon %q as entry list: %w", path, err)
		}
	}
	entries, err = normalizeDomainLexicon(entries)
	if err != nil {
		return nil, "", fmt.Errorf("validate domain lexicon %q: %w", path, err)
	}
	return entries, model.HashBytes(data), nil
}

func ExpandDomainQueryWithLexicon(query string, entries []DomainLexiconEntry) DomainQueryExpansion {
	query = strings.TrimSpace(query)
	expansion := DomainQueryExpansion{OriginalQuery: query, ExpandedQuery: query}
	if query == "" {
		return expansion
	}
	var added []string
	seenAdded := map[string]struct{}{}
	for _, entry := range entries {
		matched, matchedGroups := matchedLexiconTerms(query, entry)
		if len(matched) == 0 {
			continue
		}
		entryAdded := uniqueTerms(append([]string{entry.Canonical}, entry.Expansions...))
		entryAdded = missingExpansionTerms(query, entryAdded, seenAdded)
		if len(entryAdded) == 0 {
			continue
		}
		added = append(added, entryAdded...)
		expansion.AppliedTerms = append(expansion.AppliedTerms, DomainLexiconMatch{
			ID:            entry.ID,
			Canonical:     entry.Canonical,
			MatchedTerms:  matched,
			MatchedGroups: matchedGroups,
			AddedTerms:    entryAdded,
			EvidenceTerms: append([]string(nil), entry.EvidenceTerms...),
			SourceURLs:    append([]string(nil), entry.SourceURLs...),
			Confidence:    entry.Confidence,
			ReviewStatus:  entry.ReviewStatus,
			Note:          entry.Note,
		})
	}
	if len(added) > 0 {
		expansion.ExpandedQuery = query + " " + strings.Join(added, " ")
	}
	return expansion
}

func matchedLexiconTerms(query string, entry DomainLexiconEntry) ([]string, int) {
	candidates := uniqueTerms(append([]string{entry.Canonical}, entry.Aliases...))
	var matched []string
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if !lexiconTermMatches(query, candidate) {
			continue
		}
		key := normalizeLexiconTerm(candidate)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		matched = append(matched, candidate)
	}
	matchedGroups := 0
	if grouped := matchedLexiconGroups(query, entry.MatchGroups); len(grouped) > 0 {
		matchedGroups = len(grouped)
		for _, candidate := range grouped {
			key := normalizeLexiconTerm(candidate)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			matched = append(matched, candidate)
		}
	}
	sort.Strings(matched)
	return matched, matchedGroups
}

// matchedLexiconGroups implements compositional intent matching. Every group
// must contribute at least one term, while terms within a group are
// alternatives. This avoids enumerating whole evaluation-query phrases.
func matchedLexiconGroups(query string, groups [][]string) []string {
	if len(groups) == 0 {
		return nil
	}
	var matched []string
	for _, group := range groups {
		var groupMatch string
		for _, term := range group {
			if lexiconTermMatches(query, term) {
				groupMatch = term
				break
			}
		}
		if groupMatch == "" {
			return nil
		}
		matched = append(matched, groupMatch)
	}
	return matched
}

func missingExpansionTerms(query string, terms []string, seen map[string]struct{}) []string {
	var out []string
	for _, term := range terms {
		key := normalizeLexiconTerm(term)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		if lexiconTermMatches(query, term) {
			seen[key] = struct{}{}
			continue
		}
		seen[key] = struct{}{}
		out = append(out, term)
	}
	return out
}

func uniqueTerms(terms []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, term := range terms {
		term = strings.TrimSpace(term)
		key := normalizeLexiconTerm(term)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, term)
	}
	return out
}

func lexiconTermMatches(query, term string) bool {
	query = strings.TrimSpace(query)
	term = strings.TrimSpace(term)
	if query == "" || term == "" {
		return false
	}
	if isShortASCIIAcronym(term) {
		want := strings.ToLower(term)
		for _, token := range Tokenize(query) {
			if token == want {
				return true
			}
		}
		for _, field := range strings.Fields(strings.ToLower(query)) {
			field = strings.Trim(field, ".,?!:;()[]{}\"'")
			if !strings.HasPrefix(field, want) {
				continue
			}
			suffix := strings.TrimPrefix(field, want)
			switch suffix {
			case "은", "는", "이", "가", "을", "를", "의", "에", "에서", "로", "으로", "와", "과":
				return true
			}
		}
		return false
	}
	return strings.Contains(normalizeLexiconTerm(query), normalizeLexiconTerm(term))
}

func normalizeLexiconTerm(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isShortASCIIAcronym(value string) bool {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) < 2 || len(runes) > 4 {
		return false
	}
	for _, r := range runes {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func normalizeDomainLexicon(entries []DomainLexiconEntry) ([]DomainLexiconEntry, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("no entries")
	}
	seenIDs := map[string]struct{}{}
	out := make([]DomainLexiconEntry, 0, len(entries))
	for i, entry := range entries {
		entry.ID = strings.TrimSpace(entry.ID)
		entry.Canonical = strings.TrimSpace(entry.Canonical)
		entry.Confidence = strings.TrimSpace(entry.Confidence)
		entry.ReviewStatus = strings.TrimSpace(entry.ReviewStatus)
		entry.Note = strings.TrimSpace(entry.Note)
		entry.Aliases = uniqueTerms(entry.Aliases)
		for groupIndex := range entry.MatchGroups {
			entry.MatchGroups[groupIndex] = uniqueTerms(entry.MatchGroups[groupIndex])
			if len(entry.MatchGroups[groupIndex]) == 0 {
				return nil, fmt.Errorf("entry %q has empty match group %d", entry.ID, groupIndex)
			}
		}
		entry.Expansions = uniqueTerms(entry.Expansions)
		entry.EvidenceTerms = uniqueTerms(entry.EvidenceTerms)
		entry.SourceURLs = trimUniqueStrings(entry.SourceURLs)
		if entry.ID == "" {
			return nil, fmt.Errorf("entry %d has empty id", i)
		}
		if entry.Canonical == "" {
			return nil, fmt.Errorf("entry %q has empty canonical", entry.ID)
		}
		if _, ok := seenIDs[entry.ID]; ok {
			return nil, fmt.Errorf("duplicate entry id %q", entry.ID)
		}
		seenIDs[entry.ID] = struct{}{}
		out = append(out, entry)
	}
	return out, nil
}

func trimUniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
