package index

import (
	"container/heap"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/model"
)

const (
	evidenceOriginalCoverageWeight  = 0.016
	evidenceExpansionCoverageWeight = 0.012
	evidencePhraseCoverageWeight    = 0.020
	evidenceAttachmentPhraseWeight  = 0.016
	evidenceHeadingPhraseWeight     = 0.032
	evidenceChannelAgreementWeight  = 0.002
	evidenceClaimCoverageWeight     = 0.048
	documentIntentTermWeight        = 0.008
	documentIntentPhraseWeight      = 0.008
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
	Query         string
	OriginalQuery string

	Limit                int
	Filter               Filter
	QueryVector          []float64
	TokenWeights         map[string]float64
	EvidenceTokenWeights map[string]float64
	EvidenceTerms        []string
	EvidenceClaims       []string
	EvidenceLimit        int
}

type SearchResult struct {
	ID                string             `json:"id"`
	Title             string             `json:"title"`
	Category          string             `json:"category,omitempty"`
	DocumentType      model.DocumentType `json:"document_type"`
	Language          string             `json:"language"`
	SourceID          string             `json:"source_id,omitempty"`
	SourceURL         string             `json:"source_url"`
	EffectiveDate     string             `json:"effective_date,omitempty"`
	PublishedDate     string             `json:"published_date,omitempty"`
	Score             float64            `json:"score"`
	BM25Score         float64            `json:"bm25_score,omitempty"`
	VectorScore       float64            `json:"vector_score,omitempty"`
	Snippet           string             `json:"snippet,omitempty"`
	MatchedSource     string             `json:"matched_source,omitempty"`
	MatchedChunkID    string             `json:"matched_chunk_id,omitempty"`
	MatchedChunkIndex int                `json:"matched_chunk_index,omitempty"`
	ArticleID         string             `json:"article_id,omitempty"`
	HeadingPath       []string           `json:"heading_path,omitempty"`
	AttachmentMatches []AttachmentMatch  `json:"attachment_matches,omitempty"`
	EvidenceMatches   []EvidenceMatch    `json:"evidence_matches,omitempty"`

	FormulaNotice *model.FormulaNotice `json:"formula_notice,omitempty"`
	URI           string               `json:"uri"`
}

// ChunkCandidate is one channel's score for one concrete evidence chunk.
// Retrieval and fusion stay at this granularity until documents are grouped.
type ChunkCandidate struct {
	ChunkID          string
	DocumentID       string
	ChunkIndex       int
	Source           string
	AttachmentID     string
	AttachmentTitle  string
	AttachmentFile   string
	AttachmentStatus model.AttachmentStatus
	ArticleID        string
	HeadingPath      []string
	Text             string
	Score            float64
	BM25Score        float64
	VectorScore      float64
	FusedScore       float64
	MetadataScore    float64
	LexicalCoverage  float64
	BM25Rank         int
	VectorRank       int
}

// EvidenceMatch is a ranked, context-addressable reason why a document was
// returned. SearchResult.MatchedChunkID always mirrors the first match.
type EvidenceMatch struct {
	ChunkID          string                 `json:"chunk_id"`
	ChunkIndex       int                    `json:"chunk_index"`
	Source           string                 `json:"source"`
	AttachmentID     string                 `json:"attachment_id,omitempty"`
	AttachmentTitle  string                 `json:"attachment_title,omitempty"`
	AttachmentFile   string                 `json:"attachment_file,omitempty"`
	AttachmentStatus model.AttachmentStatus `json:"attachment_status,omitempty"`
	Score            float64                `json:"score"`
	BM25Score        float64                `json:"bm25_score,omitempty"`
	VectorScore      float64                `json:"vector_score,omitempty"`
	LexicalCoverage  float64                `json:"lexical_coverage"`
	Snippet          string                 `json:"snippet,omitempty"`
	Text             string                 `json:"-"`
	ArticleID        string                 `json:"article_id,omitempty"`
	HeadingPath      []string               `json:"heading_path,omitempty"`
}

type AttachmentMatch struct {
	ID            string                 `json:"id"`
	Title         string                 `json:"title"`
	FileName      string                 `json:"file_name,omitempty"`
	URI           string                 `json:"uri"`
	Status        model.AttachmentStatus `json:"status,omitempty"`
	ChunkID       string                 `json:"chunk_id,omitempty"`
	ChunkIndex    int                    `json:"chunk_index,omitempty"`
	Score         float64                `json:"score,omitempty"`
	Snippet       string                 `json:"snippet,omitempty"`
	ArticleID     string                 `json:"article_id,omitempty"`
	HeadingPath   []string               `json:"heading_path,omitempty"`
	FormulaNotice *model.FormulaNotice   `json:"formula_notice,omitempty"`
}

