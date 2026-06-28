package index

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/corpus"
	"github.com/chromato99/krx-rule-mcp/internal/model"
)

type Snapshot struct {
	Version      uint16
	GeneratedAt  string
	CorpusHash   string
	Documents    []SnapshotDocument
	AvgDocLength float64
	DF           map[string]int
	Chunks       []SnapshotChunk
}

type SnapshotDocument struct {
	ID          string
	ContentHash string
	IndexHash   string
}

type SnapshotChunk struct {
	ID               string
	DocID            string
	Index            int
	Text             string
	Source           string
	AttachmentID     string
	AttachmentTitle  string
	AttachmentFile   string
	AttachmentStatus model.AttachmentStatus
	Tokens           []string
	Vector           []float64
}

type VectorSnapshot struct {
	Version     uint16
	GeneratedAt string
	CorpusHash  string
	Model       string
	Dimensions  int
	Documents   []SnapshotDocument
	Vectors     []SnapshotVector
}

type SnapshotVector struct {
	ChunkID string
	Vector  []float64
}

type Repository struct {
	DataRoot    string
	IndexPath   string
	VectorPath  string
	Documents   map[string]model.Document
	Attachments map[string]AttachmentDocument
	Engine      *Engine
}

type AttachmentDocument struct {
	Attachment model.Attachment
	Text       string
}

var (
	indexSnapshotMagic  = []byte("KRXIDX2\n")
	vectorSnapshotMagic = []byte("KRXVEC2\n")
)

func LoadRepository(dataRoot, indexPath string, vectorIndexPaths ...string) (*Repository, error) {
	docs, err := corpus.LoadDocuments(dataRoot)
	if err != nil {
		return nil, fmt.Errorf("load markdown corpus: %w", err)
	}
	snap, err := LoadSnapshot(indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("BM25 index snapshot %q not found; run krx-rule-index --data-dir %s --index %s", indexPath, dataRoot, indexPath)
	}
	if err != nil {
		return nil, err
	}
	attachments := loadAttachments(dataRoot, docs)
	if !snapshotMatches(snap, docs, attachments) {
		return nil, fmt.Errorf("BM25 index snapshot %q does not match Markdown corpus; run krx-rule-index --data-dir %s --index %s", indexPath, dataRoot, indexPath)
	}
	vectors := map[string][]float64{}
	var vectorPath string
	for _, path := range vectorIndexPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		loaded, err := LoadVectorMap(path, docs, attachments)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for id, vector := range loaded {
			vectors[id] = vector
		}
		if len(loaded) > 0 && vectorPath == "" {
			vectorPath = path
		}
	}
	engine := engineFromSnapshot(docs, snap, vectors)
	repo := &Repository{
		DataRoot:    dataRoot,
		IndexPath:   indexPath,
		VectorPath:  vectorPath,
		Documents:   map[string]model.Document{},
		Attachments: attachments,
		Engine:      engine,
	}
	for _, doc := range docs {
		repo.Documents[doc.ID] = doc
	}
	return repo, nil
}

