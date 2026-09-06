package mcp

import (
	"context"
	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
	"os"
	"testing"
)

// Opt-in profiling of the real public service path, including embedding,
// bounded scope probes and evidence assembly. Never runs in ordinary CI.
func BenchmarkCorpusSearch(b *testing.B) {
	if os.Getenv("KRX_VECTOR_DATA_TEST") != "1" {
		b.Skip("set KRX_VECTOR_DATA_TEST=1")
	}
	repo, err := searchindex.LoadRepositoryGeneration("../../../krx-rule-markdown/data", "../../index", searchindex.RepositoryLoadOptions{VectorEnabled: true, RequireVector: true})
	if err != nil {
		b.Fatal(err)
	}
	embedder, err := searchindex.NewQueryEmbedderFromEnv()
	if err != nil {
		b.Fatal(err)
	}
	lexicon, _, err := searchindex.LoadDomainLexiconWithDigest("../../config/domain-lexicon.yaml")
	if err != nil {
		b.Fatal(err)
	}
	s := &Service{Repo: repo, Embedder: embedder, VectorRequired: true, DomainLexicon: lexicon}
	for _, query := range []string{"일중청산증거금 예탁 시한", "어떤 보고서인지 구분하지 않고 제출 마감 날짜를 하나로 정해줘"} {
		b.Run(query, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := s.SearchRules(context.Background(), SearchRulesInput{Query: query, Language: "ko", DocumentType: "rule", Limit: 5}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