type ChunkContext struct {
	ID               string                 `json:"id"`
	DocumentID       string                 `json:"document_id"`
	Index            int                    `json:"index"`
	Source           string                 `json:"source"`
	URI              string                 `json:"uri"`
	AttachmentID     string                 `json:"attachment_id,omitempty"`
	AttachmentTitle  string                 `json:"attachment_title,omitempty"`
	AttachmentFile   string                 `json:"attachment_file,omitempty"`
	AttachmentStatus model.AttachmentStatus `json:"attachment_status,omitempty"`
	ArticleID        string                 `json:"article_id,omitempty"`
	HeadingPath      []string               `json:"heading_path,omitempty"`
	Text             string                 `json:"text"`
}

type Engine struct {
	docs         map[string]model.Document
	chunks       []chunk
	chunkByID    map[string]int
	chunkGroups  map[string][]int
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
	ArticleID        string                 `json:"article_id,omitempty"`
	HeadingPath      []string               `json:"heading_path,omitempty"`
	Tokens           []string               `json:"tokens"`
	Vector           []float64              `json:"vector,omitempty"`
	tokenMap         map[string]int
	vectorNorm       float64
}

func BuildWithAttachments(docs []model.Document, attachments map[string]AttachmentDocument, vectors map[string][]float64) *Engine {
	e := &Engine{
		docs:        make(map[string]model.Document, len(docs)),
		chunkByID:   map[string]int{},
		chunkGroups: map[string][]int{},
		df:          map[string]int{},
	}
	var totalLen int
	for _, doc := range docs {
		e.docs[doc.ID] = doc
		if doc.IsSearchable() {
			searchBody := model.AssetSearchText(doc.Body, doc.Assets)
			parts := ChunkTextWithAnchors(searchBody, 1600)
			if len(parts) == 0 {
				parts = []AnchoredChunk{{Text: doc.Title}}
			}
			for i, part := range parts {
				id := doc.ID + "#" + itoa(i)
				c := chunk{
					ID:          id,
					DocID:       doc.ID,
					Index:       i,
					Source:      "body",
					Text:        part.Text,
					ArticleID:   part.ArticleID,
					HeadingPath: append([]string(nil), part.HeadingPath...),
					Vector:      vectors[id],
				}
				totalLen += e.addChunk(c, strings.Join(part.HeadingPath, " ")+" "+part.Text)
			}
		}
		for _, att := range doc.Attachments {
			if !att.IsSearchable() {
				continue
			}
			attDoc, ok := attachments[att.ID]
			if !ok || strings.TrimSpace(attDoc.Text) == "" {
				continue
			}
			searchText := model.AssetSearchText(attDoc.Text, att.Assets)
			parts := ChunkTextWithAnchors(searchText, 1600)
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
					Text:             part.Text,
					ArticleID:        part.ArticleID,
					HeadingPath:      append([]string(nil), part.HeadingPath...),
					Vector:           vectors[id],
				}
				totalLen += e.addChunk(c, strings.Join(part.HeadingPath, " ")+" "+part.Text)
			}
		}
	}
	if len(e.chunks) > 0 {
		e.avgDocLength = float64(totalLen) / float64(len(e.chunks))
	}
	return e
}

func (e *Engine) addChunk(c chunk, tokenText string) int {
	tokens := indexTokenize(tokenText)
	c.Tokens = tokens
	c.tokenMap = countTokens(tokens)
	if len(c.Vector) > 0 {
		c.vectorNorm = finiteVectorNorm(c.Vector)
	}
	seen := map[string]struct{}{}
	for _, tok := range tokens {
		if _, ok := seen[tok]; ok {
			continue
		}
		seen[tok] = struct{}{}
		e.df[tok]++
	}
	index := len(e.chunks)
	e.chunks = append(e.chunks, c)
	e.chunkByID[c.ID] = index
	key := chunkGroupKey(c)
	e.chunkGroups[key] = append(e.chunkGroups[key], index)
	return len(tokens)
}

func chunkGroupKey(c chunk) string {
	if c.Source == "attachment" {
		return c.DocID + "\x00" + c.Source + "\x00" + c.AttachmentID
	}
	return c.DocID + "\x00" + c.Source
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
	if opts.Limit <= 0 {
		opts.Limit = 10
	} else if opts.Limit > 50 {
		opts.Limit = 50
	}
	originalQuery := strings.TrimSpace(opts.OriginalQuery)
	if originalQuery == "" {
		originalQuery = opts.Query
	}
	queryTokens := Tokenize(opts.Query)
	originalTerms := meaningfulQueryTerms(originalQuery)
	candidateLimit := opts.Limit * 24
	if candidateLimit < 64 {
		candidateLimit = 64
	}
	if candidateLimit > 512 {
		candidateLimit = 512
	}
	bm25 := e.bm25ChunkCandidates(queryTokens, originalTerms, opts.Filter, opts.TokenWeights, candidateLimit)
	vector := e.vectorChunkCandidates(opts.QueryVector, originalTerms, opts.Filter, candidateLimit)
	fused := fuseChunkCandidates(bm25, vector)
	metadata := e.metadataDocumentScores(queryTokens, opts.Filter, opts.TokenWeights)
	return e.groupChunkCandidates(opts, fused, metadata)
}

