package evaluation

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	mcpserver "github.com/chromato99/krx-rule-mcp/internal/mcp"
	"github.com/chromato99/krx-rule-mcp/internal/model"
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

func RunCasePrefix(ctx context.Context, fixture Fixture, prefix string, client Client, provenance Provenance) (Report, error) {
	if err := ValidateFixture(fixture); err != nil {
		return Report{}, err
	}
	selected := fixture
	selected.Cases = make([]Case, 0, len(fixture.Cases))
	for _, item := range fixture.Cases {
		if strings.HasPrefix(item.ID, prefix) {
			selected.Cases = append(selected.Cases, item)
		}
	}
	if len(selected.Cases) == 0 {
		return Report{}, fmt.Errorf("fixture case prefix %q has no cases", prefix)
	}
	return runFixture(ctx, selected, client, provenance)
}

func runFixture(ctx context.Context, fixture Fixture, client Client, provenance Provenance) (Report, error) {
	provenance.EvaluatorVersion = EvaluatorVersion
	provenance.FixtureVersion = fixture.FixtureVersion
	provenance.CaseSetSHA256 = FixtureContractHash(fixture)
	provenance.EvaluationUse = fixture.Policies.EvaluationUse
	if provenance.EvaluationUse == "" {
		provenance.EvaluationUse = "development"
	}
	report := Report{
		SchemaVersion: 2,
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
	report.SplitSummaries = summarizeSplits(fixture, report.Cases)
	report.LanguageSummaries = summarizeLanguages(fixture, report.Cases)
	report.LanguageSplitSummaries = summarizeLanguageSplits(fixture, report.Cases)
	report.FailureStageCounts = summarizeFailureStages(report.Cases)
	return report, nil
}

func summarizeFailureStages(cases []CaseResult) map[string]int {
	counts := map[string]int{}
	for _, result := range cases {
		for _, stage := range result.FailureStages {
			counts[stage]++
		}
	}
	return counts
}

func evaluateCase(ctx context.Context, item Case, client Client) CaseResult {
	evidenceEligible, evidenceManualReview := evidenceEligibility(item.Expectation)
	if item.Expectation.EvidenceStatus != "supported" {
		evidenceEligible, evidenceManualReview = false, false
	}
	result := CaseResult{
		ID:                   item.ID,
		Group:                item.Group,
		Split:                item.Split,
		Language:             item.Input.Language,
		ClaimRelation:        item.Expectation.ClaimRelation,
		ExpectedStatus:       item.Expectation.EvidenceStatus,
		EvidenceEligible:     evidenceEligible,
		EvidenceManualReview: evidenceManualReview,
	}
	searchStarted := time.Now()
	search, err := client.SearchRules(ctx, item.Input.MCPInput())
	result.SearchElapsedMillis = float64(time.Since(searchStarted).Microseconds()) / 1000
	if err != nil {
		result.Failures = append(result.Failures, "search_rules: "+err.Error())
		result.FailureStages = []string{"search"}
		return result
	}
	result.Retrieval = search.Retrieval
	displayLimit := item.Input.Limit
	if displayLimit == 0 {
		displayLimit = 10
	}
	expectedRetrievalStatus := mcpserver.RetrievalNoCandidates
	if len(search.Results) > 0 {
		expectedRetrievalStatus = mcpserver.RetrievalCandidatesFound
	}
	result.RetrievalContractValid = search.ContractVersion == mcpserver.SearchContractVersion &&
		search.Retrieval.Status == expectedRetrievalStatus && search.Retrieval.ReturnedResults == len(search.Results) && len(search.Results) <= displayLimit
	result.ReturnedEvidenceValid = true
	returnedIDs := map[string]bool{}
	var bundleContexts []returnedEvidenceContext
	result.Mode = search.Mode
	result.RerankerElapsedMillis = search.RerankerElapsedMillis
	result.RerankerCandidateCount = search.RerankerCandidateCount
	result.RerankerAdopted = search.RerankerAdopted
	result.CandidateBM25Rank = candidatePolicyRank(item.Expectation, search.Candidates, func(candidate searchindex.ChunkCandidate) int { return candidate.BM25Rank })
	result.CandidateVectorRank = candidatePolicyRank(item.Expectation, search.Candidates, func(candidate searchindex.ChunkCandidate) int { return candidate.VectorRank })
	result.CandidateFusedRank = candidatePolicyRank(item.Expectation, search.Candidates, func(candidate searchindex.ChunkCandidate) int { return candidate.BaselineRank })
	result.CandidateFinalRank = candidatePolicyRank(item.Expectation, search.Candidates, func(candidate searchindex.ChunkCandidate) int { return candidate.FinalRank })
	ownerExpectation := item.Expectation
	ownerExpectation.Targets = append([]Target(nil), item.Expectation.Targets...)
	for i := range ownerExpectation.Targets {
		ownerExpectation.Targets[i].Evidence = nil
	}
	result.CandidateOwnerRank = candidatePolicyRank(ownerExpectation, search.Candidates, func(candidate searchindex.ChunkCandidate) int { return candidate.BaselineRank })
	result.CandidateBundleMatched = candidateBundleMatches(item.Expectation, search.Candidates)
	result.CandidateTrace = buildCandidateTrace(item.Expectation, search.Candidates, 8)
	result.RerankerPoolIncluded = candidatePolicyRank(item.Expectation, search.Candidates, func(candidate searchindex.ChunkCandidate) int { return candidate.RerankerRank }) > 0
	result.FilterLeaks = countFilterLeaks(item.Input, search.Results)
	result.DocumentRank = firstDocumentRank(item.Expectation, search.Results)
	if item.Expectation.QueryExpansion != nil {
		passed := expansionMatches(*item.Expectation.QueryExpansion, search)
		result.ExpansionPassed = &passed
	}

	zero := 0
	matchedEvidenceTargets := map[int]struct{}{}
	targetDepths := map[int]int{}
	for rank, observed := range search.Results {
		observedResult := ObservedResult{
			ID:                    observed.ID,
			Rank:                  rank + 1,
			Score:                 observed.Score,
			BM25Score:             observed.BM25Score,
			VectorScore:           observed.VectorScore,
			CanonicalKoreanSource: observed.CanonicalKoreanSource,
		}
		for _, attachment := range observed.AttachmentMatches {
			observedResult.AttachmentIDs = appendUnique(observedResult.AttachmentIDs, attachment.ID)
		}
		for _, match := range observed.EvidenceMatches {
			if strings.TrimSpace(match.ChunkID) == "" || returnedIDs[match.ChunkID] {
				result.ReturnedEvidenceValid = false
			}
			returnedIDs[match.ChunkID] = true
			observedResult.Evidence = append(observedResult.Evidence, ObservedEvidence{
				ChunkID: match.ChunkID, Source: match.Source, AttachmentID: match.AttachmentID,
				ArticleID: match.ArticleID, HeadingPath: append([]string(nil), match.HeadingPath...),
				Score: match.Score, BM25Score: match.BM25Score, VectorScore: match.VectorScore,
				RerankerScore: match.RerankerScore, RerankerRank: match.RerankerRank,
			})
			contextOutput, contextErr := client.GetContext(ctx, mcpserver.GetContextInput{
				ChunkID: match.ChunkID, BeforeChunks: &zero, AfterChunks: &zero, MaxChars: 50000,
			})
			check := ContextCheck{ChunkID: match.ChunkID, ExpectedDocument: observed.ID}
			if contextErr != nil {
				result.ReturnedEvidenceValid = false
				check.Error = contextErr.Error()
				result.ContextChecks = append(result.ContextChecks, check)
				continue
			}
			check.ObservedDocument = contextOutput.Document.ID
			check.DocumentMatches = contextOutput.Document.ID == observed.ID
			for _, chunk := range contextOutput.Chunks {
				if chunk.ID == match.ChunkID {
					check.ChunkPresent = true
					check.OwnerMatches = chunk.DocumentID == observed.ID && chunk.ArticleID == match.ArticleID && chunk.AttachmentID == match.AttachmentID && chunk.Source == match.Source
					break
				}
			}
			check.Truncated = contextOutput.Truncated
			if !check.ChunkPresent || !check.DocumentMatches || !check.OwnerMatches || check.Truncated {
				result.ReturnedEvidenceValid = false
			} else if rank < 5 {
				bundleContexts = append(bundleContexts, returnedEvidenceContext{match, contextOutput})
			}
			matchedTargets := matchingTargetIndexes(item.Expectation, match, contextOutput)
			check.TargetMatches = len(matchedTargets) > 0
			for _, targetIndex := range matchedTargets {
				matchedEvidenceTargets[targetIndex] = struct{}{}
				depth := len(observedResult.Evidence)
				if targetDepths[targetIndex] == 0 || depth < targetDepths[targetIndex] {
					targetDepths[targetIndex] = depth
				}
			}
			if result.EvidenceRank == 0 && targetPolicySatisfied(item.Expectation, matchedEvidenceTargets) {
				result.EvidenceRank = evidencePolicyRank(item.Expectation, targetDepths)
			}
			result.ContextChecks = append(result.ContextChecks, check)
		}
		result.Results = append(result.Results, observedResult)
	}
	result.EvidenceBundleAt5Matched = result.EvidenceEligible && !result.EvidenceManualReview &&
		result.ReturnedEvidenceValid && returnedEvidenceMatches(item.Expectation, bundleContexts)
	result.AutomaticPassed = automaticCasePass(item, result)
	if !result.RetrievalContractValid {
		result.Failures = append(result.Failures, "retrieval status, count, version or result limit is inconsistent")
	}
	if !result.ReturnedEvidenceValid {
		result.Failures = append(result.Failures, "returned evidence IDs are missing, duplicated, truncated, or inconsistent with their source contexts")
	}
	if result.EvidenceEligible && !result.EvidenceManualReview && !result.EvidenceBundleAt5Matched {
		result.Failures = append(result.Failures, "top-5 returned evidence bundle does not satisfy expected targets")
	}
	if item.Expectation.EvidenceStatus == "supported" && result.DocumentRank == 0 {
		result.Failures = append(result.Failures, "expected document absent from results")
	} else if item.Expectation.EvidenceStatus == "supported" && result.DocumentRank > 5 {
		result.Failures = append(result.Failures, fmt.Sprintf("expected document policy satisfied at rank=%d, exceeds 5", result.DocumentRank))
	}
	if result.EvidenceEligible && !result.EvidenceManualReview {
		switch {
		case result.EvidenceRank == 0:
			result.Failures = append(result.Failures, "expected automatic evidence absent from contexts")
		case result.EvidenceRank > 3:
			result.Failures = append(result.Failures, fmt.Sprintf("expected automatic evidence policy satisfied at rank=%d, exceeds 3", result.EvidenceRank))
		}
	}
	if result.FilterLeaks > 0 {
		result.Failures = append(result.Failures, fmt.Sprintf("filter leaks=%d", result.FilterLeaks))
	}
	if result.ExpansionPassed != nil && !*result.ExpansionPassed {
		result.Failures = append(result.Failures, "query expansion expectation failed")
	}
	result.FailureStages = classifyFailureStages(item, result)
	return result
}

func buildCandidateTrace(expectation Expectation, candidates []searchindex.ChunkCandidate, limit int) []CandidateTrace {
	if limit <= 0 || len(candidates) == 0 {
		return nil
	}
	ranked := append([]searchindex.ChunkCandidate(nil), candidates...)
	sort.SliceStable(ranked, func(i, j int) bool {
		left, right := ranked[i].BaselineRank, ranked[j].BaselineRank
		if left == 0 {
			left = int(^uint(0) >> 1)
		}
		if right == 0 {
			right = int(^uint(0) >> 1)
		}
		if left == right {
			return ranked[i].ChunkID < ranked[j].ChunkID
		}
		return left < right
	})
	selected := ranked
	if len(ranked) > limit {
		selected = append([]searchindex.ChunkCandidate(nil), ranked[:limit]...)
		hasTarget := false
		for _, candidate := range selected {
			if len(matchingCandidateTargetIndexes(expectation, candidate)) > 0 {
				hasTarget = true
				break
			}
		}
		if !hasTarget {
			for _, candidate := range ranked[limit:] {
				if len(matchingCandidateTargetIndexes(expectation, candidate)) > 0 {
					selected = append(selected, candidate)
					break
				}
			}
		}
	}
	trace := make([]CandidateTrace, 0, len(selected))
	for _, candidate := range selected {
		trace = append(trace, CandidateTrace{
			ChunkID: candidate.ChunkID, DocumentID: candidate.DocumentID, Source: candidate.Source,
			AttachmentID: candidate.AttachmentID, ArticleID: candidate.ArticleID,
			HeadingPath: append([]string(nil), candidate.HeadingPath...), TextPreview: candidateTextPreview(candidate.Text, 240),
			TargetMatch: len(matchingCandidateTargetIndexes(expectation, candidate)) > 0,
			BM25Rank:    candidate.BM25Rank, VectorRank: candidate.VectorRank, FusedRank: candidate.BaselineRank,
			FinalRank: candidate.FinalRank, LexicalCoverage: candidate.LexicalCoverage,
		})
	}
	return trace
}

func candidateTextPreview(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}

func classifyFailureStages(item Case, result CaseResult) []string {
	var stages []string
	add := func(stage string) {
		for _, existing := range stages {
			if existing == stage {
				return
			}
		}
		stages = append(stages, stage)
	}
	if result.FilterLeaks > 0 {
		add("filtering")
	}
	for _, check := range result.ContextChecks {
		if check.Error != "" || !check.ChunkPresent || !check.DocumentMatches {
			add("context-contract")
			break
		}
	}
	if item.Expectation.EvidenceStatus == "supported" {
		if result.DocumentRank == 0 || result.DocumentRank > 5 {
			add("document-ranking")
		}
		if result.EvidenceEligible && !result.EvidenceManualReview {
			switch {
			case result.CandidateOwnerRank == 0:
				add("candidate-generation")
			case !result.CandidateBundleMatched:
				add("candidate-evidence-gap")
			case result.EvidenceRank == 0 || result.EvidenceRank > 3:
				add("evidence-selection")
			case result.EvidenceRank > 1:
				add("top1-ranking")
			}
		}
	}
	if !result.RetrievalContractValid {
		add("retrieval-contract")
	}
	if !result.ReturnedEvidenceValid {
		add("context-contract")
	}
	if result.EvidenceEligible && !result.EvidenceManualReview && !result.EvidenceBundleAt5Matched {
		add("returned-evidence")
	}
	if result.ExpansionPassed != nil && !*result.ExpansionPassed {
		add("query-expansion")
	}
	return stages
}

func candidatePolicyRank(expectation Expectation, candidates []searchindex.ChunkCandidate, rankOf func(searchindex.ChunkCandidate) int) int {
	type rankedCandidate struct {
		candidate searchindex.ChunkCandidate
		rank      int
	}
	ranked := make([]rankedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		rank := rankOf(candidate)
		if rank > 0 {
			ranked = append(ranked, rankedCandidate{candidate: candidate, rank: rank})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].rank < ranked[j].rank })
	matched := map[int]struct{}{}
	for _, item := range ranked {
		for _, targetIndex := range matchingCandidateTargetIndexes(expectation, item.candidate) {
			matched[targetIndex] = struct{}{}
		}
		if targetPolicySatisfied(expectation, matched) {
			return item.rank
		}
	}
	return 0
}

