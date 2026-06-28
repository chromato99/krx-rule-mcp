package index

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/model"
)

type Filter struct {
	DocumentType  model.DocumentType `json:"document_type,omitempty"`
	Language      string             `json:"language,omitempty"`
	Category      string             `json:"category,omitempty"`
	EffectiveFrom string             `json:"effective_from,omitempty"`
	EffectiveTo   string             `json:"effective_to,omitempty"`
	PublishedFrom string             `json:"published_from,omitempty"`
	PublishedTo   string             `json:"published_to,omitempty"`
}

type SearchOptions struct {
	Query       string
	Limit       int
	Filter      Filter
	QueryVector []float64
}

type SearchResult struct {
	ID                string               `json:"id"`
	Title             string               `json:"title"`
	Category          string               `json:"category,omitempty"`
	DocumentType      model.DocumentType   `json:"document_type"`
	Language          string               `json:"language"`
	SourceID          string               `json:"source_id,omitempty"`
	SourceURL         string               `json:"source_url"`
	EffectiveDate     string               `json:"effective_date,omitempty"`
	PublishedDate     string               `json:"published_date,omitempty"`
	Score             float64              `json:"score"`
	BM25Score         float64              `json:"bm25_score,omitempty"`
	VectorScore       float64              `json:"vector_score,omitempty"`
	Snippet           string               `json:"snippet,omitempty"`
	MatchedSource     string               `json:"matched_source,omitempty"`
	AttachmentMatches []AttachmentMatch    `json:"attachment_matches,omitempty"`
	FormulaNotice     *model.FormulaNotice `json:"formula_notice,omitempty"`
	URI               string               `json:"uri"`
}

type AttachmentMatch struct {
	ID            string                 `json:"id"`
	Title         string                 `json:"title"`
	FileName      string                 `json:"file_name,omitempty"`
	URI           string                 `json:"uri"`
	Status        model.AttachmentStatus `json:"status,omitempty"`
	Score         float64                `json:"score,omitempty"`
	Snippet       string                 `json:"snippet,omitempty"`
	FormulaNotice *model.FormulaNotice   `json:"formula_notice,omitempty"`
}

type Engine struct {
	docs         map[string]model.Document
	chunks       []chunk
	df           map[string]int
	avgDocLength float64
}

type chunk struct {
	ID               string                 `json:"id"`
	DocID            string                 `json:"doc_id"`
	Index            int                    `json:"index"`
	Text             string                 `json:"text"`
	Source           string                 `json:"source"`
	AttachmentID     string                 `json:"attachment_id,omitempty"`
	AttachmentTitle  string                 `json:"attachment_title,omitempty"`
	AttachmentFile   string                 `json:"attachment_file,omitempty"`
	AttachmentStatus model.AttachmentStatus `json:"attachment_status,omitempty"`
	Tokens           []string               `json:"tokens"`
	Vector           []float64              `json:"vector,omitempty"`
	tokenMap         map[string]int
}

func Build(docs []model.Document, vectors map[string][]float64) *Engine {
	return BuildWithAttachments(docs, nil, vectors)
}

func BuildWithAttachments(docs []model.Document, attachments map[string]AttachmentDocument, vectors map[string][]float64) *Engine {
	e := &Engine{
		docs: make(map[string]model.Document, len(docs)),
		df:   map[string]int{},
	}
	var totalLen int
	for _, doc := range docs {
		e.docs[doc.ID] = doc
		parts := ChunkText(doc.Body, 1600)
		if len(parts) == 0 {
			parts = []string{doc.Title}
		}
		for i, part := range parts {
			id := doc.ID + "#" + itoa(i)
			c := chunk{
				ID:     id,
				DocID:  doc.ID,
				Index:  i,
				Source: "body",
				Text:   part,
				Vector: vectors[id],
			}
			totalLen += e.addChunk(c, doc.Title+" "+doc.Category+" "+part)
		}
		for _, att := range doc.Attachments {
			attDoc, ok := attachments[att.ID]
			if !ok || strings.TrimSpace(attDoc.Text) == "" {
				continue
			}
			parts := ChunkText(attDoc.Text, 1600)
			for i, part := range parts {
				id := doc.ID + "#att-" + att.ID + "-" + itoa(i)
				c := chunk{
					ID:               id,
					DocID:            doc.ID,
					Index:            i,
					Source:           "attachment",
					AttachmentID:     att.ID,
					AttachmentTitle:  firstNonEmpty(att.Title, att.FileName),
					AttachmentFile:   att.FileName,
					AttachmentStatus: att.Status,
					Text:             part,
					Vector:           vectors[id],
				}
				totalLen += e.addChunk(c, doc.Title+" "+doc.Category+" "+att.Title+" "+att.FileName+" "+part)
			}
		}
	}
	if len(e.chunks) > 0 {
		e.avgDocLength = float64(totalLen) / float64(len(e.chunks))
	}
	return e
}

