package index

import (
	"github.com/chromato99/krx-rule-mcp/internal/model"
	"strings"
	"testing"
)

func TestParentEvidencePreservesOwnerAndRetrieval(t *testing.T) {
	body := "**제1조(신고 항목)**① 회원은 다음 사항을 신고한다.\n\n1. 계약 체결일\n\n2. 결제 방식\n\n3. 거래 대상\n\n4. 계약 수량\n\n5. 계약 가격\n\n**제2조(다른 의무)** 항공 운송을 신고한다."
	e := buildTestEngine([]model.Document{{ID: "rule", Title: "신고규정", Body: body}, {ID: "other", Title: "다른규정", Body: body}}, nil, nil)
	var leaf chunk
	for _, c := range e.chunks {
		if c.DocID == "rule" && strings.Contains(c.Text, "3. 거래 대상") {
			leaf = c
			break
		}
	}
	seed := evidenceMatch(chunkCandidate(leaf), "거래 대상")
	seed.BM25Score = 4
	seed.VectorScore = .8
	seed.Score = .03
	result := SearchResult{ID: "rule", Title: "신고규정", Score: .04}
	SetSearchResultEvidence(&result, []EvidenceMatch{seed})
	got := e.ExpandEvidenceParents("거래 대상", []SearchResult{result})[0]
	if len(got.EvidenceMatches) != 6 {
		t.Fatalf("incomplete reporting list: %+v", got.EvidenceMatches)
	}
	if got.Score != result.Score || got.MatchedChunkID != seed.ChunkID || got.BM25Score != 4 || len(result.EvidenceMatches) != 1 {
		t.Fatal("context changed retrieval provenance or input")
	}
	for i, m := range got.EvidenceMatches {
		doc, contexts, ok := e.ContextAround(m.ChunkID, 0, 0)
		if !ok || doc.ID != "rule" || contexts[0].ArticleID != "제1조" {
			t.Fatal("parent crossed owner")
		}
		if i > 0 && (!m.ContextOnly || m.Score != 0 || m.BM25Score != 0 || m.VectorScore != 0 || m.LexicalCoverage != 0) {
			t.Fatal("context fabricated retrieval scores")
		}
	}
}

func TestOversizedParentIsNotPartiallyIncluded(t *testing.T) {
	e := buildTestEngine([]model.Document{{ID: "rule", Body: "**제1조(긴 문맥)**\n\n" + strings.Repeat("문맥", 5000)}}, nil, nil)
	c := e.chunks[0]
	m := evidenceMatch(chunkCandidate(c), "문맥")
	got := e.ExpandEvidenceParents("문맥", []SearchResult{{ID: "rule", EvidenceMatches: []EvidenceMatch{m}}})
	if len(got[0].EvidenceMatches) != 1 {
		t.Fatal("oversized parent partially assembled")
	}
}

func TestParentEvidenceCannotCrossAttachmentOrArticleInstance(t *testing.T) {
	doc := model.Document{ID: "rule", Body: "**제1조(본문)** 본문의 의무이다.", Attachments: []model.Attachment{{ID: "a", Title: "서식 A"}, {ID: "b", Title: "서식 B"}}}
	attachments := map[string]AttachmentDocument{
		"a": {Attachment: doc.Attachments[0], Text: "**제1조(서식 항목)**① 신청 정보를 제출한다.\n\n1. 신청인\n\n2. 신청일\n\n**제1조(경과조치)** 과거의 별도 기준이다."},
		"b": {Attachment: doc.Attachments[1], Text: "**제1조(다른 서식)** 다른 서식의 의무이다."},
	}
	e := buildTestEngine([]model.Document{doc}, attachments, nil)
	var seed EvidenceMatch
	for _, c := range e.chunks {
		if c.AttachmentID == "a" && strings.Contains(c.Text, "1. 신청인") {
			seed = evidenceMatch(chunkCandidate(c), "신청인")
			break
		}
	}
	if seed.ChunkID == "" {
		t.Fatal("missing attachment fixture")
	}
	result := e.ExpandEvidenceParents("신청인", []SearchResult{{ID: "rule", EvidenceMatches: []EvidenceMatch{seed}}})[0]
	if len(result.EvidenceMatches) != 3 {
		t.Fatalf("wrong attachment parent size: %d", len(result.EvidenceMatches))
	}
	for _, m := range result.EvidenceMatches {
		if m.AttachmentID != "a" || strings.Contains(m.Text, "과거") {
			t.Fatal("parent context crossed attachment/article instance")
		}
	}
}
