package evaluation

import "testing"

func TestFixtureComparisonExcludesOnlyLifecycleLabels(t *testing.T) {
	fixture := Fixture{Cases: []Case{{ID: "a", Split: "holdout", Group: "semantic", Input: CaseInput{Query: "original"}, Expectation: Expectation{EvidenceStatus: "supported", Targets: []Target{{DocumentID: "rule", ArticleID: "제1조"}}}}}}
	before := FixtureContractHash(fixture)
	fixture.Cases[0].Split = "validation"
	fixture.Cases[0].Group = "consumed"
	if FixtureContractHash(fixture) != before {
		t.Fatal("lifecycle change invalidated question comparison")
	}
	fixture.Cases[0].Expectation.Targets[0].ArticleID = "제2조"
	if FixtureContractHash(fixture) == before {
		t.Fatal("target rewrite did not invalidate comparison")
	}
}