func (e *Engine) Documents(filter Filter, limit, offset int) []model.Document {
	docs, _ := e.DocumentsPage(filter, limit, offset)
	return docs
}

func (e *Engine) DocumentsPage(filter Filter, limit, offset int) ([]model.Document, int) {
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	var docs []model.Document
	for _, doc := range e.docs {
		if matchesFilter(doc, filter) {
			docs = append(docs, doc)
		}
	}
	sort.SliceStable(docs, func(i, j int) bool { return docLess(docs[i], docs[j]) })
	total := len(docs)
	if offset >= len(docs) {
		return nil, total
	}
	end := offset + limit
	if end > len(docs) {
		end = len(docs)
	}
	return docs[offset:end], total
}

func (e *Engine) Recent(limit int, typ model.DocumentType, language string) []model.Document {
	return e.Documents(Filter{DocumentType: typ, Language: language}, limit, 0)
}

func (e *Engine) Categories(language string) []string {
	seen := map[string]struct{}{}
	for _, doc := range e.docs {
		if language != "" {
			normalized, ok := normalizeFilterLanguage(language)
			if !ok || model.NormalizeLanguage(doc.Language) != normalized {
				continue
			}
		}
		category := strings.TrimSpace(doc.Category)
		if category == "" {
			continue
		}
		seen[category] = struct{}{}
	}
	categories := make([]string, 0, len(seen))
	for category := range seen {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	return categories
}

func (e *Engine) Document(id string) (model.Document, bool) {
	doc, ok := e.docs[id]
	return doc, ok
}

func (e *Engine) ContextAround(chunkID string, before, after int) (model.Document, []ChunkContext, bool) {
	if before < 0 {
		before = 0
	}
	if after < 0 {
		after = 0
	}
	targetIndex, found := e.chunkByID[chunkID]
	if !found || targetIndex < 0 || targetIndex >= len(e.chunks) {
		return model.Document{}, nil, false
	}
	target := e.chunks[targetIndex]
	doc, ok := e.docs[target.DocID]
	if !ok {
		return model.Document{}, nil, false
	}
	group := append([]int(nil), e.chunkGroups[chunkGroupKey(target)]...)
	sort.Slice(group, func(i, j int) bool { return e.chunks[group[i]].Index < e.chunks[group[j]].Index })
	pos := -1
	for i, index := range group {
		if e.chunks[index].ID == chunkID {
			pos = i
			break
		}
	}
	if pos < 0 {
		return model.Document{}, nil, false
	}
	start := pos - before
	if start < 0 {
		start = 0
	}
	end := pos + after + 1
	if end > len(group) {
		end = len(group)
	}
	contexts := make([]ChunkContext, 0, end-start)
	for _, index := range group[start:end] {
		contexts = append(contexts, chunkContext(doc, e.chunks[index]))
	}
	return doc, contexts, true
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

func (e *Engine) bm25ChunkCandidates(queryTokens, originalTerms []string, filter Filter, tokenWeights map[string]float64, limit int) []ChunkCandidate {
	if len(queryTokens) == 0 || len(e.chunks) == 0 || limit <= 0 {
		return nil
	}
	top := &chunkCandidateHeap{}
	allowedDocuments := e.matchingDocumentIDs(filter)
	heap.Init(top)
	const (
		k1 = 1.4
		b  = 0.75
	)
	for _, c := range e.chunks {
		if _, ok := allowedDocuments[c.DocID]; !ok {
			continue
		}
		var score float64
		for _, tok := range queryTokens {
			tf := float64(c.tokenMap[tok])
			if tf == 0 {
				continue
			}
			weight := tokenWeights[tok]
			if weight <= 0 {
				weight = 1
			}
			idf := math.Log(1 + (float64(len(e.chunks))-float64(e.df[tok])+0.5)/(float64(e.df[tok])+0.5))
			denom := tf + k1*(1-b+b*float64(len(c.Tokens))/e.avgDocLength)
			score += weight * idf * (tf * (k1 + 1) / denom)
		}
		if score <= 0 {
			continue
		}
		candidate := chunkCandidate(c)
		candidate.Score = score
		candidate.BM25Score = score
		pushChunkCandidate(top, candidate, limit)
	}
	return rankCoveredCandidates(top.items, originalTerms)
}

func (e *Engine) vectorChunkCandidates(queryVector []float64, originalTerms []string, filter Filter, limit int) []ChunkCandidate {
	if !e.validQueryVector(queryVector) || limit <= 0 {
		return nil
	}
	top := &chunkCandidateHeap{}
	allowedDocuments := e.matchingDocumentIDs(filter)
	queryNorm := finiteVectorNorm(queryVector)
	if queryNorm == 0 {
		return nil
	}
	heap.Init(top)
	for _, c := range e.chunks {
		if len(c.Vector) == 0 {
			continue
		}
		if _, ok := allowedDocuments[c.DocID]; !ok {
			continue
		}
		score := cosineWithNorm(queryVector, queryNorm, c.Vector, c.vectorNorm)
		if score <= 0 {
			continue
		}
		candidate := chunkCandidate(c)
		candidate.Score = score
		candidate.VectorScore = score
		pushChunkCandidate(top, candidate, limit)
	}
	return rankCoveredCandidates(top.items, originalTerms)
}

func (e *Engine) matchingDocumentIDs(filter Filter) map[string]struct{} {
	allowed := make(map[string]struct{}, len(e.docs))
	for id, document := range e.docs {
		if matchesFilter(document, filter) {
			allowed[id] = struct{}{}
		}
	}
	return allowed
}

func rankCoveredCandidates(candidates []ChunkCandidate, originalTerms []string) []ChunkCandidate {
	out := append([]ChunkCandidate(nil), candidates...)
	for index := range out {
		out[index].LexicalCoverage = termCoverage(originalTerms, out[index].Text+" "+strings.Join(out[index].HeadingPath, " "))
	}
	sort.Slice(out, func(i, j int) bool { return chunkCandidateBetter(out[i], out[j]) })
	return out
}

func (e *Engine) validQueryVector(vector []float64) bool {
	return e.ValidateQueryVector(vector) == nil
}

// ValidateQueryVector verifies that a query vector is safe and dimensionally
// compatible with the vectors loaded by this engine. Callers can use it to
// reject invalid embedding responses instead of silently falling back to BM25.
func (e *Engine) ValidateQueryVector(vector []float64) error {
	if len(vector) == 0 {
		return fmt.Errorf("query vector is empty")
	}
	expectedDimensions := 0
	for _, chunk := range e.chunks {
		if len(chunk.Vector) > 0 {
			expectedDimensions = len(chunk.Vector)
			break
		}
	}
	if expectedDimensions == 0 {
		return fmt.Errorf("engine has no loaded vectors")
	}
	if len(vector) != expectedDimensions {
		return fmt.Errorf("query vector dimensions %d do not match loaded vector dimensions %d", len(vector), expectedDimensions)
	}
	if err := validateFiniteNonZeroFloat32Vector(vector); err != nil {
		return fmt.Errorf("query vector %w", err)
	}
	return nil
}

type documentMetadataScore struct {
	DocumentID string
	Score      float64
}

func (e *Engine) metadataDocumentScores(queryTokens []string, filter Filter, tokenWeights map[string]float64) []documentMetadataScore {
	scores := make([]documentMetadataScore, 0)
	for _, doc := range e.docs {
		if !matchesFilter(doc, filter) {
			continue
		}
		indexed := countTokens(Tokenize(doc.Title + " " + doc.Category))
		var score float64
		for _, token := range queryTokens {
			if indexed[token] == 0 {
				continue
			}
			weight := tokenWeights[token]
			if weight <= 0 {
				weight = 1
			}
			score += weight
		}
		if score > 0 {
			scores = append(scores, documentMetadataScore{DocumentID: doc.ID, Score: score})
		}
	}
	sort.Slice(scores, func(i, j int) bool {
		if !sameScore(scores[i].Score, scores[j].Score) {
			return scores[i].Score > scores[j].Score
		}
		return docLess(e.docs[scores[i].DocumentID], e.docs[scores[j].DocumentID])
	})
	return scores
}

func fuseChunkCandidates(bm25, vector []ChunkCandidate) []ChunkCandidate {
	fused := make(map[string]ChunkCandidate, len(bm25)+len(vector))
	for rank, candidate := range bm25 {
		current := fused[candidate.ChunkID]
		if current.ChunkID == "" {
			current = candidate
		}
		current.BM25Score = candidate.BM25Score
		current.BM25Rank = rank + 1
		current.LexicalCoverage = candidate.LexicalCoverage
		current.FusedScore += 1 / (60 + float64(rank+1))
		fused[candidate.ChunkID] = current
	}
	for rank, candidate := range vector {
		current := fused[candidate.ChunkID]
		if current.ChunkID == "" {
			current = candidate
		}
		current.VectorScore = candidate.VectorScore
		current.VectorRank = rank + 1
		if candidate.LexicalCoverage > current.LexicalCoverage {
			current.LexicalCoverage = candidate.LexicalCoverage
		}
		current.FusedScore += 1 / (60 + float64(rank+1))
		fused[candidate.ChunkID] = current
	}
	out := make([]ChunkCandidate, 0, len(fused))
	for _, candidate := range fused {
		candidate.Score = candidate.FusedScore
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool { return chunkCandidateBetter(out[i], out[j]) })
	return out
}

func (e *Engine) groupChunkCandidates(opts SearchOptions, fused []ChunkCandidate, metadata []documentMetadataScore) []SearchResult {
	type documentAggregate struct {
		candidates    []ChunkCandidate
		metadataScore float64
	}
	aggregates := make(map[string]*documentAggregate)
	for _, candidate := range fused {
		aggregate := aggregates[candidate.DocumentID]
		if aggregate == nil {
			aggregate = &documentAggregate{}
			aggregates[candidate.DocumentID] = aggregate
		}
		aggregate.candidates = append(aggregate.candidates, candidate)
	}
	for rank, item := range metadata {
		aggregate := aggregates[item.DocumentID]
		if aggregate == nil {
			aggregate = &documentAggregate{}
			aggregates[item.DocumentID] = aggregate
		}
		aggregate.metadataScore = 0.35 / (60 + float64(rank+1))
	}

	results := make([]SearchResult, 0, len(aggregates))
	query := opts.OriginalQuery
	if strings.TrimSpace(query) == "" {
		query = opts.Query
	}
	for documentID, aggregate := range aggregates {
		doc := e.docs[documentID]
		result := resultFromDoc(doc)
		evidenceLimit := opts.EvidenceLimit
		if evidenceLimit <= 0 {
			evidenceLimit = 3
		} else if evidenceLimit > 20 {
			evidenceLimit = 20
		}
		retrievalSelected := selectEvidenceCandidates(aggregate.candidates, 3)
		for rank, candidate := range retrievalSelected {
			switch rank {
			case 0:
				result.Score = candidate.FusedScore
			case 1:
				result.Score += 0.20 * candidate.FusedScore
			case 2:
				result.Score += 0.10 * candidate.FusedScore
			}
		}
		selected := selectExpandedEvidenceCandidates(
			aggregate.candidates, evidenceLimit, opts.EvidenceTokenWeights, opts.EvidenceTerms, opts.EvidenceClaims,
		)
		matches := make([]EvidenceMatch, 0, len(selected))
		for _, candidate := range selected {
			matches = append(matches, evidenceMatch(candidate, query))
		}
		SetSearchResultEvidence(&result, matches)
		if len(selected) > 0 {
			result.Score += documentIntentTermWeight*expandedTermCoverage(selected[0], opts.EvidenceTokenWeights) +
				documentIntentPhraseWeight*expandedPhraseCoverage(selected[0], opts.EvidenceTerms)
		}
		result.Score += aggregate.metadataScore
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool { return e.searchResultLess(results[i], results[j]) })
	return trim(results, opts.Limit)
}

func selectEvidenceCandidates(candidates []ChunkCandidate, limit int) []ChunkCandidate {
	return selectRankedEvidenceCandidates(candidates, limit, chunkCandidateBetter)
}

func selectExpandedEvidenceCandidates(
	candidates []ChunkCandidate,
	limit int,
	tokenWeights map[string]float64,
	evidenceTerms []string,
	evidenceClaims []string,
) []ChunkCandidate {
	ranked := append([]ChunkCandidate(nil), candidates...)
	for index := range ranked {
		candidate := &ranked[index]
		candidate.Score = candidate.FusedScore +
			evidenceOriginalCoverageWeight*candidate.LexicalCoverage +
			evidenceExpansionCoverageWeight*expandedTermCoverage(*candidate, tokenWeights) +
			evidencePhraseCoverageWeight*expandedPhraseCoverage(*candidate, evidenceTerms) +
			evidenceClaimCoverageWeight*evidenceClaimCoverage(*candidate, evidenceClaims)
		if candidate.BM25Score > 0 && candidate.VectorScore > 0 {
			candidate.Score += evidenceChannelAgreementWeight
		}
		candidate.Score += evidenceHeadingPhraseWeight * expandedHeadingPhraseCoverage(*candidate, evidenceTerms)
		if candidate.AttachmentID != "" {
			candidate.Score += evidenceAttachmentPhraseWeight * expandedPhraseCoverage(*candidate, evidenceTerms)
		}
	}
	return selectRankedEvidenceCandidates(ranked, limit, func(left, right ChunkCandidate) bool {
		if !sameScore(left.Score, right.Score) {
			return left.Score > right.Score
		}
		return chunkCandidateBetter(left, right)
	})
}

func evidenceClaimCoverage(candidate ChunkCandidate, claims []string) float64 {
	if len(claims) == 0 {
		return 0
	}
	evidence := normalizeClaimText(candidate.Text + " " + strings.Join(candidate.HeadingPath, " "))
	matched := 0
	for _, claim := range claims {
		if strings.Contains(evidence, normalizeClaimText(claim)) {
			matched++
		}
	}
	return float64(matched) / float64(len(claims))
}

func selectRankedEvidenceCandidates(candidates []ChunkCandidate, limit int, better func(ChunkCandidate, ChunkCandidate) bool) []ChunkCandidate {
	if limit <= 0 || len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool { return better(candidates[i], candidates[j]) })
	selected := make([]ChunkCandidate, 0, limit)
	ownerCounts := map[string]int{}
	for _, candidate := range candidates {
		owner := evidenceOwner(candidate)
		if ownerCounts[owner] >= 2 {
			continue
		}
		duplicate := false
		for _, existing := range selected {
			if candidate.ChunkID == existing.ChunkID || evidenceNearDuplicate(candidate, existing) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		selected = append(selected, candidate)
		ownerCounts[owner]++
		if len(selected) == limit {
			break
		}
	}
	return selected
}

func expandedTermCoverage(candidate ChunkCandidate, tokenWeights map[string]float64) float64 {
	text := candidate.Text + " " + strings.Join(candidate.HeadingPath, " ") + " " + candidate.AttachmentTitle
	tokens := countTokens(indexTokenize(text))
	var matched, total float64
	for token, weight := range tokenWeights {
		if weight <= 0 || weight >= 1 {
			continue
		}
		total += weight
		if tokens[token] > 0 {
			matched += weight
		}
	}
	if total == 0 {
		return 0
	}
	return matched / total
}
func expandedPhraseCoverage(candidate ChunkCandidate, terms []string) float64 {
	if len(terms) == 0 {
		return 0
	}
	text := normalizeEvidencePhrase(candidate.Text + " " + strings.Join(candidate.HeadingPath, " ") + " " + candidate.AttachmentTitle)
	longest := 0
	longestMatched := 0
	for _, term := range terms {
		normalized := normalizeEvidencePhrase(term)
		if len(normalized) < 3 {
			continue
		}
		if len(normalized) > longest {
			longest = len(normalized)
		}
		if strings.Contains(text, normalized) && len(normalized) > longestMatched {
			longestMatched = len(normalized)
		}
	}
	if longest == 0 {
		return 0
	}
	return float64(longestMatched) / float64(longest)
}
func expandedHeadingPhraseCoverage(candidate ChunkCandidate, terms []string) float64 {
	candidate.Text = ""
	candidate.AttachmentTitle = ""
	return expandedPhraseCoverage(candidate, terms)
}

func normalizeEvidencePhrase(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), ""))
}