func (e *Engine) addChunk(c chunk, tokenText string) int {
	tokens := Tokenize(tokenText)
	c.Tokens = tokens
	c.tokenMap = countTokens(tokens)
	seen := map[string]struct{}{}
	for _, tok := range tokens {
		if _, ok := seen[tok]; ok {
			continue
		}
		seen[tok] = struct{}{}
		e.df[tok]++
	}
	e.chunks = append(e.chunks, c)
	return len(tokens)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func (e *Engine) Search(opts SearchOptions) []SearchResult {
	if opts.Limit <= 0 || opts.Limit > 50 {
		opts.Limit = 10
	}
	queryTokens := Tokenize(opts.Query)
	bm25 := e.bm25Scores(queryTokens, opts.Filter)
	vector := e.vectorScores(opts.QueryVector, opts.Filter)
	if len(vector) > 0 && len(bm25) > 0 {
		return e.rrf(opts, bm25, vector)
	}
	if len(vector) > 0 && len(queryTokens) == 0 {
		return e.rankVector(opts, vector)
	}
	return e.rankBM25(opts, bm25)
}

func (e *Engine) Documents(filter Filter, limit, offset int) []model.Document {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var docs []model.Document
	for _, doc := range e.docs {
		if matchesFilter(doc, filter) {
			docs = append(docs, doc)
		}
	}
	sort.Slice(docs, func(i, j int) bool {
		return docSortDate(docs[i]).After(docSortDate(docs[j]))
	})
	if offset >= len(docs) {
		return nil
	}
	end := offset + limit
	if end > len(docs) {
		end = len(docs)
	}
	return docs[offset:end]
}

func (e *Engine) Recent(limit int, typ model.DocumentType, language string) []model.Document {
	return e.Documents(Filter{DocumentType: typ, Language: language}, limit, 0)
}

func (e *Engine) Document(id string) (model.Document, bool) {
	doc, ok := e.docs[id]
	return doc, ok
}

func (e *Engine) Chunks() []chunk {
	out := make([]chunk, len(e.chunks))
	copy(out, e.chunks)
	return out
}

func (e *Engine) SetVectors(vectors map[string][]float64) {
	for i := range e.chunks {
		if vec, ok := vectors[e.chunks[i].ID]; ok {
			e.chunks[i].Vector = vec
		}
	}
}

func (e *Engine) HasVectors() bool {
	for _, c := range e.chunks {
		if len(c.Vector) > 0 {
			return true
		}
	}
	return false
}

func (e *Engine) bm25Scores(queryTokens []string, filter Filter) map[string]SearchResult {
	out := map[string]SearchResult{}
	if len(queryTokens) == 0 || len(e.chunks) == 0 {
		return out
	}
	k1 := 1.4
	b := 0.75
	for _, c := range e.chunks {
		doc := e.docs[c.DocID]
		if !matchesFilter(doc, filter) {
			continue
		}
		var score float64
		for _, tok := range queryTokens {
			tf := float64(c.tokenMap[tok])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (float64(len(e.chunks))-float64(e.df[tok])+0.5)/(float64(e.df[tok])+0.5))
			denom := tf + k1*(1-b+b*float64(len(c.Tokens))/e.avgDocLength)
			score += idf * (tf * (k1 + 1) / denom)
		}
		if score <= 0 {
			continue
		}
		res := resultFromDoc(doc)
		res.Score = score
		res.BM25Score = score
		res.Snippet = Snippet(c.Text, strings.Join(queryTokens, " "), 300)
		res.MatchedSource = c.Source
		if c.Source == "attachment" {
			res.Snippet = "첨부 " + c.AttachmentTitle + ": " + res.Snippet
			res.AttachmentMatches = []AttachmentMatch{attachmentMatch(c, score, res.Snippet)}
		}
		if prev, ok := out[doc.ID]; !ok || score > prev.BM25Score {
			if ok && len(prev.AttachmentMatches) > 0 && len(res.AttachmentMatches) == 0 {
				res.AttachmentMatches = prev.AttachmentMatches
			}
			out[doc.ID] = res
		} else if ok && c.Source == "attachment" {
			prev.AttachmentMatches = mergeAttachmentMatch(prev.AttachmentMatches, attachmentMatch(c, score, "첨부 "+c.AttachmentTitle+": "+Snippet(c.Text, strings.Join(queryTokens, " "), 300)))
			out[doc.ID] = prev
		}
	}
	return out
}

