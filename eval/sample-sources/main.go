// sample-sources produces deterministic source excerpts without running search.
// Run after implementation freeze, before authoring/auditing holdout labels.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	searchindex "github.com/chromato99/krx-rule-mcp/internal/index"
)

type reservation struct {
	Seed string   `json:"seed"`
	IDs  []string `json:"canonical_source_ids"`
}
type excerpt struct {
	ArticleID     string   `json:"article_id,omitempty"`
	HeadingPath   []string `json:"heading_path,omitempty"`
	Text          string   `json:"text"`
	SelectionHash string   `json:"selection_hash"`
	Fallback      bool     `json:"fallback,omitempty"`
}
type source struct {
	ID       string    `json:"id"`
	SourceID string    `json:"source_id"`
	Title    string    `json:"title"`
	Language string    `json:"language"`
	Samples  []excerpt `json:"samples"`
}

func main() {
	reservationPath := flag.String("reservation", "", "reserved source JSON")
	output := flag.String("output", "", "sample JSON output")
	dataDir := flag.String("data-dir", "../krx-rule-markdown/data", "corpus directory")
	indexDir := flag.String("index-dir", "index", "verified index directory")
	flag.Parse()
	if *reservationPath == "" || *output == "" {
		panic("--reservation and --output are required")
	}
	data, err := os.ReadFile(*reservationPath)
	must(err)
	var reserved reservation
	must(json.Unmarshal(data, &reserved))
	repo, err := searchindex.LoadRepositoryGeneration(*dataDir, *indexDir, searchindex.RepositoryLoadOptions{})
	must(err)
	selected := map[string]bool{}
	for _, id := range reserved.IDs {
		selected[id] = true
	}
	var sources []source
	addenda := regexp.MustCompile(`(?mi)^\s*(?:#+\s*)?(?:\*\*)?부\s*칙`)
	deleted := regexp.MustCompile(`(?i)^(?:\*\*)?(?:제[0-9]+조(?:의[0-9]+)?|§[0-9-]+)[^\n]*?(?:삭제|deleted)[^\n]*$`)
	for _, doc := range repo.Documents {
		canonical := doc.SourceID
		if canonical == "" {
			canonical = doc.ID
		}
		if !selected[canonical] || !doc.IsSearchable() {
			continue
		}
		body := doc.Body
		if doc.Language == "ko" {
			if loc := addenda.FindStringIndex(body); loc != nil {
				body = body[:loc[0]]
			}
		}
		parts := searchindex.ChunkTextWithAnchors(body, 1600)
		var groups []excerpt
		seen := map[string]bool{}
		current := ""
		for _, part := range parts {
			if part.ArticleID == "" {
				current = ""
				continue
			}
			if current == part.ArticleID && len(groups) > 0 {
				groups[len(groups)-1].Text += "\n\n" + part.Text
				continue
			}
			current = part.ArticleID
			if seen[current] {
				current = ""
				continue
			}
			seen[current] = true
			groups = append(groups, excerpt{ArticleID: part.ArticleID, HeadingPath: part.HeadingPath, Text: part.Text})
		}
		var eligible []excerpt
		for _, group := range groups {
			if !deleted.MatchString(strings.TrimSpace(group.Text)) {
				eligible = append(eligible, group)
			}
		}
		if len(eligible) < 2 {
			for i, part := range parts {
				if part.ArticleID != "" || len([]rune(part.Text)) < 40 {
					continue
				}
				eligible = append(eligible, excerpt{HeadingPath: part.HeadingPath, Text: part.Text, Fallback: true, SelectionHash: fmt.Sprint(i)})
			}
		}
		for i := range eligible {
			key := eligible[i].ArticleID
			if key == "" {
				key = "paragraph:" + eligible[i].SelectionHash
			}
			digest := sha256.Sum256([]byte(reserved.Seed + canonical + key))
			eligible[i].SelectionHash = hex.EncodeToString(digest[:])
		}
		sort.Slice(eligible, func(i, j int) bool { return eligible[i].SelectionHash < eligible[j].SelectionHash })
		count := 2
		if doc.Language == "en" {
			count = 1
		}
		if len(eligible) < count {
			panic("insufficient substantive source sections: " + doc.ID)
		}
		sources = append(sources, source{ID: doc.ID, SourceID: canonical, Title: doc.Title, Language: doc.Language, Samples: eligible[:count]})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	encoded, err := json.MarshalIndent(struct {
		CorpusRelease string   `json:"corpus_release"`
		Sources       []source `json:"sources"`
	}{repo.CorpusReleaseHash, sources}, "", "  ")
	must(err)
	must(os.WriteFile(*output, append(encoded, '\n'), 0644))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