func evidenceOwner(candidate ChunkCandidate) string {
	if candidate.AttachmentID != "" {
		return candidate.DocumentID + "\x00attachment\x00" + candidate.AttachmentID + "\x00" + candidate.ArticleID
	}
	if candidate.ArticleID != "" {
		return candidate.DocumentID + "\x00article\x00" + candidate.ArticleID
	}
	return candidate.DocumentID + "\x00heading\x00" + strings.Join(candidate.HeadingPath, "\x00")
}

func evidenceNearDuplicate(left, right ChunkCandidate) bool {
	if evidenceOwner(left) != evidenceOwner(right) {
		return false
	}
	delta := left.ChunkIndex - right.ChunkIndex
	if delta < 0 {
		delta = -delta
	}
	return delta <= 1 || textTokenJaccard(left.Text, right.Text) >= 0.75
}

func textTokenJaccard(left, right string) float64 {
	leftTokens := countTokens(Tokenize(left))
	rightTokens := countTokens(Tokenize(right))
	if len(leftTokens) == 0 || len(rightTokens) == 0 {
		return 0
	}
	intersection := 0
	for token := range leftTokens {
		if rightTokens[token] > 0 {
			intersection++
		}
	}
	union := len(leftTokens) + len(rightTokens) - intersection
	return float64(intersection) / float64(union)
}

