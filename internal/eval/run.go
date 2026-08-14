package evaluation

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	mcpserver "github.com/chromato99/krx-rule-mcp/internal/mcp"
)

type Client interface {
	SearchRules(context.Context, mcpserver.SearchRulesInput) (mcpserver.SearchRulesOutput, error)
	GetContext(context.Context, mcpserver.GetContextInput) (mcpserver.ContextOutput, error)
}

func Run(ctx context.Context, fixture Fixture, client Client, provenance Provenance) (Report, error) {
	if err := ValidateFixture(fixture); err != nil {
		return Report{}, err
	}
	return runFixture(ctx, fixture, client, provenance)
}

func RunSplit(ctx context.Context, fixture Fixture, split string, client Client, provenance Provenance) (Report, error) {
	if err := ValidateFixture(fixture); err != nil {
		return Report{}, err
	}
	selected := fixture
	selected.Cases = make([]Case, 0, len(fixture.Cases))
	for _, item := range fixture.Cases {
		if item.Split == split {
			selected.Cases = append(selected.Cases, item)
		}
	}
	if len(selected.Cases) == 0 {
		return Report{}, fmt.Errorf("fixture split %q has no cases", split)
	}
	return runFixture(ctx, selected, client, provenance)
}

func runFixture(ctx context.Context, fixture Fixture, client Client, provenance Provenance) (Report, error) {
	provenance.EvaluatorVersion = EvaluatorVersion
	provenance.FixtureVersion = fixture.FixtureVersion
	report := Report{
		SchemaVersion: 1,
		GeneratedAt:   time.Now().UTC(),
		Provenance:    provenance,
		Slices:        map[string]SliceMetrics{},
		Cases:         make([]CaseResult, 0, len(fixture.Cases)),
	}
	latencies := make([]float64, 0, len(fixture.Cases))
	for _, item := range fixture.Cases {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		started := time.Now()
		result := evaluateCase(ctx, item, client)
		result.ElapsedMillis = float64(time.Since(started).Microseconds()) / 1000
		latencies = append(latencies, result.ElapsedMillis)
		report.Cases = append(report.Cases, result)
	}
	report.Summary, report.Slices = summarize(fixture, report.Cases, latencies)
	return report, nil
}

