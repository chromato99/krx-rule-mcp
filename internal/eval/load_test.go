package evaluation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestLoadFixtureRejectsRemovedAnswerabilityControls(t *testing.T) {
	for _, field := range []string{"allow_results", "clarification_required"} {
		path := filepath.Join(t.TempDir(), "fixture.json")
		data := `{"schema_version":2,"cases":[{"expectation":{"` + field + `":true}}]}`
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadFixture(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("removed control %s accepted: %v", field, err)
		}
	}
}

func TestFixtureRequiresCurrentSchema(t *testing.T) {
	if err := ValidateFixture(Fixture{SchemaVersion: 1}); err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("old fixture schema accepted: %v", err)
	}
}