func matchingCandidateTargetIndexes(expectation Expectation, candidate searchindex.ChunkCandidate) []int {
	var matched []int
	for targetIndex, target := range expectation.Targets {
		if target.DocumentID != candidate.DocumentID {
			continue
		}
		if target.ArticleID != "" && target.ArticleID != candidate.ArticleID {
			continue
		}
		if target.AttachmentID != "" && target.AttachmentID != candidate.AttachmentID {
			continue
		}
		if target.Evidence != nil {
			text := strings.Join(candidate.HeadingPath, " ") + " " + candidate.AttachmentTitle + " " + candidate.Text
			if !evidenceTextMatches(*target.Evidence, text) ||
				!claimRelationEvidenceMatches(expectation.ClaimRelation, *target.Evidence, text) {
				continue
			}
		}
		matched = append(matched, targetIndex)
	}
	return matched
}

// Diagnostics distinguish a missing source owner from evidence split across
// several candidate chunks. Neither diagnostic counts as an answered case.
func candidateBundleMatches(expectation Expectation, candidates []searchindex.ChunkCandidate) bool {
	matched := map[int]struct{}{}
	for index, target := range expectation.Targets {
		var parts []string
		for _, candidate := range candidates {
			if candidate.DocumentID != target.DocumentID || (target.ArticleID != "" && target.ArticleID != candidate.ArticleID) ||
				(target.AttachmentID != "" && target.AttachmentID != candidate.AttachmentID) {
				continue
			}
			parts = append(parts, strings.Join(candidate.HeadingPath, " ")+" "+candidate.Text)
		}
		if len(parts) == 0 {
			continue
		}
		text := strings.Join(parts, "\n\n")
		if target.Evidence != nil && (!evidenceTextMatches(*target.Evidence, text) || !claimRelationEvidenceMatches(expectation.ClaimRelation, *target.Evidence, text)) {
			continue
		}
		matched[index] = struct{}{}
	}
	return targetPolicySatisfied(expectation, matched)
}

