package evaluation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var (
	caseIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	hashPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func LoadFixture(path string) (Fixture, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return Fixture{}, "", fmt.Errorf("open evaluation fixture: %w", err)
	}
	defer file.Close()
	hasher := sha256.New()
	decoder := json.NewDecoder(io.TeeReader(file, hasher))
	decoder.DisallowUnknownFields()
	var fixture Fixture
	if err := decoder.Decode(&fixture); err != nil {
		return Fixture{}, "", fmt.Errorf("decode evaluation fixture: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Fixture{}, "", fmt.Errorf("decode evaluation fixture: trailing JSON value")
	}
	if err := ValidateFixture(fixture); err != nil {
		return Fixture{}, "", err
	}
	return fixture, hex.EncodeToString(hasher.Sum(nil)), nil
}

func ValidateFixture(fixture Fixture) error {
	if fixture.SchemaVersion != 1 {
		return fmt.Errorf("evaluation fixture schema_version = %d, want 1", fixture.SchemaVersion)
	}
	if !strings.HasPrefix(fixture.FixtureVersion, "rag-v") {
		return fmt.Errorf("evaluation fixture version %q is invalid", fixture.FixtureVersion)
	}
	if len(fixture.Cases) < 120 {
		return fmt.Errorf("evaluation fixture has %d cases, want at least 120", len(fixture.Cases))
	}
	if fixture.Policies.ChunkIDsInExpectations {
		return fmt.Errorf("evaluation fixture must not pin release-specific chunk ids")
	}
	if fixture.Policies.ScoresAreConfidence {
		return fmt.Errorf("evaluation fixture must not treat ranking scores as confidence")
	}
	if !hashPattern.MatchString(fixture.Source.SHA256) {
		return fmt.Errorf("evaluation source sha256 %q is invalid", fixture.Source.SHA256)
	}
	seen := map[string]struct{}{}
	for index, item := range fixture.Cases {
		if !caseIDPattern.MatchString(item.ID) {
			return fmt.Errorf("evaluation case %d has invalid id %q", index, item.ID)
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return fmt.Errorf("evaluation case id %q is duplicated", item.ID)
		}
		seen[item.ID] = struct{}{}
		if err := validateCase(item); err != nil {
			return fmt.Errorf("evaluation case %q: %w", item.ID, err)
		}
	}
	return nil
}

func VerifySourceFixture(path, expectedHash string, expectedCases int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read source fixture: %w", err)
	}
	sum := sha256.Sum256(data)
	observed := hex.EncodeToString(sum[:])
	if observed != expectedHash {
		return fmt.Errorf("source fixture sha256 = %s, want %s", observed, expectedHash)
	}
	var source struct {
		Cases []json.RawMessage `json:"cases"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return fmt.Errorf("decode source fixture: %w", err)
	}
	if len(source.Cases) != expectedCases {
		return fmt.Errorf("source fixture has %d cases, want %d", len(source.Cases), expectedCases)
	}
	return nil
}

func validateCase(item Case) error {
	if strings.TrimSpace(item.Group) == "" {
		return fmt.Errorf("group is required")
	}
	switch item.Split {
	case "regression", "development", "holdout":
	default:
		return fmt.Errorf("unsupported split %q", item.Split)
	}
	if strings.TrimSpace(item.Input.Query) == "" {
		return fmt.Errorf("input.query is required")
	}
	if item.Input.Limit < 0 || item.Input.Limit > 50 {
		return fmt.Errorf("input.limit must be between 1 and 50, or 0 for default")
	}
	switch item.Expectation.EvidenceStatus {
	case "supported", "insufficient", "ambiguous", "unknown":
	default:
		return fmt.Errorf("unsupported evidence_status %q", item.Expectation.EvidenceStatus)
	}
	switch item.Expectation.ClaimRelation {
	case "supports", "contradicts", "not_applicable":
	default:
		return fmt.Errorf("unsupported claim_relation %q", item.Expectation.ClaimRelation)
	}
	switch item.Expectation.TargetPolicy {
	case "any", "all":
	case "at_least":
		if item.Expectation.AtLeast <= 0 || item.Expectation.AtLeast > len(item.Expectation.Targets) {
			return fmt.Errorf("at_least must be between 1 and target count")
		}
	default:
		return fmt.Errorf("unsupported target_policy %q", item.Expectation.TargetPolicy)
	}
	if item.Expectation.EvidenceStatus == "supported" && len(item.Expectation.Targets) == 0 {
		return fmt.Errorf("supported case requires at least one target")
	}
	if item.Expectation.EvidenceStatus == "ambiguous" && !item.Expectation.ClarificationRequired {
		return fmt.Errorf("ambiguous case requires clarification_required")
	}
	for _, target := range item.Expectation.Targets {
		if strings.TrimSpace(target.DocumentID) == "" {
			return fmt.Errorf("target.document_id is required")
		}
		if target.Evidence != nil && target.ArticleID == "" && target.AttachmentID == "" {
			return fmt.Errorf("evidence text conditions require article_id or attachment_id")
		}
		if item.Expectation.ClaimRelation == "contradicts" &&
			(target.Evidence == nil || len(target.Evidence.RelationMustContainAny) == 0) {
			return fmt.Errorf("contradicts target requires relation_must_contain_any evidence")
		}
	}
	return nil
}
