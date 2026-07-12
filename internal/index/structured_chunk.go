package index

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// AnchoredChunk preserves the structural owner of a piece of legal text.
// ArticleID is the owning article heading, not an article merely cited inside
// the text. HeadingPath proceeds from chapter/section headings through the
// owning article and, when present, paragraph/item/subitem markers.
type AnchoredChunk struct {
	Text        string
	ArticleID   string
	HeadingPath []string
}

type sourceBlock struct {
	text   string
	atomic bool
}

type anchorState struct {
	sections     []sectionHeading
	articleID    string
	articleLabel string
	paragraph    string
	item         string
	subitem      string
}

type sectionHeading struct {
	level int
	label string
}

var (
	articleIDPattern         = regexp.MustCompile(`^제\s*([0-9]+)\s*조(?:\s*의\s*([0-9]+))?`)
	plainArticleTitlePattern = regexp.MustCompile(`^(제\s*[0-9]+\s*조(?:\s*의\s*[0-9]+)?\s*\([^\n)]*\))`)
	plainArticleOnlyPattern  = regexp.MustCompile(`^제\s*[0-9]+\s*조(?:\s*의\s*[0-9]+)?\s*$`)
	legalSectionPattern      = regexp.MustCompile(`^제\s*[0-9]+\s*(장|절|관)(?:\s*의\s*[0-9]+)?(?:\s+.*)?$`)
	supplementHeadingPattern = regexp.MustCompile(`^부\s*칙(?:\([^)]*\))?\s*(?:<[^>]+>)?$`)
	markdownHeadingPattern   = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	paragraphMarkerPattern   = regexp.MustCompile(`^([①②③④⑤⑥⑦⑧⑨⑩⑪⑫⑬⑭⑮⑯⑰⑱⑲⑳])`)
	itemMarkerPattern        = regexp.MustCompile(`^([0-9]{1,3}[.)])`)
	subitemMarkerPattern     = regexp.MustCompile(`^([가나다라마바사아자차카타파하][.)])`)
)

// ChunkTextWithAnchors chunks Markdown at legal structure boundaries. Fenced
// blocks and table rows are atomic. Consequently an individual equation pair
// or oversized table row may exceed maxRunes; semantic integrity takes
// precedence over the target chunk size for those units.
func ChunkTextWithAnchors(text string, maxRunes int) []AnchoredChunk {
	text = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
	if text == "" {
		return nil
	}
	if maxRunes <= 0 {
		maxRunes = 1600
	}

	blocks := parseSourceBlocks(text)
	state := anchorState{}
	chunks := make([]AnchoredChunk, 0, len(blocks))
	var current *AnchoredChunk
	flush := func() {
		if current == nil || strings.TrimSpace(current.Text) == "" {
			current = nil
			return
		}
		current.Text = strings.TrimSpace(current.Text)
		chunks = append(chunks, *current)
		current = nil
	}

	for _, block := range blocks {
		articleID, articleLabel, articleRemainder, isArticle := extractArticleHeading(block.text)
		isHeading := false
		if isArticle {
			flush()
			state.articleID = articleID
			state.articleLabel = articleLabel
			state.paragraph = ""
			state.item = ""
			state.subitem = ""
			state.applyLowerMarker(articleRemainder)
		} else if level, label, ok := extractSectionHeading(block.text); ok {
			flush()
			state.setSection(level, label)
			state.articleID = ""
			state.articleLabel = ""
			state.paragraph = ""
			state.item = ""
			state.subitem = ""
			isHeading = true
		} else {
			state.applyLowerMarker(block.text)
		}

		path := state.headingPath()
		parts := expandSourceBlock(block, maxRunes)
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			anchored := AnchoredChunk{
				Text:        part,
				ArticleID:   state.articleID,
				HeadingPath: append([]string(nil), path...),
			}
			if block.atomic || isHeading || isArticle {
				flush()
				chunks = append(chunks, anchored)
				continue
			}
			if current != nil && current.ArticleID == anchored.ArticleID && stringSlicesEqual(current.HeadingPath, anchored.HeadingPath) && runeLen(current.Text)+2+runeLen(part) <= maxRunes {
				current.Text += "\n\n" + part
				continue
			}
			flush()
			current = &anchored
		}
	}
	flush()
	return chunks
}

