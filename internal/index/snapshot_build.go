package index

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/corpus"
	"github.com/chromato99/krx-rule-mcp/internal/model"
)

const (
	indexSnapshotFormatVersion  uint16 = 6
	indexerVersion                     = "tokenizer-ko-2gram-3gram-script-html-alias-structured-anchor-v1-asset-ref-v1-chunk1600-md-html-table-row-equation-pair-bm25-k1-1.4-b-0.75"
	vectorSnapshotFormatVersion uint16 = VectorSnapshotFormatVersion
	VectorSnapshotFormatVersion uint16 = 3
	VectorMetadataFormatVersion        = 3
)

type VectorScope string

const (
	VectorScopeFull   VectorScope = "full"
	VectorScopeSample VectorScope = "sample"
)

type VectorMetadata struct {
	Version              int         `json:"version"`
	GeneratedAt          string      `json:"generated_at"`
	GenerationID         string      `json:"generation_id"`
	IndexSourceHash      string      `json:"index_source_hash"`
	IndexBuildHash       string      `json:"index_build_hash"`
	CorpusReleaseHash    string      `json:"corpus_release_hash,omitempty"`
	CorpusHash           string      `json:"corpus_hash,omitempty"`
	Model                string      `json:"model"`
	ModelRevision        string      `json:"model_revision,omitempty"`
	Dimensions           int         `json:"dimensions"`
	QueryPrefix          string      `json:"query_prefix"`
	DocumentPrefix       string      `json:"document_prefix"`
	Scope                VectorScope `json:"scope"`
	ExpectedChunkCount   int         `json:"expected_chunk_count"`
	StoredVectorCount    int         `json:"stored_vector_count"`
	ChunkIDSetHash       string      `json:"chunk_id_set_hash"`
	StoredChunkIDSetHash string      `json:"stored_chunk_id_set_hash"`
}

func BuildSnapshot(dataRoot string) (Snapshot, []model.Document, error) {
	return buildSnapshot(dataRoot, false)
}

func BuildReleaseSnapshot(dataRoot string) (Snapshot, []model.Document, error) {
	return buildSnapshot(dataRoot, true)
}

func buildSnapshot(dataRoot string, requireManifest bool) (Snapshot, []model.Document, error) {
	loaded, err := corpus.LoadWithOptions(dataRoot, corpus.LoadOptions{RequireManifest: requireManifest})
	if err != nil {
		return Snapshot{}, nil, err
	}
	docs := loaded.Documents
	attachments := attachmentDocuments(docs, loaded.AttachmentTexts)
	engine := BuildWithAttachments(docs, attachments, nil)
	documents, err := snapshotDocuments(docs, attachments)
	if err != nil {
		return Snapshot{}, nil, err
	}
	indexSourceHash, err := corpus.IndexSourceHash(docs, loaded.AttachmentTexts)
	if err != nil {
		return Snapshot{}, nil, err
	}
	indexBuildHash := buildHash(indexSourceHash, indexerVersion)
	chunks := make([]SnapshotChunk, 0, len(engine.chunks))
	for _, c := range engine.chunks {
		chunks = append(chunks, SnapshotChunk{
			ID:               c.ID,
			DocID:            c.DocID,
			Index:            c.Index,
			Text:             c.Text,
			Source:           c.Source,
			AttachmentID:     c.AttachmentID,
			AttachmentTitle:  c.AttachmentTitle,
			AttachmentFile:   c.AttachmentFile,
			AttachmentStatus: c.AttachmentStatus,
			ArticleID:        c.ArticleID,
			HeadingPath:      append([]string(nil), c.HeadingPath...),
			Tokens:           c.Tokens,
		})
	}
	return Snapshot{
		Version:           indexSnapshotFormatVersion,
		IndexerVersion:    indexerVersion,
		GeneratedAt:       nowRFC3339(),
		IndexSourceHash:   indexSourceHash,
		IndexBuildHash:    indexBuildHash,
		CorpusReleaseHash: loaded.ReleaseHash,
		CorpusHash:        indexSourceHash,
		Documents:         documents,
		AvgDocLength:      engine.avgDocLength,
		DF:                engine.df,
		Chunks:            chunks,
	}, docs, nil
}