func firstDocumentRank(expectation Expectation, results []mcpserver.SearchResultDTO) int {
	matchedTargets := map[int]struct{}{}
	for rank, result := range results {
		for targetIndex, target := range expectation.Targets {
			if result.ID == target.DocumentID {
				matchedTargets[targetIndex] = struct{}{}
			}
		}
		if targetPolicySatisfied(expectation, matchedTargets) {
			return rank + 1
		}
	}
	return 0
}

func evidenceEligibility(expectation Expectation) (eligible, manual bool) {
	for _, target := range expectation.Targets {
		if target.ArticleID != "" || target.AttachmentID != "" || target.Evidence != nil {
			eligible = true
		}
		if target.Evidence != nil && target.Evidence.ManualReview {
			manual = true
		}
	}
	return eligible, manual
}

func matchingTargetIndexes(expectation Expectation, observed mcpserver.EvidenceMatchDTO, contextOutput mcpserver.ContextOutput) []int {
	var matched []int
	for targetIndex, target := range expectation.Targets {
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
		matched = append(matched, targetIndex)
	}
	return matched
}

func contextMatchesExpectation(expectation Expectation, observed mcpserver.EvidenceMatchDTO, contextOutput mcpserver.ContextOutput) bool {
	matched := map[int]struct{}{}
	for _, targetIndex := range matchingTargetIndexes(expectation, observed, contextOutput) {
		matched[targetIndex] = struct{}{}
	}
	return targetPolicySatisfied(expectation, matched)
}

