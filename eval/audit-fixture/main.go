// audit-fixture validates source labels and corpus grounding without retrieval.
package main

import (
	"flag"
	"fmt"
	evaluation "github.com/chromato99/krx-rule-mcp/internal/eval"
	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
)

func main() {
	fixturePath := flag.String("fixture", "", "fixture JSON")
	sourcePath := flag.String("source-fixture", "", "intake JSON")
	dataDir := flag.String("data-dir", "../krx-rule-markdown/data", "corpus directory")
	indexDir := flag.String("index-dir", "index", "index directory")
	flag.Parse()
	fixture, hash, err := evaluation.LoadFixture(*fixturePath)
	must(err)
	must(evaluation.VerifySourceFixture(*sourcePath, fixture.Source.SHA256, fixture.Source.OriginalCases))
	repo, err := searchindex.LoadRepositoryGeneration(*dataDir, *indexDir, searchindex.RepositoryLoadOptions{})
	must(err)
	must(evaluation.ValidateFixtureGrounding(fixture, repo))
	fmt.Printf("grounding passed: cases=%d fixture_sha256=%s case_contract=%s\n", len(fixture.Cases), hash, evaluation.FixtureContractHash(fixture))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