func evidenceMatch(candidate ChunkCandidate, query string) EvidenceMatch {
	snippet := Snippet(candidate.Text, query, 300)
	if candidate.Source == "attachment" {
		snippet = "첨부 " + candidate.AttachmentTitle + ": " + snippet
	}
	return EvidenceMatch{
		ChunkID:          candidate.ChunkID,
		ChunkIndex:       candidate.ChunkIndex,
		Source:           candidate.Source,
		AttachmentID:     candidate.AttachmentID,
		AttachmentTitle:  candidate.AttachmentTitle,
		AttachmentFile:   candidate.AttachmentFile,
		AttachmentStatus: candidate.AttachmentStatus,
		Score:            candidate.Score,
		BM25Score:        candidate.BM25Score,
		VectorScore:      candidate.VectorScore,
		LexicalCoverage:  candidate.LexicalCoverage,
		Snippet:          snippet,
		Text:             candidate.Text,
		ArticleID:        candidate.ArticleID,
		HeadingPath:      append([]string(nil), candidate.HeadingPath...),
	}
}

func attachmentMatchFromEvidence(match EvidenceMatch) AttachmentMatch {
	return AttachmentMatch{
		ID:          match.AttachmentID,
		Title:       match.AttachmentTitle,
		FileName:    match.AttachmentFile,
		URI:         "krx-rule://attachments/" + match.AttachmentID,
		Status:      match.AttachmentStatus,
		ChunkID:     match.ChunkID,
		ChunkIndex:  match.ChunkIndex,
		Score:       match.Score,
		Snippet:     match.Snippet,
		ArticleID:   match.ArticleID,
		HeadingPath: append([]string(nil), match.HeadingPath...),
	}
}

