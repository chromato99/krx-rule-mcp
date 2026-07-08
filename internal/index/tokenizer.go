package index

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	tokenPattern          = regexp.MustCompile(`[0-9A-Za-z가-힣]+`)
	scriptNotationPattern = regexp.MustCompile(`([0-9A-Za-z가-힣]+)\s*[_^]\{([^}]*)\}`)
	htmlScriptPattern     = regexp.MustCompile(`(?is)([0-9A-Za-z가-힣]+)\s*<(?:sub|sup)>([^<]*)</(?:sub|sup)>`)
	aliasTokenPattern     = regexp.MustCompile(`[0-9A-Za-z가-힣]+`)
	singleASCIIAlnum      = regexp.MustCompile(`[0-9A-Za-z]`)
	paragraphSplitPattern = regexp.MustCompile(`\n{2,}`)
	whitespacePattern     = regexp.MustCompile(`\s+`)
	htmlTableOpenPattern  = regexp.MustCompile(`(?is)<table\b[^>]*>`)
	htmlTableRowPattern   = regexp.MustCompile(`(?is)<tr\b[^>]*>.*?</tr>`)
)

func Tokenize(text string) []string {
	return uniqueTokenize(text)
}

func indexTokenize(text string) []string {
	text = appendScriptNotationAliases(text)
	raw := tokenPattern.FindAllString(strings.ToLower(text), -1)
	tokens := make([]string, 0, len(raw)*2)
	for _, tok := range raw {
		addToken(&tokens, nil, tok)
		if isHangulToken(tok) {
			runes := []rune(tok)
			for n := 2; n <= 3; n++ {
				if len(runes) < n {
					continue
				}
				for i := 0; i+n <= len(runes); i++ {
					addToken(&tokens, nil, string(runes[i:i+n]))
				}
			}
		}
	}
	return tokens
}

func uniqueTokenize(text string) []string {
	text = appendScriptNotationAliases(text)
	raw := tokenPattern.FindAllString(strings.ToLower(text), -1)
	seen := make(map[string]struct{}, len(raw)*2)
	tokens := make([]string, 0, len(raw)*2)
	for _, tok := range raw {
		addToken(&tokens, seen, tok)
		if isHangulToken(tok) {
			runes := []rune(tok)
			for n := 2; n <= 3; n++ {
				if len(runes) < n {
					continue
				}
				for i := 0; i+n <= len(runes); i++ {
					addToken(&tokens, seen, string(runes[i:i+n]))
				}
			}
		}
	}
	return tokens
}

func appendScriptNotationAliases(text string) string {
	aliases := scriptNotationAliases(text)
	if len(aliases) == 0 {
		return text
	}
	return text + " " + strings.Join(aliases, " ")
}

func scriptNotationAliases(text string) []string {
	aliases := []string{}
	for _, match := range scriptNotationPattern.FindAllStringSubmatch(text, -1) {
		if len(match) == 3 {
			aliases = appendScriptAlias(aliases, match[1], match[2])
		}
	}
	for _, match := range htmlScriptPattern.FindAllStringSubmatch(text, -1) {
		if len(match) == 3 {
			aliases = appendScriptAlias(aliases, match[1], match[2])
		}
	}
	return aliases
}

func appendScriptAlias(aliases []string, baseRaw, scriptRaw string) []string {
	base := strings.Join(aliasTokenPattern.FindAllString(baseRaw, -1), "")
	script := strings.Join(aliasTokenPattern.FindAllString(scriptRaw, -1), "")
	if base == "" || script == "" {
		return aliases
	}
	return append(aliases, base+script)
}

func addToken(tokens *[]string, seen map[string]struct{}, token string) {
	if token == "" {
		return
	}
	if utf8.RuneCountInString(token) == 1 && !singleASCIIAlnum.MatchString(token) {
		return
	}
	if seen != nil {
		if _, ok := seen[token]; ok {
			return
		}
		seen[token] = struct{}{}
	}
	*tokens = append(*tokens, token)
}

func isHangulToken(token string) bool {
	for _, r := range token {
		if r < '가' || r > '힣' {
			return false
		}
	}
	return token != ""
}

func ChunkText(text string, maxRunes int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if maxRunes <= 0 {
		maxRunes = 1600
	}
	paras := paragraphSplitPattern.Split(text, -1)
	var chunks []string
	var current strings.Builder
	currentLen := 0
	for _, para := range paras {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		paraLen := utf8.RuneCountInString(para)
		if currentLen > 0 && currentLen+paraLen+2 > maxRunes {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
			currentLen = 0
		}
		if paraLen > maxRunes && isMarkdownTable(para) {
			for _, part := range splitMarkdownTable(para, maxRunes) {
				if currentLen > 0 {
					chunks = append(chunks, strings.TrimSpace(current.String()))
					current.Reset()
					currentLen = 0
				}
				chunks = append(chunks, part)
			}
			continue
		}
		if paraLen > maxRunes && isHTMLTable(para) {
			for _, part := range splitHTMLTable(para, maxRunes) {
				if currentLen > 0 {
					chunks = append(chunks, strings.TrimSpace(current.String()))
					current.Reset()
					currentLen = 0
				}
				chunks = append(chunks, part)
			}
			continue
		}
		if paraLen > maxRunes {
			for _, part := range splitRunes(para, maxRunes) {
				if currentLen > 0 {
					chunks = append(chunks, strings.TrimSpace(current.String()))
					current.Reset()
					currentLen = 0
				}
				chunks = append(chunks, part)
			}
			continue
		}
		if currentLen > 0 {
			current.WriteString("\n\n")
			currentLen += 2
		}
		current.WriteString(para)
		currentLen += paraLen
	}
	if currentLen > 0 {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}
	return chunks
}

