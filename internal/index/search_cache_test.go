package index

import (
	"container/heap"
	"fmt"
	"math"
	"os"
	"reflect"
	"testing"

	"github.com/chromato99/krx-rule-mcp/internal/model"
)

// The exhaustive reference deliberately scans every chunk/term. This checks
// score and top-K equivalence, including weighted terms, filters and ties.
func exhaustiveBM25(e *Engine, tokens, terms []string, filter Filter, weights map[string]float64, limit int) []ChunkCandidate {
	all := &chunkCandidateHeap{}
	heap.Init(all)
	for _, c := range e.chunks {
		if !matchesFilter(e.docs[c.DocID], filter) {
			continue
		}
		var score float64
		for _, token := range tokens {
			tf := float64(c.tokenMap[token])
			if tf == 0 {
				continue
			}
			weight := weights[token]
			if weight <= 0 {
				weight = 1
			}
			idf := math.Log(1 + (float64(len(e.chunks))-float64(e.df[token])+0.5)/(float64(e.df[token])+0.5))
			score += weight * idf * (tf * 2.4 / (tf + 1.4*(0.25+0.75*float64(len(c.Tokens))/e.avgDocLength)))
		}
		if score > 0 {
			item := chunkCandidate(c)
			item.Score = score
			item.BM25Score = score
			pushChunkCandidate(all, item, limit)
		}
	}
	return rankCoveredCandidates(all.items, terms)
}

func TestPostingsPreserveExhaustiveScores(t *testing.T) {
	var docs []model.Document
	for i := 0; i < 45; i++ {
		docs = append(docs, model.Document{ID: fmt.Sprint(i), Title: "검색 절차", Category: fmt.Sprint(i % 3), Language: "ko", DocumentType: model.DocumentTypeRule,
			Body: fmt.Sprintf("**제1조(거래 기준)** 거래소 회원은 자료%d를 제출한다.\n\n① 신청 자료와 결제 증거금을 확인한다.\n\n**제2조(가격)** 평가가격 비율%d를 산출한다.", i%7, i%5)})
	}
	e := buildTestEngine(docs, nil, nil)
	if len(e.chunks) < len(docs) {
		t.Fatal("test corpus is not searchable")
	}
	for _, query := range []string{"자료 제출", "평가가격 비율", "없는단어", "거래소 회원 신청 결제 증거금"} {
		for _, filter := range []Filter{{}, {Category: "1"}, {Language: "en"}} {
			for _, limit := range []int{1, 7, 120} {
				tokens := uniqueSearchTokens(Tokenize(query))
				terms := meaningfulQueryTerms(query)
				weights := map[string]float64{"자료": 0.4, "제출": 2}
				got := e.bm25ChunkCandidates(tokens, terms, filter, weights, limit)
				want := exhaustiveBM25(e, tokens, terms, filter, weights, limit)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("postings differ: query=%q filter=%+v limit=%d\ngot=%v\nwant=%v", query, filter, limit, got, want)
				}
			}
		}
	}
}

// Opt-in actual-corpus benchmark; no query embedding/network dependency.
func BenchmarkCorpusBM25(b *testing.B) {
	if os.Getenv("KRX_DATA_TEST") != "1" {
		b.Skip("set KRX_DATA_TEST=1")
	}
	repo, err := LoadRepositoryGeneration("../../../krx-rule-markdown/data", "../../index", RepositoryLoadOptions{})
	if err != nil {
		b.Fatal(err)
	}
	e := repo.Engine
	for _, query := range []string{"증거금 예탁 시한", "상장 심사 제출 서류", "market clearing settlement obligations"} {
		tokens := uniqueSearchTokens(Tokenize(query))
		terms := meaningfulQueryTerms(query)
		b.Run(query+"/postings", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				e.bm25ChunkCandidates(tokens, terms, Filter{}, nil, 120)
			}
		})
		b.Run(query+"/exhaustive", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				exhaustiveBM25(e, tokens, terms, Filter{}, nil, 120)
			}
		})
	}
}