// SetSearchResultEvidence replaces the externally visible evidence order and
// keeps every mirrored primary/attachment field consistent with that order.
func SetSearchResultEvidence(result *SearchResult, matches []EvidenceMatch) {
	if result == nil {
		return
	}
	result.EvidenceMatches = append(result.EvidenceMatches[:0], matches...)
	result.Snippet = ""
	result.MatchedSource = ""
	result.MatchedChunkID = ""
	result.MatchedChunkIndex = 0
	result.ArticleID = ""
	result.HeadingPath = nil
	result.AttachmentMatches = nil
	result.BM25Score = 0
	result.VectorScore = 0
	for index, match := range result.EvidenceMatches {
		if index == 0 {
			result.Snippet = match.Snippet
			result.MatchedSource = match.Source
			result.MatchedChunkID = match.ChunkID
			result.MatchedChunkIndex = match.ChunkIndex
			result.ArticleID = match.ArticleID
			result.HeadingPath = append([]string(nil), match.HeadingPath...)
		}
		if match.BM25Score > result.BM25Score {
			result.BM25Score = match.BM25Score
		}
		if match.VectorScore > result.VectorScore {
			result.VectorScore = match.VectorScore
		}
		if match.Source == "attachment" {
			result.AttachmentMatches = mergeAttachmentMatch(result.AttachmentMatches, attachmentMatchFromEvidence(match))
		}
	}
}