func LoadSnapshot(path string) (Snapshot, error) {
	if path == "" {
		return Snapshot{}, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	if !bytes.HasPrefix(data, indexSnapshotMagic) {
		return Snapshot{}, fmt.Errorf("read index snapshot: unsupported snapshot header")
	}
	payload, err := gunzip(data[len(indexSnapshotMagic):])
	if err != nil {
		return Snapshot{}, fmt.Errorf("read index snapshot: %w", err)
	}
	r := bytes.NewReader(payload)
	snap := Snapshot{DF: map[string]int{}}
	snap.Version, err = readU16(r)
	if err != nil {
		return Snapshot{}, err
	}
	if snap.Version != indexSnapshotFormatVersion {
		return Snapshot{}, fmt.Errorf("read index snapshot: unsupported snapshot version %d", snap.Version)
	}
	snap.GeneratedAt, err = readString(r)
	if err != nil {
		return Snapshot{}, err
	}
	snap.CorpusHash, err = readString(r)
	if err != nil {
		return Snapshot{}, err
	}
	docCount, err := readU32(r)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Documents = make([]SnapshotDocument, 0, docCount)
	for range docCount {
		doc, err := readSnapshotDocument(r)
		if err != nil {
			return Snapshot{}, err
		}
		snap.Documents = append(snap.Documents, doc)
	}
	if err := binary.Read(r, binary.BigEndian, &snap.AvgDocLength); err != nil {
		return Snapshot{}, err
	}
	dfCount, err := readU32(r)
	if err != nil {
		return Snapshot{}, err
	}
	for range dfCount {
		token, err := readString(r)
		if err != nil {
			return Snapshot{}, err
		}
		count, err := readU32(r)
		if err != nil {
			return Snapshot{}, err
		}
		snap.DF[token] = int(count)
	}
	chunkCount, err := readU32(r)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Chunks = make([]SnapshotChunk, 0, chunkCount)
	for range chunkCount {
		chunk, err := readSnapshotChunk(r)
		if err != nil {
			return Snapshot{}, err
		}
		snap.Chunks = append(snap.Chunks, chunk)
	}
	return snap, nil
}

func LoadVectorSnapshot(path string) (VectorSnapshot, error) {
	if path == "" {
		return VectorSnapshot{}, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return VectorSnapshot{}, err
	}
	if !bytes.HasPrefix(data, vectorSnapshotMagic) {
		return VectorSnapshot{}, fmt.Errorf("read vector snapshot: unsupported snapshot header")
	}
	payload, err := gunzip(data[len(vectorSnapshotMagic):])
	if err != nil {
		return VectorSnapshot{}, fmt.Errorf("read vector snapshot: %w", err)
	}
	r := bytes.NewReader(payload)
	snap := VectorSnapshot{}
	snap.Version, err = readU16(r)
	if err != nil {
		return VectorSnapshot{}, err
	}
	if snap.Version != vectorSnapshotFormatVersion {
		return VectorSnapshot{}, fmt.Errorf("read vector snapshot: unsupported snapshot version %d", snap.Version)
	}
	snap.GeneratedAt, err = readString(r)
	if err != nil {
		return VectorSnapshot{}, err
	}
	snap.CorpusHash, err = readString(r)
	if err != nil {
		return VectorSnapshot{}, err
	}
	snap.Model, err = readString(r)
	if err != nil {
		return VectorSnapshot{}, err
	}
	dimensions, err := readU32(r)
	if err != nil {
		return VectorSnapshot{}, err
	}
	snap.Dimensions = int(dimensions)
	docCount, err := readU32(r)
	if err != nil {
		return VectorSnapshot{}, err
	}
	snap.Documents = make([]SnapshotDocument, 0, docCount)
	for range docCount {
		doc, err := readSnapshotDocument(r)
		if err != nil {
			return VectorSnapshot{}, err
		}
		snap.Documents = append(snap.Documents, doc)
	}
	vectorCount, err := readU32(r)
	if err != nil {
		return VectorSnapshot{}, err
	}
	snap.Vectors = make([]SnapshotVector, 0, vectorCount)
	for range vectorCount {
		chunkID, err := readString(r)
		if err != nil {
			return VectorSnapshot{}, err
		}
		size, err := readU32(r)
		if err != nil {
			return VectorSnapshot{}, err
		}
		vector := make([]float64, 0, size)
		for range size {
			var value float32
			if err := binary.Read(r, binary.BigEndian, &value); err != nil {
				return VectorSnapshot{}, err
			}
			vector = append(vector, float64(value))
		}
		snap.Vectors = append(snap.Vectors, SnapshotVector{ChunkID: chunkID, Vector: vector})
	}
	return snap, nil
}

func LoadVectorMap(path string, docs []model.Document, attachments map[string]AttachmentDocument) (map[string][]float64, error) {
	snap, err := LoadVectorSnapshot(path)
	if err != nil {
		return nil, err
	}
	if snap.CorpusHash != corpusHash(docs, attachments) || !snapshotDocumentsMatch(snap.Documents, docs, attachments) {
		return nil, nil
	}
	metadata, err := LoadVectorMetadata(VectorMetadataPath(path))
	if err != nil || !vectorMetadataMatches(metadata, snap) {
		return nil, nil
	}
	out := make(map[string][]float64, len(snap.Vectors))
	for _, item := range snap.Vectors {
		if len(item.Vector) > 0 {
			out[item.ChunkID] = item.Vector
		}
	}
	return out, nil
}

func vectorMetadataMatches(metadata VectorMetadata, snap VectorSnapshot) bool {
	embedder, err := newOpenAIEmbedderFromEnv()
	if err != nil {
		return false
	}
	return metadata.Version == 1 &&
		metadata.CorpusHash == snap.CorpusHash &&
		metadata.Model == snap.Model &&
		metadata.Dimensions == snap.Dimensions &&
		metadata.Model == embedder.Model &&
		metadata.Dimensions == embedder.Dimensions &&
		metadata.QueryPrefix == envDefaultPreserveSpace("KRX_EMBEDDING_QUERY_PREFIX", "query: ") &&
		metadata.DocumentPrefix == envDefaultPreserveSpace("KRX_EMBEDDING_DOCUMENT_PREFIX", "passage: ")
}

func engineFromSnapshot(docs []model.Document, snap Snapshot, vectors map[string][]float64) *Engine {
	e := &Engine{
		docs:         make(map[string]model.Document, len(docs)),
		chunkByID:    map[string]int{},
		chunkGroups:  map[string][]int{},
		df:           make(map[string]int, len(snap.DF)),
		avgDocLength: snap.AvgDocLength,
	}
	for _, doc := range docs {
		e.docs[doc.ID] = doc
	}
	for token, count := range snap.DF {
		e.df[token] = count
	}
	for _, item := range snap.Chunks {
		c := chunk{
			ID:               item.ID,
			DocID:            item.DocID,
			Index:            item.Index,
			Text:             item.Text,
			Source:           item.Source,
			AttachmentID:     item.AttachmentID,
			AttachmentTitle:  item.AttachmentTitle,
			AttachmentFile:   item.AttachmentFile,
			AttachmentStatus: item.AttachmentStatus,
			Tokens:           item.Tokens,
			tokenMap:         countTokens(item.Tokens),
			Vector:           vectors[item.ID],
		}
		index := len(e.chunks)
		e.chunks = append(e.chunks, c)
		e.chunkByID[c.ID] = index
		key := chunkGroupKey(c)
		e.chunkGroups[key] = append(e.chunkGroups[key], index)
	}
	if e.avgDocLength == 0 && len(e.chunks) > 0 {
		var total int
		for _, c := range e.chunks {
			total += len(c.Tokens)
		}
		e.avgDocLength = float64(total) / float64(len(e.chunks))
	}
	return e
}

func snapshotMatches(snap Snapshot, docs []model.Document, attachments map[string]AttachmentDocument) bool {
	return snapshotDocumentsMatch(snap.Documents, docs, attachments) && snap.CorpusHash == corpusHash(docs, attachments)
}

func snapshotDocumentsMatch(snapshotDocs []SnapshotDocument, docs []model.Document, attachments map[string]AttachmentDocument) bool {
	if len(snapshotDocs) != len(docs) {
		return false
	}
	want := map[string]string{}
	for _, doc := range snapshotDocuments(docs, attachments) {
		want[doc.ID] = doc.IndexHash
	}
	for _, doc := range snapshotDocs {
		if want[doc.ID] != doc.IndexHash {
			return false
		}
	}
	return true
}

func corpusHash(docs []model.Document, attachments map[string]AttachmentDocument) string {
	return corpusHashFromDocuments(snapshotDocuments(docs, attachments))
}

func corpusHashFromDocuments(docs []SnapshotDocument) string {
	items := make([]string, 0, len(docs))
	for _, doc := range docs {
		items = append(items, doc.ID+":"+doc.IndexHash)
	}
	sort.Strings(items)
	return model.HashText(strings.Join(items, "\n"))
}

func readSnapshotDocument(r *bytes.Reader) (SnapshotDocument, error) {
	id, err := readString(r)
	if err != nil {
		return SnapshotDocument{}, err
	}
	contentHash, err := readString(r)
	if err != nil {
		return SnapshotDocument{}, err
	}
	indexHash, err := readString(r)
	if err != nil {
		return SnapshotDocument{}, err
	}
	return SnapshotDocument{ID: id, ContentHash: contentHash, IndexHash: indexHash}, nil
}

func readSnapshotChunk(r *bytes.Reader) (SnapshotChunk, error) {
	id, err := readString(r)
	if err != nil {
		return SnapshotChunk{}, err
	}
	docID, err := readString(r)
	if err != nil {
		return SnapshotChunk{}, err
	}
	index, err := readU32(r)
	if err != nil {
		return SnapshotChunk{}, err
	}
	text, err := readString(r)
	if err != nil {
		return SnapshotChunk{}, err
	}
	source, err := readString(r)
	if err != nil {
		return SnapshotChunk{}, err
	}
	attachmentID, err := readString(r)
	if err != nil {
		return SnapshotChunk{}, err
	}
	attachmentTitle, err := readString(r)
	if err != nil {
		return SnapshotChunk{}, err
	}
	attachmentFile, err := readString(r)
	if err != nil {
		return SnapshotChunk{}, err
	}
	attachmentStatus, err := readString(r)
	if err != nil {
		return SnapshotChunk{}, err
	}
	tokenCount, err := readU32(r)
	if err != nil {
		return SnapshotChunk{}, err
	}
	tokens := make([]string, 0, tokenCount)
	for range tokenCount {
		token, err := readString(r)
		if err != nil {
			return SnapshotChunk{}, err
		}
		tokens = append(tokens, token)
	}
	return SnapshotChunk{
		ID:               id,
		DocID:            docID,
		Index:            int(index),
		Text:             text,
		Source:           source,
		AttachmentID:     attachmentID,
		AttachmentTitle:  attachmentTitle,
		AttachmentFile:   attachmentFile,
		AttachmentStatus: model.AttachmentStatus(attachmentStatus),
		Tokens:           tokens,
	}, nil
}

func readU16(r *bytes.Reader) (uint16, error) {
	var value uint16
	err := binary.Read(r, binary.BigEndian, &value)
	return value, err
}

func readU32(r *bytes.Reader) (uint32, error) {
	var value uint32
	err := binary.Read(r, binary.BigEndian, &value)
	return value, err
}

func readString(r *bytes.Reader) (string, error) {
	size, err := readU32(r)
	if err != nil {
		return "", err
	}
	if uint64(size) > uint64(r.Len()) {
		return "", io.ErrUnexpectedEOF
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func gunzip(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	return io.ReadAll(gz)
}

func loadAttachmentText(dataRoot string, att model.Attachment) string {
	if att.TextPath == "" {
		return ""
	}
	path := att.TextPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(dataRoot, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func loadAttachments(dataRoot string, docs []model.Document) map[string]AttachmentDocument {
	attachments := map[string]AttachmentDocument{}
	for _, doc := range docs {
		for _, att := range doc.Attachments {
			text := loadAttachmentText(dataRoot, att)
			attachments[att.ID] = AttachmentDocument{Attachment: att, Text: text}
		}
	}
	return attachments
}

func documentIndexHash(doc model.Document, attachments map[string]AttachmentDocument) string {
	var b strings.Builder
	b.WriteString(doc.ContentHash)
	b.WriteString("|language:")
	b.WriteString(model.NormalizeLanguage(doc.Language))
	b.WriteString("|source_id:")
	b.WriteString(doc.SourceID)
	b.WriteString("|file:")
	b.WriteString(doc.FileName)
	b.WriteString(":")
	b.WriteString(doc.RawPath)
	b.WriteString(":")
	b.WriteString(doc.TextPath)
	b.WriteString(":")
	b.WriteString(doc.FileHash)
	for _, att := range doc.Attachments {
		b.WriteString("|")
		b.WriteString(att.ID)
		b.WriteString(":")
		b.WriteString(string(att.Status))
		b.WriteString(":")
		b.WriteString(att.TextPath)
		b.WriteString(":")
		b.WriteString(att.ContentHash)
		if attachment, ok := attachments[att.ID]; ok && strings.TrimSpace(attachment.Text) != "" {
			b.WriteString(":text:")
			b.WriteString(model.HashText(attachment.Text))
		}
	}
	return model.HashText(b.String())
}

func parseSnapshotTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}