func evaluateCase(ctx context.Context, item Case, client Client) CaseResult {
	result := CaseResult{
		ID:             item.ID,
		Group:          item.Group,
		Split:          item.Split,
		ExpectedStatus: item.Expectation.EvidenceStatus,
	}
	search, err := client.SearchRules(ctx, item.Input.MCPInput())
	if err != nil {
		result.Failures = append(result.Failures, "search_rules: "+err.Error())
		return result
	}
	result.ObservedStatus = string(search.Answerability.Status)
	result.Answerable = search.Answerable
	result.Answerability = search.Answerability
	result.Mode = search.Mode
	result.StatusCorrect = result.ObservedStatus == result.ExpectedStatus && search.Answerable == (result.ExpectedStatus == "supported")
	result.ClarificationProvided = strings.TrimSpace(search.Answerability.Clarification) != ""
	result.FilterLeaks = countFilterLeaks(item.Input, search.Results)
	result.DocumentRank = firstDocumentRank(item.Expectation, search.Results)
	result.EvidenceEligible, result.EvidenceManualReview = evidenceEligibility(item.Expectation)
	if item.Expectation.QueryExpansion != nil {
		passed := expansionMatches(*item.Expectation.QueryExpansion, search)
		result.ExpansionPassed = &passed
	}

	zero := 0
	evidenceOrdinal := 0
	for rank, observed := range search.Results {
		observedResult := ObservedResult{
			ID:                    observed.ID,
			Rank:                  rank + 1,
			Score:                 observed.Score,
			BM25Score:             observed.BM25Score,
			VectorScore:           observed.VectorScore,
			MatchedChunkID:        observed.MatchedChunkID,
			ArticleID:             observed.ArticleID,
			CanonicalKoreanSource: observed.CanonicalKoreanSource,
		}
		for _, attachment := range observed.AttachmentMatches {
			observedResult.AttachmentIDs = appendUnique(observedResult.AttachmentIDs, attachment.ID)
		}
		evidence := observed.EvidenceMatches
		if len(evidence) == 0 && observed.MatchedChunkID != "" {
			evidence = []mcpserver.EvidenceMatchDTO{{
				ChunkID: observed.MatchedChunkID, ChunkIndex: observed.MatchedChunkIndex,
				Source: observed.MatchedSource, ArticleID: observed.ArticleID, HeadingPath: observed.HeadingPath,
				Score: observed.Score, BM25Score: observed.BM25Score, VectorScore: observed.VectorScore,
			}}
		}
		for _, match := range evidence {
			evidenceOrdinal++
			observedResult.Evidence = append(observedResult.Evidence, ObservedEvidence{
				ChunkID: match.ChunkID, Source: match.Source, AttachmentID: match.AttachmentID,
				ArticleID: match.ArticleID, HeadingPath: append([]string(nil), match.HeadingPath...),
				Score: match.Score, BM25Score: match.BM25Score, VectorScore: match.VectorScore,
			})
			contextOutput, contextErr := client.GetContext(ctx, mcpserver.GetContextInput{
				ChunkID: match.ChunkID, BeforeChunks: &zero, AfterChunks: &zero, MaxChars: 10000,
			})
			check := ContextCheck{ChunkID: match.ChunkID, ExpectedDocument: observed.ID}
			if contextErr != nil {
				check.Error = contextErr.Error()
				result.ContextChecks = append(result.ContextChecks, check)
				continue
			}
			check.ObservedDocument = contextOutput.Document.ID
			check.DocumentMatches = contextOutput.Document.ID == observed.ID
			for _, chunk := range contextOutput.Chunks {
				if chunk.ID == match.ChunkID {
					check.ChunkPresent = true
					break
				}
			}
			check.TargetMatches = contextMatchesExpectation(item.Expectation, match, contextOutput)
			if check.TargetMatches && result.EvidenceRank == 0 {
				result.EvidenceRank = evidenceOrdinal
			}
			result.ContextChecks = append(result.ContextChecks, check)
		}
		result.Results = append(result.Results, observedResult)
	}
	result.AutomaticPassed = automaticCasePass(item, result)
	if !result.StatusCorrect {
		result.Failures = append(result.Failures, fmt.Sprintf("status=%s want=%s", result.ObservedStatus, result.ExpectedStatus))
	}
	if item.Expectation.EvidenceStatus == "supported" && result.DocumentRank == 0 {
		result.Failures = append(result.Failures, "expected document absent from results")
	}
	if result.EvidenceEligible && !result.EvidenceManualReview && result.EvidenceRank == 0 {
		result.Failures = append(result.Failures, "expected automatic evidence absent from contexts")
	}
	if result.FilterLeaks > 0 {
		result.Failures = append(result.Failures, fmt.Sprintf("filter leaks=%d", result.FilterLeaks))
	}
	if result.ExpansionPassed != nil && !*result.ExpansionPassed {
		result.Failures = append(result.Failures, "query expansion expectation failed")
	}
	return result
}

func firstDocumentRank(expectation Expectation, results []mcpserver.SearchResultDTO) int {
	for rank, result := range results {
		for _, target := range expectation.Targets {
			if result.ID == target.DocumentID {
				return rank + 1
			}
		}
	}
	return 0
}

func evidenceEligibility(expectation Expectation) (eligible, manual bool) {
	for _, target := range expectation.Targets {
		if target.ArticleID != "" || target.AttachmentID != "" {
			eligible = true
		}
		if target.Evidence != nil && target.Evidence.ManualReview {
			manual = true
		}
	}
	return eligible, manual
}

func contextMatchesExpectation(expectation Expectation, observed mcpserver.EvidenceMatchDTO, contextOutput mcpserver.ContextOutput) bool {
	matched := 0
	for _, target := range expectation.Targets {
		if target.DocumentID != contextOutput.Document.ID {
			continue
		}
		if target.ArticleID != "" && target.ArticleID != observed.ArticleID {
			continue
		}
		if target.AttachmentID != "" && target.AttachmentID != observed.AttachmentID {
			continue
		}
		if target.Evidence != nil {
			if !evidenceTextMatches(*target.Evidence, contextOutput.Content) ||
				!claimRelationEvidenceMatches(expectation.ClaimRelation, *target.Evidence, contextOutput.Content) {
				continue
			}
		}
		matched++
	}
	switch expectation.TargetPolicy {
	case "all":
		return matched == len(expectation.Targets) && matched > 0
	case "at_least":
		return matched >= expectation.AtLeast
	default:
		return matched > 0
	}
}

func claimRelationEvidenceMatches(relation string, expectation EvidenceExpectation, text string) bool {
	if relation != "contradicts" {
		return true
	}
	text = strings.ToLower(text)
	for _, required := range expectation.RelationMustContainAny {
		if strings.Contains(text, strings.ToLower(required)) {
			return true
		}
	}
	return false
}

