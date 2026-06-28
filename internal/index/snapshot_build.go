package index

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/corpus"
	"github.com/chromato99/krx-rule-mcp/internal/model"
)

const snapshotFormatVersion uint16 = 1

type VectorMetadata struct {
	Version        int    `json:"version"`
	GeneratedAt    string `json:"generated_at"`
	CorpusHash     string `json:"corpus_hash"`
	Model          string `json:"model"`
	Dimensions     int    `json:"dimensions"`
	QueryPrefix    string `json:"query_prefix"`
	DocumentPrefix string `json:"document_prefix"`
}

func BuildSnapshot(dataRoot string) (Snapshot, []model.Document, error) {
	docs, err := corpus.LoadDocuments(dataRoot)
	if err != nil {
		return Snapshot{}, nil, err
	}
	attachments := loadAttachments(dataRoot, docs)
	engine := BuildWithAttachments(docs, attachments, nil)
	documents := snapshotDocuments(docs, attachments)
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
			Tokens:           c.Tokens,
		})
	}
	return Snapshot{
		Version:      snapshotFormatVersion,
		GeneratedAt:  nowRFC3339(),
		CorpusHash:   corpusHashFromDocuments(documents),
		Documents:    documents,
		AvgDocLength: engine.avgDocLength,
		DF:           engine.df,
		Chunks:       chunks,
	}, docs, nil
}

func WriteSnapshot(path string, snap Snapshot) error {
	var payload bytes.Buffer
	writeU16(&payload, snapshotFormatVersion)
	writeString(&payload, snap.GeneratedAt)
	writeString(&payload, snap.CorpusHash)
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
		writeU32(&payload, uint32(len(chunk.Tokens)))
		for _, token := range chunk.Tokens {
			writeString(&payload, token)
		}
	}
	return writeCompressed(path, indexSnapshotMagic, payload.Bytes())
}

func WriteVectorSnapshot(path string, snap Snapshot, vectors map[string][]float64, model string, dimensions int) error {
	var payload bytes.Buffer
	writeU16(&payload, snapshotFormatVersion)
	writeString(&payload, nowRFC3339())
	writeString(&payload, snap.CorpusHash)
	writeString(&payload, model)
	writeU32(&payload, uint32(dimensions))
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
	metadata.Version = 1
	if metadata.GeneratedAt == "" {
		metadata.GeneratedAt = nowRFC3339()
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func LoadVectorMetadata(path string) (VectorMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return VectorMetadata{}, err
	}
	var metadata VectorMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return VectorMetadata{}, err
	}
	return metadata, nil
}

func VectorMetadataPath(vectorPath string) string {
	return vectorPath + ".meta.json"
}

func snapshotDocuments(docs []model.Document, attachments map[string]AttachmentDocument) []SnapshotDocument {
	out := make([]SnapshotDocument, 0, len(docs))
	for _, doc := range docs {
		out = append(out, SnapshotDocument{
			ID:          doc.ID,
			ContentHash: doc.ContentHash,
			IndexHash:   documentIndexHash(doc, attachments),
		})
	}
	return out
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(magic, compressed.Bytes()...), 0o644)
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
