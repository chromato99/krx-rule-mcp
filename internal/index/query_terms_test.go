package index

import (
	"slices"
	"testing"
)

func TestExplicitIdentifierEvidenceTermsHandleKoreanParticles(t *testing.T) {
	terms := ExplicitIdentifierEvidenceTerms("ETF는 NAV에서 LP가 어떤 호가를 내나")
	for _, want := range []string{"상장지수집합투자기구", "순자산가치", "유동성공급호가"} {
		if !slices.Contains(terms, want) {
			t.Fatalf("identifier evidence terms = %#v, want %q", terms, want)
		}
	}
	entries := []DomainLexiconEntry{{
		ID: "nav", Canonical: "순자산가치", Aliases: []string{"NAV"}, Expansions: []string{"괴리율"}, EvidenceTerms: []string{"net asset value"},
		Confidence: "high", ReviewStatus: "official-glossary",
	}}
	expansion := ExpandDomainQueryWithLexicon("NAV에서 벌어지는가", entries)
	if !expansion.Applied() || expansion.ReviewedMatchCount() != 1 {
		t.Fatalf("particle-suffixed acronym expansion = %#v", expansion)
	}
	concepts := expansion.ReviewedEvidenceConcepts()
	if len(concepts) != 1 || !slices.Contains(concepts[0], "순자산가치") || !slices.Contains(concepts[0], "net asset value") || slices.Contains(concepts[0], "괴리율") {
		t.Fatalf("reviewed evidence concepts = %#v", concepts)
	}
}

func TestMeaningfulQueryTermsNormalizeKoreanParticles(t *testing.T) {
	got := meaningfulQueryTerms("ETF 종가가 NAV에서 호가를 내야 하나")
	want := []string{"etf", "종가", "nav", "호가", "내야"}
	if !slices.Equal(got, want) {
		t.Fatalf("meaningfulQueryTerms() = %#v, want %#v", got, want)
	}
}

func TestRankingNumericTermsKeepValueBoundaries(t *testing.T) {
	for _, test := range []struct{ text, claim string }{
		{"14시", "4시"}, {"73%", "3%"}, {"103일", "3일"}, {"1.4시", "4시"},
	} {
		if containsQuantitativeClaim(normalizeClaimText(test.text), normalizeClaimText(test.claim)) {
			t.Fatalf("partial numeric match: %q in %q", test.claim, test.text)
		}
	}
	terms := QuantitativeEvidenceTerms("오후 2시까지 납부")
	if !slices.Contains(terms, "14시") || !slices.Contains(terms, "14:00") {
		t.Fatalf("clock equivalents missing: %v", terms)
	}
}