func WriteSnapshot(path string, snap Snapshot) error {
	normalizeSnapshotHashes(&snap)
	snap.Version = indexSnapshotFormatVersion
	snap.IndexerVersion = firstNonEmpty(snap.IndexerVersion, indexerVersion)
	if err := validateSnapshotStructure(snap); err != nil {
		return fmt.Errorf("write index snapshot: %w", err)
	}
	var payload bytes.Buffer
	writeU16(&payload, indexSnapshotFormatVersion)
	writeString(&payload, snap.GeneratedAt)
	writeString(&payload, snap.IndexSourceHash)
	writeString(&payload, snap.IndexBuildHash)
	writeString(&payload, snap.CorpusReleaseHash)
	writeString(&payload, firstNonEmpty(snap.IndexerVersion, indexerVersion))
	writeU32(&payload, uint32(len(snap.Documents)))
	for _, doc := range snap.Documents {
		writeString(&payload, doc.ID)
		writeString(&payload, doc.ContentHash)
		writeString(&payload, doc.IndexHash)
	}
	writeF64(&payload, snap.AvgDocLength)
	tokens := make([]string, 0, len(snap.DF))
	for token := range snap.DF {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	writeU32(&payload, uint32(len(tokens)))
	for _, token := range tokens {
		writeString(&payload, token)
		writeU32(&payload, uint32(snap.DF[token]))
	}
	writeU32(&payload, uint32(len(snap.Chunks)))
	for _, chunk := range snap.Chunks {
		writeString(&payload, chunk.ID)
		writeString(&payload, chunk.DocID)
		writeU32(&payload, uint32(chunk.Index))
		writeString(&payload, chunk.Text)
		writeString(&payload, chunk.Source)
		writeString(&payload, chunk.AttachmentID)
		writeString(&payload, chunk.AttachmentTitle)
		writeString(&payload, chunk.AttachmentFile)
		writeString(&payload, string(chunk.AttachmentStatus))
		writeString(&payload, chunk.ArticleID)
		writeU32(&payload, uint32(len(chunk.HeadingPath)))
		for _, heading := range chunk.HeadingPath {
			writeString(&payload, heading)
		}
		writeU32(&payload, uint32(len(chunk.Tokens)))
		for _, token := range chunk.Tokens {
			writeString(&payload, token)
		}
	}
	return writeCompressed(path, indexSnapshotMagic, payload.Bytes())
}

type VectorWriteOptions struct {
	Scope          VectorScope
	ModelRevision  string
	QueryPrefix    string
	DocumentPrefix string
	GenerationID   string
}

func WriteVectorSnapshot(path string, snap Snapshot, vectors map[string][]float64, model string, dimensions int, options ...VectorWriteOptions) error {
	normalizeSnapshotHashes(&snap)
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("embedding model is required")
	}
	option := VectorWriteOptions{}
	if len(options) > 0 {
		option = options[0]
	}
	expectedIDs := snapshotChunkIDs(snap.Chunks)
	option = normalizeVectorWriteOptions(snap, vectors, model, dimensions, option)
	if option.Scope != VectorScopeFull && option.Scope != VectorScopeSample {
		return fmt.Errorf("invalid vector scope %q", option.Scope)
	}
	if err := ValidateVectorMap(vectors, expectedIDs, dimensions, option.Scope == VectorScopeFull); err != nil {
		return err
	}
	generatedAt := nowRFC3339()
	var payload bytes.Buffer
	writeU16(&payload, vectorSnapshotFormatVersion)
	writeString(&payload, generatedAt)
	writeString(&payload, option.GenerationID)
	writeString(&payload, snap.IndexSourceHash)
	writeString(&payload, snap.IndexBuildHash)
	writeString(&payload, snap.CorpusReleaseHash)
	writeString(&payload, model)
	writeString(&payload, option.ModelRevision)
	writeU32(&payload, uint32(dimensions))
	writeString(&payload, option.QueryPrefix)
	writeString(&payload, option.DocumentPrefix)
	writeString(&payload, string(option.Scope))
	writeU32(&payload, uint32(len(expectedIDs)))
	writeString(&payload, chunkIDSetHash(expectedIDs))
	writeU32(&payload, uint32(len(snap.Documents)))
	for _, doc := range snap.Documents {
		writeString(&payload, doc.ID)
		writeString(&payload, doc.ContentHash)
		writeString(&payload, doc.IndexHash)
	}
	ids := make([]string, 0, len(vectors))
	for id := range vectors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	writeU32(&payload, uint32(len(ids)))
	for _, id := range ids {
		vector := vectors[id]
		writeString(&payload, id)
		writeU32(&payload, uint32(len(vector)))
		for _, value := range vector {
			_ = binary.Write(&payload, binary.BigEndian, float32(value))
		}
	}
	return writeCompressed(path, vectorSnapshotMagic, payload.Bytes())
}