type returnedEvidenceContext struct {
	match   mcpserver.EvidenceMatchDTO
	context mcpserver.ContextOutput
}

// Combine returned chunks only within the same labelled target. The caller
// can inspect fragments together, but another article or attachment must not
// supply a missing condition. The input is bounded to the first five results.
func returnedEvidenceMatches(expectation Expectation, contexts []returnedEvidenceContext) bool {
	matched := map[int]struct{}{}
	for targetIndex, target := range expectation.Targets {
		var parts []string
		seen := map[string]bool{}
		for _, item := range contexts {
			if item.context.Document.ID != target.DocumentID ||
				(target.ArticleID != "" && item.match.ArticleID != target.ArticleID) ||
				(target.AttachmentID != "" && item.match.AttachmentID != target.AttachmentID) || seen[item.match.ChunkID] {
				continue
			}
			seen[item.match.ChunkID] = true
			parts = append(parts, item.context.Content)
		}
		if len(parts) == 0 {
			continue
		}
		text := strings.Join(parts, "\n\n")
		if target.Evidence != nil && (!evidenceTextMatches(*target.Evidence, text) ||
			!claimRelationEvidenceMatches(expectation.ClaimRelation, *target.Evidence, text)) {
			continue
		}
		matched[targetIndex] = struct{}{}
	}
	return targetPolicySatisfied(expectation, matched)
}