func isMarkdownTable(text string) bool {
	lines := nonEmptyLines(text)
	if len(lines) < 3 {
		return false
	}
	return strings.Contains(lines[0], "|") && tableSeparatorLine(lines[1])
}

func splitMarkdownTable(text string, maxRunes int) []string {
	lines := nonEmptyLines(text)
	if len(lines) < 3 {
		return splitRunes(text, maxRunes)
	}
	header := []string{lines[0], lines[1]}
	var parts []string
	current := append([]string{}, header...)
	currentLen := runeLen(strings.Join(current, "\n"))
	for _, row := range lines[2:] {
		rowLen := runeLen(row) + 1
		if len(current) > len(header) && currentLen+rowLen > maxRunes {
			parts = append(parts, strings.Join(current, "\n"))
			current = append([]string{}, header...)
			currentLen = runeLen(strings.Join(current, "\n"))
		}
		current = append(current, row)
		currentLen += rowLen
	}
	if len(current) > len(header) {
		parts = append(parts, strings.Join(current, "\n"))
	}
	return parts
}

func isHTMLTable(text string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(text))
	return strings.HasPrefix(trimmed, "<table") && strings.Contains(trimmed, "</table>")
}

func splitHTMLTable(text string, maxRunes int) []string {
	open := htmlTableOpenPattern.FindString(text)
	rows := htmlTableRowPattern.FindAllString(text, -1)
	if open == "" || len(rows) < 2 {
		return splitRunes(text, maxRunes)
	}
	header := []string{strings.TrimSpace(rows[0])}
	body := rows[1:]
	var parts []string
	current := append([]string{}, header...)
	currentLen := htmlTableLen(open, current)
	for _, rawRow := range body {
		row := strings.TrimSpace(rawRow)
		rowLen := runeLen(row) + 1
		if len(current) > len(header) && currentLen+rowLen > maxRunes {
			parts = append(parts, renderHTMLTableChunk(open, current))
			current = append([]string{}, header...)
			currentLen = htmlTableLen(open, current)
		}
		current = append(current, row)
		currentLen += rowLen
	}
	if len(current) > len(header) {
		parts = append(parts, renderHTMLTableChunk(open, current))
	}
	return parts
}

func htmlTableLen(open string, rows []string) int {
	return runeLen(open) + runeLen("</table>") + runeLen(strings.Join(rows, "\n")) + len(rows) + 2
}

func renderHTMLTableChunk(open string, rows []string) string {
	return strings.Join(append(append([]string{open}, rows...), "</table>"), "\n")
}

func nonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func tableSeparatorLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, "|") {
		return false
	}
	trimmed = strings.Trim(trimmed, "| ")
	for _, cell := range strings.Split(trimmed, "|") {
		cell = strings.TrimSpace(cell)
		if cell == "" || strings.Trim(cell, "-:") != "" {
			return false
		}
	}
	return true
}

func runeLen(text string) int {
	return utf8.RuneCountInString(text)
}

func splitRunes(text string, maxRunes int) []string {
	runes := []rune(text)
	var parts []string
	for start := 0; start < len(runes); start += maxRunes {
		end := start + maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		parts = append(parts, strings.TrimSpace(string(runes[start:end])))
	}
	return parts
}

func Snippet(text, query string, maxRunes int) string {
	text = strings.TrimSpace(whitespacePattern.ReplaceAllString(text, " "))
	if text == "" {
		return ""
	}
	if maxRunes <= 0 {
		maxRunes = 280
	}
	lower := strings.ToLower(text)
	start := 0
	for _, token := range Tokenize(query) {
		if idx := strings.Index(lower, strings.ToLower(token)); idx >= 0 {
			start = idx
			break
		}
	}
	runes := []rune(text)
	bytePrefix := text[:start]
	runeStart := utf8.RuneCountInString(bytePrefix)
	if runeStart > 40 {
		runeStart -= 40
	} else {
		runeStart = 0
	}
	runeEnd := runeStart + maxRunes
	if runeEnd > len(runes) {
		runeEnd = len(runes)
	}
	out := string(runes[runeStart:runeEnd])
	if runeStart > 0 {
		out = "..." + out
	}
	if runeEnd < len(runes) {
		out += "..."
	}
	return out
}