func (e *Engine) vectorScores(queryVector []float64, filter Filter) map[string]SearchResult {
	out := map[string]SearchResult{}
	if len(queryVector) == 0 {
		return out
	}
	for _, c := range e.chunks {
		if len(c.Vector) == 0 {
			continue
		}
		doc := e.docs[c.DocID]
		if !matchesFilter(doc, filter) {
			continue
		}
		score := cosine(queryVector, c.Vector)
		if score <= 0 {
			continue
		}
		res := resultFromDoc(doc)
		res.Score = score
		res.VectorScore = score
		res.Snippet = Snippet(c.Text, doc.Title, 300)
		res.MatchedSource = c.Source
		if c.Source == "attachment" {
			res.Snippet = "첨부 " + c.AttachmentTitle + ": " + res.Snippet
			res.AttachmentMatches = []AttachmentMatch{attachmentMatch(c, score, res.Snippet)}
		}
		if prev, ok := out[doc.ID]; !ok || score > prev.VectorScore {
			if ok && len(prev.AttachmentMatches) > 0 && len(res.AttachmentMatches) == 0 {
				res.AttachmentMatches = prev.AttachmentMatches
			}
			out[doc.ID] = res
		} else if ok && c.Source == "attachment" {
			prev.AttachmentMatches = mergeAttachmentMatch(prev.AttachmentMatches, attachmentMatch(c, score, "첨부 "+c.AttachmentTitle+": "+Snippet(c.Text, doc.Title, 300)))
			out[doc.ID] = prev
		}
	}
	return out
}

func (e *Engine) rankBM25(opts SearchOptions, scores map[string]SearchResult) []SearchResult {
	results := mapValues(scores)
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return docSortDate(e.docs[results[i].ID]).After(docSortDate(e.docs[results[j].ID]))
		}
		return results[i].Score > results[j].Score
	})
	return trim(results, opts.Limit)
}

func (e *Engine) rankVector(opts SearchOptions, scores map[string]SearchResult) []SearchResult {
	results := mapValues(scores)
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return trim(results, opts.Limit)
}

func (e *Engine) rrf(opts SearchOptions, bm25, vector map[string]SearchResult) []SearchResult {
	combined := map[string]SearchResult{}
	addRank := func(scores map[string]SearchResult, kind string) {
		results := mapValues(scores)
		sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
		for rank, res := range results {
			existing := combined[res.ID]
			if existing.ID == "" {
				existing = res
				existing.Score = 0
			}
			existing.Score += 1.0 / (60.0 + float64(rank+1))
			if kind == "bm25" {
				existing.BM25Score = res.BM25Score
				if existing.Snippet == "" {
					existing.Snippet = res.Snippet
				}
			} else {
				existing.VectorScore = res.VectorScore
				if existing.Snippet == "" {
					existing.Snippet = res.Snippet
				}
			}
			existing.AttachmentMatches = mergeAttachmentMatches(existing.AttachmentMatches, res.AttachmentMatches)
			if existing.MatchedSource == "" || existing.MatchedSource == "body" && res.MatchedSource == "attachment" {
				existing.MatchedSource = res.MatchedSource
			}
			combined[res.ID] = existing
		}
	}
	addRank(bm25, "bm25")
	addRank(vector, "vector")
	results := mapValues(combined)
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return trim(results, opts.Limit)
}

