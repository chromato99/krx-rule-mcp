package index

import "strings"

const parentEvidenceLimit = 12
const parentEvidenceRuneLimit = 8000

func (e *Engine) boundedEvidenceParent(c chunk) []int {
	article, paragraph := evidenceParentKeys(c)
	for _, group := range [][]int{e.articleGroups[article], e.paragraphGroups[paragraph]} {
		if len(group) == 0 || len(group) > parentEvidenceLimit {
			continue
		}
		runes := 0
		for _, idx := range group {
			runes += runeLen(e.chunks[idx].Text)
		}
		if runes <= parentEvidenceRuneLimit {
			return group
		}
	}
	return nil
}

func evidenceParentKeys(c chunk) (string, string) {
	if c.ArticleID == "" {
		return "", ""
	}
	for i, label := range c.HeadingPath {
		if !strings.HasPrefix(strings.ReplaceAll(label, " ", ""), c.ArticleID) {
			continue
		}
		article := chunkGroupKey(c) + "\x00" + strings.Join(c.HeadingPath[:i+1], "\x00")
		for j := i + 1; j < len(c.HeadingPath); j++ {
			if paragraphMarkerPattern.MatchString(c.HeadingPath[j]) {
				return article, article + "\x00" + strings.Join(c.HeadingPath[i+1:j+1], "\x00")
			}
		}
		return article, ""
	}
	return "", ""
}

// ExpandEvidenceParents appends complete, bounded source-owned context to the
// first five document candidates. Retrieved evidence keeps its order and scores.
// Context-only siblings are never counted as independent lexical/vector hits.
// Large articles fall back to a complete paragraph; oversized parents are not
// partially spliced into a purported complete statement.
func (e *Engine) ExpandEvidenceParents(query string, results []SearchResult) []SearchResult {
	out := append([]SearchResult(nil), results...)
	for i := 0; i < len(out) && i < 5; i++ {
		original := results[i].EvidenceMatches
		matches := append([]EvidenceMatch(nil), original...)
		seen := map[string]bool{}
		used := 0
		for _, m := range original {
			seen[m.ChunkID] = true
			used += runeLen(m.Text)
		}
		for _, match := range original {
			index, ok := e.chunkByID[match.ChunkID]
			if !ok {
				continue
			}
			c := e.chunks[index]
			group := e.boundedEvidenceParent(c)
			if len(group) == 0 {
				continue
			}
			count, runes := 0, 0
			for _, idx := range group {
				if !seen[e.chunks[idx].ID] {
					count++
					runes += runeLen(e.chunks[idx].Text)
				}
			}
			if len(matches)+count > parentEvidenceLimit || used+runes > parentEvidenceRuneLimit {
				continue
			}
			for _, idx := range group {
				child := e.chunks[idx]
				if seen[child.ID] {
					continue
				}
				context := evidenceMatch(chunkCandidate(child), query)
				context.ContextOnly = true
				matches = append(matches, context)
				seen[child.ID] = true
			}
			used += runes
		}
		// Copy before updating mirrored fields; preserve the input backing slice.
		out[i].EvidenceMatches = nil
		SetSearchResultEvidence(&out[i], matches)
	}
	return out
}