func evidenceTextMatches(expectation EvidenceExpectation, text string) bool {
	text = strings.ToLower(text)
	for _, required := range expectation.MustContainAll {
		if !strings.Contains(text, strings.ToLower(required)) {
			return false
		}
	}
	if len(expectation.MustContainAny) > 0 {
		found := false
		for _, required := range expectation.MustContainAny {
			if strings.Contains(text, strings.ToLower(required)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, forbidden := range expectation.MustNotContain {
		if strings.Contains(text, strings.ToLower(forbidden)) {
			return false
		}
	}
	return true
}

func countFilterLeaks(input CaseInput, results []mcpserver.SearchResultDTO) int {
	leaks := 0
	for _, result := range results {
		if input.Language != "" && result.Language != input.Language {
			leaks++
			continue
		}
		if input.DocumentType != "" && string(result.DocumentType) != input.DocumentType {
			leaks++
			continue
		}
		if input.Category != "" && result.Category != input.Category {
			leaks++
			continue
		}
		if input.EffectiveFrom != "" && (result.EffectiveDate == "" || result.EffectiveDate < input.EffectiveFrom) {
			leaks++
			continue
		}
		if input.EffectiveTo != "" && (result.EffectiveDate == "" || result.EffectiveDate > input.EffectiveTo) {
			leaks++
		}
	}
	return leaks
}

func expansionMatches(expectation QueryExpansionExpectation, search mcpserver.SearchRulesOutput) bool {
	if search.QueryExpansion == nil {
		return false
	}
	ids := map[string]struct{}{}
	for _, applied := range search.QueryExpansion.AppliedTerms {
		ids[applied.ID] = struct{}{}
	}
	for _, required := range expectation.RequiredEntryIDs {
		if _, ok := ids[required]; !ok {
			return false
		}
	}
	expanded := normalizeExpansionExpectation(search.QueryExpansion.ExpandedQuery)
	for _, required := range expectation.RequiredTerms {
		if !strings.Contains(expanded, normalizeExpansionExpectation(required)) {
			return false
		}
	}
	return true
}

func normalizeExpansionExpectation(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), ""))
}