func attachmentMatch(c chunk, score float64, snippet string) AttachmentMatch {
	return AttachmentMatch{
		ID:       c.AttachmentID,
		Title:    c.AttachmentTitle,
		FileName: c.AttachmentFile,
		URI:      "krx-rule://attachments/" + c.AttachmentID,
		Status:   c.AttachmentStatus,
		Score:    score,
		Snippet:  snippet,
	}
}

func mergeAttachmentMatches(existing, incoming []AttachmentMatch) []AttachmentMatch {
	for _, match := range incoming {
		existing = mergeAttachmentMatch(existing, match)
	}
	return existing
}

func mergeAttachmentMatch(matches []AttachmentMatch, incoming AttachmentMatch) []AttachmentMatch {
	if incoming.ID == "" {
		return matches
	}
	for i, match := range matches {
		if match.ID == incoming.ID {
			if incoming.Score > match.Score {
				matches[i] = incoming
			}
			return matches
		}
	}
	matches = append(matches, incoming)
	sort.Slice(matches, func(i, j int) bool { return matches[i].Score > matches[j].Score })
	if len(matches) > 5 {
		matches = matches[:5]
	}
	return matches
}

func resultFromDoc(doc model.Document) SearchResult {
	return SearchResult{
		ID:            doc.ID,
		Title:         doc.Title,
		Category:      doc.Category,
		DocumentType:  doc.DocumentType,
		Language:      model.NormalizeLanguage(doc.Language),
		SourceID:      doc.SourceID,
		SourceURL:     doc.SourceURL,
		EffectiveDate: doc.EffectiveDate,
		PublishedDate: doc.PublishedDate,
		URI:           doc.URI(),
	}
}

func matchesFilter(doc model.Document, filter Filter) bool {
	if filter.DocumentType != "" && doc.DocumentType != filter.DocumentType {
		return false
	}
	if filter.Language != "" {
		language, ok := normalizeFilterLanguage(filter.Language)
		if !ok || model.NormalizeLanguage(doc.Language) != language {
			return false
		}
	}
	if filter.Category != "" && !strings.EqualFold(doc.Category, filter.Category) {
		return false
	}
	if !dateInRange(doc.EffectiveDate, filter.EffectiveFrom, filter.EffectiveTo) {
		return false
	}
	if !dateInRange(doc.PublishedDate, filter.PublishedFrom, filter.PublishedTo) {
		return false
	}
	return true
}

func normalizeFilterLanguage(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "_", "-")))
	switch value {
	case "ko", "kor", "korean", "ko-kr":
		return model.LanguageKorean, true
	case "en", "eng", "english", "en-us", "en-gb":
		return model.LanguageEnglish, true
	default:
		return "", false
	}
}

func dateInRange(value, from, to string) bool {
	if value == "" || (from == "" && to == "") {
		return true
	}
	if from != "" && value < from {
		return false
	}
	if to != "" && value > to {
		return false
	}
	return true
}

func docSortDate(doc model.Document) time.Time {
	for _, value := range []string{doc.EffectiveDate, doc.PublishedDate} {
		if value == "" {
			continue
		}
		if t, err := time.Parse("2006-01-02", value); err == nil {
			return t
		}
	}
	return doc.CollectedAt
}

func countTokens(tokens []string) map[string]int {
	out := make(map[string]int, len(tokens))
	for _, tok := range tokens {
		out[tok]++
	}
	return out
}

func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, an, bn float64
	for i := range a {
		dot += a[i] * b[i]
		an += a[i] * a[i]
		bn += b[i] * b[i]
	}
	if an == 0 || bn == 0 {
		return 0
	}
	return dot / (math.Sqrt(an) * math.Sqrt(bn))
}

func mapValues(m map[string]SearchResult) []SearchResult {
	out := make([]SearchResult, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func trim(results []SearchResult, limit int) []SearchResult {
	if limit <= 0 || limit > len(results) {
		return results
	}
	return results[:limit]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
