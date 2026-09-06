package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	mcpserver "github.com/chromato99/krx-rule-mcp/internal/mcp"
	"strings"
	"testing"
)

type evidenceClient struct {
	search   mcpserver.SearchRulesOutput
	contexts map[string]mcpserver.ContextOutput
}

func (c evidenceClient) SearchRules(context.Context, mcpserver.SearchRulesInput) (mcpserver.SearchRulesOutput, error) {
	return c.search, nil
}
func (c evidenceClient) GetContext(_ context.Context, in mcpserver.GetContextInput) (mcpserver.ContextOutput, error) {
	out, ok := c.contexts[in.ChunkID]
	if !ok {
		return out, fmt.Errorf("unknown chunk %q", in.ChunkID)
	}
	return out, nil
}
func returnedClient(texts ...string) evidenceClient {
	c := evidenceClient{search: mcpserver.SearchRulesOutput{ContractVersion: mcpserver.SearchContractVersion, Retrieval: mcpserver.RetrievalInfo{Status: mcpserver.RetrievalCandidatesFound, ReturnedResults: 1}, Results: []mcpserver.SearchResultDTO{{ID: "rule"}}}, contexts: map[string]mcpserver.ContextOutput{}}
	for i, text := range texts {
		id := fmt.Sprintf("rule#%d", i)
		c.search.Results[0].EvidenceMatches = append(c.search.Results[0].EvidenceMatches, mcpserver.EvidenceMatchDTO{ChunkID: id, Source: "body", ArticleID: "제1조"})
		c.contexts[id] = mcpserver.ContextOutput{Document: mcpserver.DocumentDTO{ID: "rule"}, Chunks: []mcpserver.ChunkDTO{{ID: id, DocumentID: "rule", Source: "body", ArticleID: "제1조", Text: text}}, Content: text}
	}
	return c
}
func evidenceCase() Case {
	return Case{ID: "fragments", Group: "semantic", Split: "development", Input: CaseInput{Query: "추가 납부 기한", Limit: 10}, Expectation: Expectation{EvidenceStatus: "supported", ClaimRelation: "supports", TargetPolicy: "any", Targets: []Target{{DocumentID: "rule", ArticleID: "제1조", Evidence: &EvidenceExpectation{MustContainAll: []string{"추가 납부", "14시"}}}}}}
}

func TestReturnedEvidenceCombinesOnlySameTargetFragments(t *testing.T) {
	item := evidenceCase()
	client := returnedClient("추가 납부 의무", "기한은 14시까지다")
	got := evaluateCase(context.Background(), item, client)
	if !got.EvidenceBundleAt5Matched || !got.AutomaticPassed || got.EvidenceRank != 0 {
		t.Fatalf("same-article fragments not assembled: %#v", got)
	}
	client.search.Results[0].EvidenceMatches[1].ArticleID = "제2조"
	value := client.contexts["rule#1"]
	value.Chunks[0].ArticleID = "제2조"
	client.contexts["rule#1"] = value
	if got = evaluateCase(context.Background(), item, client); got.EvidenceBundleAt5Matched || got.AutomaticPassed {
		t.Fatal("borrowed a missing value from another article")
	}
}

func TestReturnedEvidenceCannotBorrowSixthDocument(t *testing.T) {
	client := returnedClient("추가 납부 기한은 14시까지다")
	target := client.search.Results[0]
	client.search.Results = nil
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("other-%d", i)
		chunkID := id + "#0"
		client.search.Results = append(client.search.Results, mcpserver.SearchResultDTO{ID: id, EvidenceMatches: []mcpserver.EvidenceMatchDTO{{ChunkID: chunkID, Source: "body"}}})
		client.contexts[chunkID] = mcpserver.ContextOutput{Document: mcpserver.DocumentDTO{ID: id}, Chunks: []mcpserver.ChunkDTO{{ID: chunkID, DocumentID: id, Source: "body", Text: "다른 규정"}}, Content: "다른 규정"}
	}
	client.search.Results = append(client.search.Results, target)
	client.search.Retrieval.ReturnedResults = 6
	got := evaluateCase(context.Background(), evidenceCase(), client)
	if got.DocumentRank != 6 || got.EvidenceBundleAt5Matched || got.AutomaticPassed {
		t.Fatalf("sixth document counted as top-5: %#v", got)
	}
}