func chunkCandidate(c chunk) ChunkCandidate {
	return ChunkCandidate{
		ChunkID:          c.ID,
		DocumentID:       c.DocID,
		ChunkIndex:       c.Index,
		Source:           c.Source,
		AttachmentID:     c.AttachmentID,
		AttachmentTitle:  c.AttachmentTitle,
		AttachmentFile:   c.AttachmentFile,
		AttachmentStatus: c.AttachmentStatus,
		ArticleID:        c.ArticleID,
		HeadingPath:      append([]string(nil), c.HeadingPath...),
		Text:             c.Text,
	}
}

type chunkCandidateHeap struct {
	items []ChunkCandidate
}

func (h chunkCandidateHeap) Len() int { return len(h.items) }
func (h chunkCandidateHeap) Less(i, j int) bool {
	return chunkCandidateWorse(h.items[i], h.items[j])
}
func (h chunkCandidateHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *chunkCandidateHeap) Push(value any) {
	h.items = append(h.items, value.(ChunkCandidate))
}
func (h *chunkCandidateHeap) Pop() any {
	last := len(h.items) - 1
	value := h.items[last]
	h.items = h.items[:last]
	return value
}

func pushChunkCandidate(top *chunkCandidateHeap, candidate ChunkCandidate, limit int) {
	if top.Len() < limit {
		heap.Push(top, candidate)
		return
	}
	if chunkCandidateBetter(candidate, top.items[0]) {
		top.items[0] = candidate
		heap.Fix(top, 0)
	}
}

func sortedChunkCandidates(candidates []ChunkCandidate) []ChunkCandidate {
	out := append([]ChunkCandidate(nil), candidates...)
	sort.Slice(out, func(i, j int) bool { return chunkCandidateBetter(out[i], out[j]) })
	return out
}

func chunkCandidateBetter(left, right ChunkCandidate) bool {
	if !sameScore(left.Score, right.Score) {
		return left.Score > right.Score
	}
	if !sameScore(left.LexicalCoverage, right.LexicalCoverage) {
		return left.LexicalCoverage > right.LexicalCoverage
	}
	leftStructured := left.ArticleID != "" || left.AttachmentID != ""
	rightStructured := right.ArticleID != "" || right.AttachmentID != ""
	if leftStructured != rightStructured {
		return leftStructured
	}
	return left.ChunkID < right.ChunkID
}

func chunkCandidateWorse(left, right ChunkCandidate) bool {
	return chunkCandidateBetter(right, left)
}