func parseSourceBlocks(text string) []sourceBlock {
	lines := strings.Split(text, "\n")
	blocks := make([]sourceBlock, 0, len(lines)/2)
	ordinary := make([]string, 0, 8)
	flushOrdinary := func() {
		value := strings.TrimSpace(strings.Join(ordinary, "\n"))
		ordinary = ordinary[:0]
		if value != "" {
			blocks = append(blocks, sourceBlock{text: value})
		}
	}

	for i := 0; i < len(lines); {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			flushOrdinary()
			i++
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			flushOrdinary()
			first, language, next := readFence(lines, i)
			if language == "hwp-equation" {
				prefix := ""
				if last := len(blocks) - 1; last >= 0 && !blocks[last].atomic && isEquationSourceLabel(blocks[last].text) {
					prefix = blocks[last].text
					blocks = blocks[:last]
				}
				lookahead := next
				for lookahead < len(lines) && strings.TrimSpace(lines[lookahead]) == "" {
					lookahead++
				}
				latexLabel := ""
				if lookahead < len(lines) && isEquationLatexLabel(lines[lookahead]) {
					latexLabel = strings.TrimSpace(lines[lookahead])
					lookahead++
					for lookahead < len(lines) && strings.TrimSpace(lines[lookahead]) == "" {
						lookahead++
					}
				}
				if lookahead < len(lines) && fenceLanguage(lines[lookahead]) == "math" {
					second, _, afterSecond := readFence(lines, lookahead)
					pieces := []string{first}
					if prefix != "" {
						pieces = append([]string{prefix}, pieces...)
					}
					if latexLabel != "" {
						pieces = append(pieces, latexLabel)
					}
					pieces = append(pieces, second)
					first = strings.Join(pieces, "\n\n")
					next = afterSecond
				} else if prefix != "" {
					first = prefix + "\n" + first
				}
			}
			blocks = append(blocks, sourceBlock{text: first, atomic: true})
			i = next
			continue
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "<table") {
			flushOrdinary()
			start := i
			if strings.Contains(strings.ToLower(lines[i]), "</table>") {
				i++
				blocks = append(blocks, sourceBlock{text: strings.TrimSpace(strings.Join(lines[start:i], "\n")), atomic: true})
				continue
			}
			for i++; i < len(lines); i++ {
				if strings.Contains(strings.ToLower(lines[i]), "</table>") {
					i++
					break
				}
			}
			blocks = append(blocks, sourceBlock{text: strings.TrimSpace(strings.Join(lines[start:i], "\n")), atomic: true})
			continue
		}
		if len(ordinary) > 0 && startsStructuralBoundary(trimmed) {
			flushOrdinary()
		}
		ordinary = append(ordinary, lines[i])
		i++
	}
	flushOrdinary()
	return blocks
}

func isEquationSourceLabel(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	return !strings.Contains(text, "\n") && strings.HasPrefix(text, "수식 ") && strings.Contains(text, "원본") && strings.Contains(text, "hwp")
}

func isEquationLatexLabel(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	return !strings.Contains(text, "\n") && strings.HasPrefix(text, "수식 ") && strings.Contains(text, "latex")
}

func startsStructuralBoundary(text string) bool {
	if isEquationSourceLabel(text) {
		return true
	}
	if _, _, _, ok := extractArticleHeading(text); ok {
		return true
	}
	if _, _, ok := extractSectionHeading(text); ok {
		return true
	}
	return paragraphMarkerPattern.MatchString(text) || itemMarkerPattern.MatchString(text) || subitemMarkerPattern.MatchString(text)
}

func readFence(lines []string, start int) (text, language string, next int) {
	language = fenceLanguage(lines[start])
	next = start + 1
	for next < len(lines) {
		if strings.TrimSpace(lines[next]) == "```" {
			next++
			break
		}
		next++
	}
	return strings.TrimSpace(strings.Join(lines[start:next], "\n")), language, next
}

func fenceLanguage(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "```") {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "```")))
}

func expandSourceBlock(block sourceBlock, maxRunes int) []string {
	text := strings.TrimSpace(block.text)
	if text == "" {
		return nil
	}
	if isMarkdownTable(text) {
		if runeLen(text) <= maxRunes {
			return []string{text}
		}
		if parts := splitMarkdownTable(text, maxRunes); len(parts) > 0 {
			return parts
		}
		return []string{text}
	}
	if isHTMLTable(text) {
		if runeLen(text) <= maxRunes {
			return []string{text}
		}
		if parts := splitHTMLTable(text, maxRunes); len(parts) > 0 {
			return parts
		}
		return []string{text}
	}
	if block.atomic {
		return []string{text}
	}
	if utf8.RuneCountInString(text) > maxRunes {
		return splitRunes(text, maxRunes)
	}
	return []string{text}
}

