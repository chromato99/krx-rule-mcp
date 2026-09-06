package index

import (
	"sort"
	"strings"
)

// These caches are derived from the verified snapshot at load time. They do
// not change tokenization, BM25 scores, corpus identity, or the artifact format.
// Engine searches only read them, including concurrent category probes.
func (e *Engine) prepareSearchCaches() {
	e.postings = make(map[string][]int, len(e.df))
	e.articleGroups = make(map[string][]int)
	e.paragraphGroups = make(map[string][]int)
	for index, c := range e.chunks {
		for token := range c.tokenMap {
			e.postings[token] = append(e.postings[token], index)
		}
		if article, paragraph := evidenceParentKeys(c); article != "" {
			e.articleGroups[article] = append(e.articleGroups[article], index)
			if paragraph != "" {
				e.paragraphGroups[paragraph] = append(e.paragraphGroups[paragraph], index)
			}
		}
	}
	e.metadataTokens = make(map[string]map[string]int, len(e.docs))
	e.sourceTitles = make(map[string][]string, len(e.docs))
	for id, doc := range e.docs {
		titles := []string{doc.Title}
		// The portal sometimes labels an English PDF with its Korean title.
		// A printed legal title is source-derived metadata, not a query alias.
		if doc.Language == "en" {
			printed := strings.Trim(strings.SplitN(strings.TrimSpace(doc.Body), "\n\n", 2)[0], "#* \n")
			if runeLen(printed) <= 180 && legalDocumentTitle(printed) && printed != doc.Title {
				titles = append(titles, printed)
			}
		}
		e.sourceTitles[id] = titles
		e.metadataTokens[id] = countTokens(Tokenize(strings.Join(titles, " ") + " " + doc.Category))
	}
	for _, groups := range []map[string][]int{e.chunkGroups, e.articleGroups, e.paragraphGroups} {
		for _, group := range groups {
			sort.Slice(group, func(i, j int) bool { return e.chunks[group[i]].Index < e.chunks[group[j]].Index })
		}
	}
}