func targetPolicySatisfied(expectation Expectation, matched map[int]struct{}) bool {
	switch expectation.TargetPolicy {
	case "all":
		return len(expectation.Targets) > 0 && len(matched) == len(expectation.Targets)
	case "at_least":
		return expectation.AtLeast > 0 && len(matched) >= expectation.AtLeast
	default:
		return len(matched) > 0
	}
}

// evidencePolicyRank measures the evidence depth within each matching document.
// DocumentRank separately captures how many document results a client must inspect.
func evidencePolicyRank(expectation Expectation, depths map[int]int) int {
	if !targetPolicySatisfied(expectation, indexSet(depths)) {
		return 0
	}
	if expectation.TargetPolicy == "any" {
		best := 0
		for _, depth := range depths {
			if depth > 0 && (best == 0 || depth < best) {
				best = depth
			}
		}
		return best
	}
	values := make([]int, 0, len(depths))
	for _, depth := range depths {
		if depth > 0 {
			values = append(values, depth)
		}
	}
	sort.Ints(values)
	if expectation.TargetPolicy == "at_least" {
		return values[expectation.AtLeast-1]
	}
	return values[len(values)-1]
}

func indexSet(values map[int]int) map[int]struct{} {
	out := make(map[int]struct{}, len(values))
	for index, value := range values {
		if value > 0 {
			out[index] = struct{}{}
		}
	}
	return out
}

