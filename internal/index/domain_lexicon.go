package index

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

	defaultconfig "github.com/chromato99/krx-rule-mcp/config"
	"gopkg.in/yaml.v3"
)

const DefaultDomainLexiconPath = "config/domain-lexicon.yaml"

type DomainLexiconEntry struct {
	ID           string   `json:"id" yaml:"id"`
	Canonical    string   `json:"canonical" yaml:"canonical"`
	Aliases      []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Expansions   []string `json:"expansions,omitempty" yaml:"expansions,omitempty"`
	SourceURLs   []string `json:"source_urls,omitempty" yaml:"source_urls,omitempty"`
	Confidence   string   `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	ReviewStatus string   `json:"review_status,omitempty" yaml:"review_status,omitempty"`
	Note         string   `json:"note,omitempty" yaml:"note,omitempty"`
}

type DomainLexiconMatch struct {
	ID           string   `json:"id"`
	Canonical    string   `json:"canonical"`
	MatchedTerms []string `json:"matched_terms,omitempty"`
	AddedTerms   []string `json:"added_terms,omitempty"`
	SourceURLs   []string `json:"source_urls,omitempty"`
	Confidence   string   `json:"confidence,omitempty"`
	ReviewStatus string   `json:"review_status,omitempty"`
	Note         string   `json:"note,omitempty"`
}

type DomainQueryExpansion struct {
	OriginalQuery string               `json:"original_query"`
	ExpandedQuery string               `json:"expanded_query"`
	AppliedTerms  []DomainLexiconMatch `json:"applied_terms,omitempty"`
}

func (e DomainQueryExpansion) Applied() bool {
	return len(e.AppliedTerms) > 0
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
	data, err := os.ReadFile(path)
	if err != nil {
		if strings.TrimSpace(path) != "" && path != DefaultDomainLexiconPath {
			return nil, fmt.Errorf("read domain lexicon %q: %w", path, err)
		}
		data = defaultconfig.DomainLexiconYAML
	}
	var doc struct {
		Entries []DomainLexiconEntry `yaml:"entries"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse domain lexicon %q: %w", path, err)
	}
	entries := doc.Entries
	if len(entries) == 0 {
		if err := yaml.Unmarshal(data, &entries); err != nil {
			return nil, fmt.Errorf("parse domain lexicon %q as entry list: %w", path, err)
		}
	}
	entries, err = normalizeDomainLexicon(entries)
	if err != nil {
		return nil, fmt.Errorf("validate domain lexicon %q: %w", path, err)
	}
	return entries, nil
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
		matched := matchedLexiconTerms(query, entry)
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
			ID:           entry.ID,
			Canonical:    entry.Canonical,
			MatchedTerms: matched,
			AddedTerms:   entryAdded,
			SourceURLs:   append([]string(nil), entry.SourceURLs...),
			Confidence:   entry.Confidence,
			ReviewStatus: entry.ReviewStatus,
			Note:         entry.Note,
		})
	}
	if len(added) > 0 {
		expansion.ExpandedQuery = query + " " + strings.Join(added, " ")
	}
	return expansion
}

func matchedLexiconTerms(query string, entry DomainLexiconEntry) []string {
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
	sort.Strings(matched)
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
		entry.Expansions = uniqueTerms(entry.Expansions)
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
