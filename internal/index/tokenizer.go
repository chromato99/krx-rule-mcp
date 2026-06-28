package index

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	tokenPattern          = regexp.MustCompile(`[0-9A-Za-z가-힣]+`)
	singleASCIIAlnum      = regexp.MustCompile(`[0-9A-Za-z]`)
	paragraphSplitPattern = regexp.MustCompile(`\n{2,}`)
	whitespacePattern     = regexp.MustCompile(`\s+`)
)

func Tokenize(text string) []string {
	return uniqueTokenize(text)
}

func indexTokenize(text string) []string {
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