func claimRelationEvidenceMatches(relation string, expectation EvidenceExpectation, text string) bool {
	if relation != "contradicts" {
		return true
	}
	text = normalizeEvidenceText(text)
	for _, required := range expectation.RelationMustContainAny {
		if strings.Contains(text, normalizeEvidenceText(required)) {
			return true
		}
	}
	return false
}

func evidenceTextMatches(expectation EvidenceExpectation, text string) bool {
	text = normalizeEvidenceText(text)
	for _, required := range expectation.MustContainAll {
		if !strings.Contains(text, normalizeEvidenceText(required)) {
			return false
		}
	}
	if len(expectation.MustContainAny) > 0 {
		found := false
		for _, required := range expectation.MustContainAny {
			if strings.Contains(text, normalizeEvidenceText(required)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, forbidden := range expectation.MustNotContain {
		if strings.Contains(text, normalizeEvidenceText(forbidden)) {
			return false
		}
	}
	return true
}

var (
	formulaTimesPattern = regexp.MustCompile(`(?i)(\\times|\btimes\b)`)
	formulaGEPattern    = regexp.MustCompile(`(?i)(\\geq?|\bgeq?\b)`)
)

func normalizeEvidenceText(value string) string {
	value = strings.ToLower(value)
	value = strings.NewReplacer(
		"`", "",
	).Replace(value)
	value = formulaTimesPattern.ReplaceAllString(value, "×")
	value = formulaGEPattern.ReplaceAllString(value, "≥")
	return strings.Join(strings.Fields(value), " ")
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

// This pass describes the retrieval contract only. Negative/ambiguous fixtures
// may return candidates; their answer/refusal/clarification needs a caller LLM.
func automaticCasePass(item Case, result CaseResult) bool {
	if !result.RetrievalContractValid || !result.ReturnedEvidenceValid || result.FilterLeaks != 0 {
		return false
	}
	if result.ExpansionPassed != nil && !*result.ExpansionPassed {
		return false
	}
	if item.Expectation.EvidenceStatus == "supported" {
		return result.DocumentRank > 0 && result.DocumentRank <= 5 &&
			(!result.EvidenceEligible || result.EvidenceManualReview || result.EvidenceBundleAt5Matched)
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
	searchLatencies := make([]float64, 0, len(cases))
	rerankerLatencies := make([]float64, 0, len(cases))
	for _, result := range cases {
		searchLatencies = append(searchLatencies, result.SearchElapsedMillis)
		if result.RerankerElapsedMillis > 0 {
			rerankerLatencies = append(rerankerLatencies, result.RerankerElapsedMillis)
		}
		if result.RerankerCandidateCount > 0 {
			summary.RerankerAttempted++
			if result.RerankerAdopted {
				summary.RerankerAdopted++
			}
		}
		item := caseByID[result.ID]
		slice := slices[result.Group]
		slice.Cases++
		if !result.RetrievalContractValid {
			summary.RetrievalContractFailures++
		}
		if result.ReturnedEvidenceValid {
			summary.ReturnedEvidenceValidCases++
		}
		if result.EvidenceBundleAt5Matched {
			summary.EvidenceBundleHitAt5++
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
			summary.CandidateEvidenceEligible++
			slice.EvidenceEligible++
			if result.CandidateFusedRank > 0 && result.CandidateFusedRank <= 64 {
				summary.CandidateEvidenceRecall64++
			}
			if result.RerankerCandidateCount > 0 {
				summary.RerankerPoolEligible++
				if result.RerankerPoolIncluded {
					summary.RerankerPoolHit++
				}
			}
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
		if item.Expectation.EvidenceStatus == "insufficient" && len(result.Results) > 0 {
			summary.NegativeCasesWithCandidates++
		}
		if item.Expectation.EvidenceStatus == "ambiguous" && len(result.Results) > 0 {
			summary.AmbiguousCasesWithCandidates++
		}
		for _, check := range result.ContextChecks {
			summary.ContextChecks++
			if check.Error == "" && check.ChunkPresent && check.DocumentMatches && check.OwnerMatches && !check.Truncated {
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
		if item.Input.Language == model.LanguageEnglish && item.Expectation.EvidenceStatus == "supported" {
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
	summary.CandidateEvidenceRecall64Rate = ratio(summary.CandidateEvidenceRecall64, summary.CandidateEvidenceEligible)
	summary.RerankerPoolHitRate = ratio(summary.RerankerPoolHit, summary.RerankerPoolEligible)
	summary.ManualEvidenceHitAt1Rate = ratio(summary.ManualEvidenceHitAt1, summary.ManualEvidenceEligible)
	summary.EvidenceBundleHitAt5Rate = ratio(summary.EvidenceBundleHitAt5, summary.EvidenceEligible)
	summary.ContextConsistencyRate = ratio(summary.ContextConsistent, summary.ContextChecks)
	summary.P95SearchLatencyMillis = percentile95(searchLatencies)
	summary.P95RerankerLatencyMillis = percentile95(rerankerLatencies)
	summary.P95LatencyMillis = percentile95(latencies)
	return summary, slices
}

func summarizeSplits(fixture Fixture, cases []CaseResult) map[string]Summary {
	caseByID := make(map[string]Case, len(fixture.Cases))
	for _, item := range fixture.Cases {
		caseByID[item.ID] = item
	}
	summaries := map[string]Summary{}
	for _, split := range []string{"regression", "development", "validation", "holdout"} {
		selectedFixture := fixture
		selectedFixture.Cases = nil
		var selectedCases []CaseResult
		var latencies []float64
		for _, result := range cases {
			item, ok := caseByID[result.ID]
			if !ok || item.Split != split {
				continue
			}
			selectedFixture.Cases = append(selectedFixture.Cases, item)
			selectedCases = append(selectedCases, result)
			latencies = append(latencies, result.ElapsedMillis)
		}
		if len(selectedCases) == 0 {
			continue
		}
		summary, _ := summarize(selectedFixture, selectedCases, latencies)
		summaries[split] = summary
	}
	return summaries
}

func summarizeLanguages(fixture Fixture, cases []CaseResult) map[string]Summary {
	caseByID := make(map[string]Case, len(fixture.Cases))
	languages := map[string]struct{}{}
	for _, item := range fixture.Cases {
		caseByID[item.ID] = item
		languages[summaryLanguage(item.Input.Language)] = struct{}{}
	}
	summaries := make(map[string]Summary, len(languages))
	for language := range languages {
		selectedFixture := fixture
		selectedFixture.Cases = nil
		var selectedCases []CaseResult
		var latencies []float64
		for _, result := range cases {
			item, ok := caseByID[result.ID]
			if !ok || summaryLanguage(item.Input.Language) != language {
				continue
			}
			selectedFixture.Cases = append(selectedFixture.Cases, item)
			selectedCases = append(selectedCases, result)
			latencies = append(latencies, result.ElapsedMillis)
		}
		if len(selectedCases) == 0 {
			continue
		}
		summary, _ := summarize(selectedFixture, selectedCases, latencies)
		summaries[language] = summary
	}
	return summaries
}

func summarizeLanguageSplits(fixture Fixture, cases []CaseResult) map[string]map[string]Summary {
	caseByID := make(map[string]Case, len(fixture.Cases))
	languages := map[string]struct{}{}
	for _, item := range fixture.Cases {
		caseByID[item.ID] = item
		languages[summaryLanguage(item.Input.Language)] = struct{}{}
	}
	summaries := make(map[string]map[string]Summary, len(languages))
	for language := range languages {
		bySplit := map[string]Summary{}
		for _, split := range []string{"regression", "development", "validation", "holdout"} {
			selectedFixture := fixture
			selectedFixture.Cases = nil
			var selectedCases []CaseResult
			var latencies []float64
			for _, result := range cases {
				item, ok := caseByID[result.ID]
				if !ok || summaryLanguage(item.Input.Language) != language || item.Split != split {
					continue
				}
				selectedFixture.Cases = append(selectedFixture.Cases, item)
				selectedCases = append(selectedCases, result)
				latencies = append(latencies, result.ElapsedMillis)
			}
			if len(selectedCases) == 0 {
				continue
			}
			summary, _ := summarize(selectedFixture, selectedCases, latencies)
			bySplit[split] = summary
		}
		if len(bySplit) > 0 {
			summaries[language] = bySplit
		}
	}
	return summaries
}

func summaryLanguage(language string) string {
	if language == "" {
		return "unspecified"
	}
	return language
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