func meaningfulQueryTerms(query string) []string {
	raw := tokenPattern.FindAllString(strings.ToLower(query), -1)
	seen := map[string]struct{}{}
	terms := make([]string, 0, len(raw))
	for _, term := range raw {
		term = normalizeMeaningfulQueryTerm(term)
		if _, stop := queryStopWords[term]; stop || runeLen(term) < 2 {
			continue
		}
		if _, exists := seen[term]; exists {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	return terms
}

func normalizeMeaningfulQueryTerm(term string) string {
	for _, suffix := range []string{
		"에서도", "에서는", "에게서", "에서", "에게", "에는", "으로", "까지", "부터", "처럼",
		"은", "는", "이", "가", "을", "를", "의", "에", "도",
	} {
		if !strings.HasSuffix(term, suffix) {
			continue
		}
		base := strings.TrimSuffix(term, suffix)
		if runeLen(base) >= 2 {
			return base
		}
	}
	return term
}

var queryStopWords = map[string]struct{}{
	"krx": {}, "규정": {}, "규정상": {}, "근거": {}, "관련": {}, "대한": {},
	"무엇": {}, "어떤": {}, "얼마": {}, "있는가": {}, "없는가": {}, "하나": {},
	"해야": {}, "하는가": {}, "되나": {}, "인가": {}, "때": {}, "경우": {},
	"아무": {}, "너무": {}, "하지": {}, "않아도": {}, "되는가": {}, "가능한가": {},
	"써도": {}, "해도": {},
}

func termCoverage(terms []string, text string) float64 {
	if len(terms) == 0 {
		return 0
	}
	text = strings.ToLower(text)
	matched := 0
	for _, term := range terms {
		if strings.Contains(text, term) {
			matched++
		}
	}
	return float64(matched) / float64(len(terms))
}

func (e *Engine) searchResultLess(a, b SearchResult) bool {
	if !sameScore(a.Score, b.Score) {
		return a.Score > b.Score
	}
	if !sameScore(a.BM25Score, b.BM25Score) {
		return a.BM25Score > b.BM25Score
	}
	if !sameScore(a.VectorScore, b.VectorScore) {
		return a.VectorScore > b.VectorScore
	}
	return docLess(e.docs[a.ID], e.docs[b.ID])
}

func sameScore(a, b float64) bool {
	return math.Abs(a-b) < 1e-12
}

func attachmentMatch(c chunk, score float64, snippet string) AttachmentMatch {
	return AttachmentMatch{
		ID:          c.AttachmentID,
		Title:       c.AttachmentTitle,
		FileName:    c.AttachmentFile,
		URI:         "krx-rule://attachments/" + c.AttachmentID,
		Status:      c.AttachmentStatus,
		ChunkID:     c.ID,
		ChunkIndex:  c.Index,
		Score:       score,
		Snippet:     snippet,
		ArticleID:   c.ArticleID,
		HeadingPath: append([]string(nil), c.HeadingPath...),
	}
}

func chunkContext(doc model.Document, c chunk) ChunkContext {
	uri := doc.URI()
	if c.Source == "attachment" && c.AttachmentID != "" {
		uri = "krx-rule://attachments/" + c.AttachmentID
	}
	return ChunkContext{
		ID:               c.ID,
		DocumentID:       c.DocID,
		Index:            c.Index,
		Source:           c.Source,
		URI:              uri,
		AttachmentID:     c.AttachmentID,
		AttachmentTitle:  c.AttachmentTitle,
		AttachmentFile:   c.AttachmentFile,
		AttachmentStatus: c.AttachmentStatus,
		ArticleID:        c.ArticleID,
		HeadingPath:      append([]string(nil), c.HeadingPath...),
		Text:             c.Text,
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

func docLess(a, b model.Document) bool {
	aDate := docSortDate(a)
	bDate := docSortDate(b)
	if !aDate.Equal(bDate) {
		return aDate.After(bDate)
	}
	return a.ID < b.ID
}

func countTokens(tokens []string) map[string]int {
	out := make(map[string]int, len(tokens))
	for _, tok := range tokens {
		out[tok]++
	}
	return out
}

func cosine(a, b []float64) float64 {
	return cosineWithNorm(a, finiteVectorNorm(a), b, finiteVectorNorm(b))
}

func finiteVectorNorm(vector []float64) float64 {
	var squared float64
	for _, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0
		}
		squared += value * value
	}
	if squared <= 0 || math.IsNaN(squared) || math.IsInf(squared, 0) {
		return 0
	}
	return math.Sqrt(squared)
}

func cosineWithNorm(a []float64, aNorm float64, b []float64, bNorm float64) float64 {
	if len(a) == 0 || len(a) != len(b) || aNorm == 0 || bNorm == 0 {
		return 0
	}
	var dot float64
	for index := range a {
		dot += a[index] * b[index]
	}
	score := dot / (aNorm * bNorm)
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0
	}
	return score
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