func TestReturnedContextContractRejectsMissingDuplicateTruncatedAndWrongOwners(t *testing.T) {
	for _, kind := range []string{"missing", "duplicate", "truncated", "article", "attachment", "source", "document", "chunk-document"} {
		t.Run(kind, func(t *testing.T) {
			c := returnedClient("추가 납부 기한은 14시까지다")
			v := c.contexts["rule#0"]
			switch kind {
			case "missing":
				delete(c.contexts, "rule#0")
			case "duplicate":
				c.search.Results[0].EvidenceMatches = append(c.search.Results[0].EvidenceMatches, c.search.Results[0].EvidenceMatches[0])
			case "truncated":
				v.Truncated = true
			case "article":
				v.Chunks[0].ArticleID = "제2조"
			case "attachment":
				v.Chunks[0].AttachmentID = "attachment"
			case "source":
				v.Chunks[0].Source = "attachment"
			case "document":
				v.Document.ID = "another"
			case "chunk-document":
				v.Chunks[0].DocumentID = "another"
			}
			if kind != "missing" {
				c.contexts["rule#0"] = v
			}
			got := evaluateCase(context.Background(), evidenceCase(), c)
			if got.ReturnedEvidenceValid || got.AutomaticPassed {
				t.Fatalf("bad %s context accepted: %#v", kind, got)
			}
		})
	}
}

func TestNegativeAndAmbiguousCandidatesAreNotAnswers(t *testing.T) {
	for _, status := range []string{"insufficient", "ambiguous"} {
		item := evidenceCase()
		item.Expectation.EvidenceStatus = status
		got := evaluateCase(context.Background(), item, returnedClient("추가 납부 기한은 14시까지다"))
		summary, _ := summarize(Fixture{Cases: []Case{item}}, []CaseResult{got}, nil)
		if !got.AutomaticPassed || summary.EvidenceEligible != 0 || summary.EvidenceBundleHitAt5 != 0 || summary.NegativeCasesWithCandidates+summary.AmbiguousCasesWithCandidates != 1 {
			t.Fatalf("candidate confused with answer: %#v", summary)
		}
		data, _ := json.Marshal(summary)
		for _, field := range []string{"false_supported", "answer_coverage", "selected_evidence_precision"} {
			if strings.Contains(string(data), field) {
				t.Fatalf("server-only report claims answer quality: %s", data)
			}
		}
	}
}

func TestReturnedEvidenceStillRequiresReviewedContradictionText(t *testing.T) {
	item := evidenceCase()
	item.Expectation.ClaimRelation = "contradicts"
	item.Expectation.Targets[0].Evidence = &EvidenceExpectation{MustContainAll: []string{"사용"}, RelationMustContainAny: []string{"사용하지 못한다"}}
	if got := evaluateCase(context.Background(), item, returnedClient("다른 용도로 사용할 수 있다")); got.EvidenceBundleAt5Matched {
		t.Fatal("opposite polarity counted as target evidence")
	}
	if got := evaluateCase(context.Background(), item, returnedClient("다른 용도로 사용하지 못한다")); !got.EvidenceBundleAt5Matched {
		t.Fatal("reviewed contradictory evidence not retrieved")
	}
}

func TestDocumentCandidateWithoutChunksIsNotSubstantiveEvidence(t *testing.T) {
	client := returnedClient("추가 납부 기한은 14시까지다")
	client.search.Results[0].EvidenceMatches = nil
	got := evaluateCase(context.Background(), evidenceCase(), client)
	if !got.RetrievalContractValid || !got.ReturnedEvidenceValid || got.DocumentRank != 1 {
		t.Fatalf("metadata candidate rejected: %#v", got)
	}
	if got.EvidenceBundleAt5Matched || len(got.ContextChecks) != 0 || got.AutomaticPassed {
		t.Fatalf("document ID alone counted as substantive evidence: %#v", got)
	}
}