func automaticCasePass(item Case, result CaseResult) bool {
	if !result.StatusCorrect || result.FilterLeaks != 0 {
		return false
	}
	if result.ExpansionPassed != nil && !*result.ExpansionPassed {
		return false
	}
	switch item.Expectation.EvidenceStatus {
	case "supported":
		if result.DocumentRank == 0 || result.DocumentRank > 5 {
			return false
		}
		if result.EvidenceEligible && !result.EvidenceManualReview && (result.EvidenceRank == 0 || result.EvidenceRank > 3) {
			return false
		}
	case "insufficient":
		if len(result.Results) != 0 {
			return false
		}
	case "ambiguous":
		if !result.ClarificationProvided {
			return false
		}
	}
	for _, check := range result.ContextChecks {
		if check.Error != "" || !check.ChunkPresent || !check.DocumentMatches {
			return false
		}
	}
	return true
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func summarize(fixture Fixture, cases []CaseResult, latencies []float64) (Summary, map[string]SliceMetrics) {
	summary := Summary{Cases: len(cases)}
	slices := map[string]SliceMetrics{}
	caseByID := make(map[string]Case, len(fixture.Cases))
	for _, item := range fixture.Cases {
		caseByID[item.ID] = item
		switch item.Expectation.EvidenceStatus {
		case "supported":
			summary.SupportedCases++
		case "insufficient":
			summary.InsufficientCases++
		case "ambiguous":
			summary.AmbiguousCases++
		}
	}
	var reciprocalRank float64
	for _, result := range cases {
		item := caseByID[result.ID]
		slice := slices[result.Group]
		slice.Cases++
		if result.StatusCorrect {
			summary.StatusCorrect++
			slice.StatusCorrect++
		}
		if result.AutomaticPassed {
			summary.AutomaticPassed++
		}
		if result.EvidenceManualReview {
			summary.ManualReviewCases++
		}
		if item.Expectation.EvidenceStatus == "supported" && len(item.Expectation.Targets) > 0 {
			summary.DocumentEligible++
			slice.DocumentEligible++
			if result.DocumentRank == 1 {
				summary.DocumentHitAt1++
			}
			if result.DocumentRank > 0 && result.DocumentRank <= 5 {
				summary.DocumentHitAt5++
				slice.DocumentHitAt5++
				reciprocalRank += 1 / float64(result.DocumentRank)
			}
		}
		if result.EvidenceEligible && !result.EvidenceManualReview {
			summary.EvidenceEligible++
			slice.EvidenceEligible++
			if result.EvidenceRank == 1 {
				summary.EvidenceHitAt1++
				slice.EvidenceHitAt1++
			}
			if result.EvidenceRank > 0 && result.EvidenceRank <= 3 {
				summary.EvidenceRecallAt3++
				slice.EvidenceRecallAt3++
			}
		}
		if result.EvidenceEligible && result.EvidenceManualReview {
			summary.ManualEvidenceEligible++
			if result.EvidenceRank == 1 {
				summary.ManualEvidenceHitAt1++
			}
		}
		if item.Expectation.EvidenceStatus == "insufficient" {
			if result.ObservedStatus == "insufficient" && len(result.Results) == 0 {
				summary.InsufficientRefused++
			}
			if result.ObservedStatus == "supported" {
				summary.FalseSupported++
			}
		}
		if item.Expectation.EvidenceStatus == "ambiguous" && result.ObservedStatus == "ambiguous" && result.ClarificationProvided {
			summary.AmbiguousClarified++
		}
		for _, check := range result.ContextChecks {
			summary.ContextChecks++
			if check.Error == "" && check.ChunkPresent && check.DocumentMatches {
				summary.ContextConsistent++
			}
		}
		summary.FilterLeaks += result.FilterLeaks
		if result.ExpansionPassed != nil {
			summary.ExpansionChecks++
			if *result.ExpansionPassed {
				summary.ExpansionPassed++
			}
		}
		if strings.HasPrefix(result.Group, "english") && item.Expectation.EvidenceStatus == "supported" {
			summary.EnglishCanonicalChecks++
			if englishCanonicalPassed(item, result) {
				summary.EnglishCanonicalPassed++
			}
		}
		if hasHWPAttachment(item.Expectation) {
			summary.HWPAttachmentChecks++
			if result.DocumentRank > 0 && result.DocumentRank <= 5 && result.EvidenceRank > 0 {
				summary.HWPAttachmentPassed++
			}
		}
		if slice.EvidenceEligible > 0 {
			slice.EvidenceHitAt1Rate = ratio(slice.EvidenceHitAt1, slice.EvidenceEligible)
		}
		slices[result.Group] = slice
	}
	summary.DocumentHitAt5Rate = ratio(summary.DocumentHitAt5, summary.DocumentEligible)
	if summary.DocumentEligible > 0 {
		summary.MRRAt5 = reciprocalRank / float64(summary.DocumentEligible)
	}
	summary.EvidenceHitAt1Rate = ratio(summary.EvidenceHitAt1, summary.EvidenceEligible)
	summary.EvidenceRecallAt3Rate = ratio(summary.EvidenceRecallAt3, summary.EvidenceEligible)
	summary.ManualEvidenceHitAt1Rate = ratio(summary.ManualEvidenceHitAt1, summary.ManualEvidenceEligible)
	summary.StatusAccuracy = ratio(summary.StatusCorrect, summary.Cases)
	summary.InsufficientRefusalRate = ratio(summary.InsufficientRefused, summary.InsufficientCases)
	summary.FalseSupportedRate = ratio(summary.FalseSupported, summary.InsufficientCases)
	summary.AmbiguousClarificationRate = ratio(summary.AmbiguousClarified, summary.AmbiguousCases)
	summary.ContextConsistencyRate = ratio(summary.ContextConsistent, summary.ContextChecks)
	summary.P95LatencyMillis = percentile95(latencies)
	return summary, slices
}

func englishCanonicalPassed(item Case, result CaseResult) bool {
	for _, observed := range result.Results {
		for _, target := range item.Expectation.Targets {
			if observed.ID != target.DocumentID || observed.CanonicalKoreanSource == nil {
				continue
			}
			if strings.TrimSuffix(target.DocumentID, "-en") == observed.CanonicalKoreanSource.ID {
				return true
			}
		}
	}
	return false
}

func hasHWPAttachment(expectation Expectation) bool {
	for _, target := range expectation.Targets {
		if strings.HasSuffix(strings.ToLower(target.AttachmentID), "-hwp") {
			return true
		}
	}
	return false
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func percentile95(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	index := (95*len(ordered)+99)/100 - 1
	if index < 0 {
		index = 0
	}
	return ordered[index]
}