func WriteVectorMetadata(path string, metadata VectorMetadata) error {
	metadata.Version = VectorMetadataFormatVersion
	if metadata.IndexSourceHash == "" {
		metadata.IndexSourceHash = metadata.CorpusHash
	}
	if metadata.CorpusHash == "" {
		metadata.CorpusHash = metadata.IndexSourceHash
	}
	if metadata.GeneratedAt == "" {
		metadata.GeneratedAt = nowRFC3339()
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, 0o644)
}

func LoadVectorMetadata(path string) (VectorMetadata, error) {
	metadata, _, err := LoadVectorMetadataWithDigest(path)
	return metadata, err
}

// LoadVectorMetadataWithDigest returns the digest of the same bytes that were
// decoded as metadata, so callers never need to reopen the artifact path.
func LoadVectorMetadataWithDigest(path string) (VectorMetadata, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return VectorMetadata{}, "", err
	}
	digest := model.HashBytes(data)
	var metadata VectorMetadata
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return VectorMetadata{}, digest, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("unexpected second JSON value")
		}
		return VectorMetadata{}, digest, fmt.Errorf("vector metadata contains trailing data: %w", err)
	}
	return metadata, digest, nil
}

func VectorMetadataPath(vectorPath string) string {
	return vectorPath + ".meta.json"
}

func snapshotDocuments(docs []model.Document, attachments map[string]AttachmentDocument) ([]SnapshotDocument, error) {
	out := make([]SnapshotDocument, 0, len(docs))
	texts := attachmentTextMap(attachments)
	for _, doc := range docs {
		indexHash, err := corpus.DocumentIndexSourceHash(doc, texts)
		if err != nil {
			return nil, err
		}
		out = append(out, SnapshotDocument{
			ID:          doc.ID,
			ContentHash: doc.EffectiveBodyHash(),
			IndexHash:   indexHash,
		})
	}
	return out, nil
}

func writeCompressed(path string, magic []byte, payload []byte) error {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(payload); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return writeFileAtomic(path, append(magic, compressed.Bytes()...), 0o644)
}

func writeU16(buf *bytes.Buffer, value uint16) {
	_ = binary.Write(buf, binary.BigEndian, value)
}

func writeU32(buf *bytes.Buffer, value uint32) {
	_ = binary.Write(buf, binary.BigEndian, value)
}

func writeF64(buf *bytes.Buffer, value float64) {
	_ = binary.Write(buf, binary.BigEndian, value)
}

func writeString(buf *bytes.Buffer, value string) {
	data := []byte(value)
	writeU32(buf, uint32(len(data)))
	_, _ = buf.Write(data)
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