func extractArticleHeading(text string) (id, label, remainder string, ok bool) {
	trimmed := strings.TrimSpace(text)
	candidateText := trimmed
	if match := markdownHeadingPattern.FindStringSubmatch(trimmed); len(match) == 3 {
		candidateText = strings.TrimSpace(match[2])
	}
	if strings.HasPrefix(candidateText, "**") {
		if closeAt := strings.Index(candidateText[2:], "**"); closeAt >= 0 {
			closeAt += 2
			candidate := strings.TrimSpace(candidateText[2:closeAt])
			if id = normalizeArticleID(candidate); id != "" {
				return id, normalizeHeadingLabel(candidate), strings.TrimSpace(candidateText[closeAt+2:]), true
			}
		}
	}
	if match := plainArticleTitlePattern.FindStringSubmatch(candidateText); len(match) == 2 {
		candidate := strings.TrimSpace(match[1])
		remainder := strings.TrimSpace(candidateText[len(match[0]):])
		if looksLikeArticleCitationRemainder(remainder) {
			return "", "", "", false
		}
		return normalizeArticleID(candidate), normalizeHeadingLabel(candidate), remainder, true
	}
	firstLine := candidateText
	if newline := strings.IndexByte(firstLine, '\n'); newline >= 0 {
		firstLine = strings.TrimSpace(firstLine[:newline])
	}
	if plainArticleOnlyPattern.MatchString(firstLine) {
		return normalizeArticleID(firstLine), normalizeHeadingLabel(firstLine), strings.TrimSpace(candidateText[len(firstLine):]), true
	}
	return "", "", "", false
}

func looksLikeArticleCitationRemainder(remainder string) bool {
	for _, prefix := range []string{"에 따라", "에 따른", "에 의하여", "에 의한", "의 규정", "를 준용", "을 준용", "에서 정", "으로 정"} {
		if strings.HasPrefix(remainder, prefix) {
			return true
		}
	}
	return false
}

func normalizeArticleID(value string) string {
	match := articleIDPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) == 0 {
		return ""
	}
	id := "제" + match[1] + "조"
	if len(match) > 2 && match[2] != "" {
		id += "의" + match[2]
	}
	return id
}

func extractSectionHeading(text string) (level int, label string, ok bool) {
	trimmed := strings.TrimSpace(text)
	firstLine := trimmed
	if newline := strings.IndexByte(firstLine, '\n'); newline >= 0 {
		firstLine = strings.TrimSpace(firstLine[:newline])
	}
	if match := markdownHeadingPattern.FindStringSubmatch(firstLine); len(match) == 3 {
		return len(match[1]), normalizeHeadingLabel(match[2]), true
	}
	plain := strings.TrimSpace(strings.Trim(firstLine, "*<>"))
	if match := legalSectionPattern.FindStringSubmatch(plain); len(match) == 2 {
		switch match[1] {
		case "장":
			level = 1
		case "절":
			level = 2
		default:
			level = 3
		}
		return level, normalizeHeadingLabel(plain), true
	}
	if supplementHeadingPattern.MatchString(plain) {
		return 1, normalizeHeadingLabel(plain), true
	}
	return 0, "", false
}

func normalizeHeadingLabel(value string) string {
	value = strings.TrimSpace(strings.Trim(value, "*"))
	value = strings.Join(strings.Fields(value), " ")
	if id := normalizeArticleID(value); id != "" {
		match := articleIDPattern.FindStringIndex(value)
		if len(match) == 2 {
			suffix := strings.TrimSpace(value[match[1]:])
			if strings.HasPrefix(suffix, "(") {
				return id + suffix
			}
			if suffix != "" {
				return id + " " + suffix
			}
			return id
		}
	}
	return value
}

func (s *anchorState) setSection(level int, label string) {
	kept := s.sections[:0]
	for _, heading := range s.sections {
		if heading.level < level {
			kept = append(kept, heading)
		}
	}
	s.sections = append(kept, sectionHeading{level: level, label: label})
}

func (s *anchorState) applyLowerMarker(text string) {
	trimmed := strings.TrimSpace(text)
	if match := paragraphMarkerPattern.FindStringSubmatch(trimmed); len(match) == 2 {
		s.paragraph = match[1]
		s.item = ""
		s.subitem = ""
		return
	}
	if match := itemMarkerPattern.FindStringSubmatch(trimmed); len(match) == 2 {
		s.item = match[1]
		s.subitem = ""
		return
	}
	if match := subitemMarkerPattern.FindStringSubmatch(trimmed); len(match) == 2 {
		s.subitem = match[1]
	}
}

func (s anchorState) headingPath() []string {
	path := make([]string, 0, len(s.sections)+4)
	for _, heading := range s.sections {
		path = append(path, heading.label)
	}
	if s.articleLabel != "" {
		path = append(path, s.articleLabel)
	}
	if s.paragraph != "" {
		path = append(path, s.paragraph)
	}
	if s.item != "" {
		path = append(path, s.item)
	}
	if s.subitem != "" {
		path = append(path, s.subitem)
	}
	return path
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
