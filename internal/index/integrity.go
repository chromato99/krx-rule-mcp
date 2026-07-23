package index

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chromato99/krx-rule-mcp/internal/corpus"
	"github.com/chromato99/krx-rule-mcp/internal/model"
)

func buildHash(indexSourceHash, version string) string {
	payload, _ := json.Marshal(map[string]any{
		"index_format_version": indexSnapshotFormatVersion,
		"index_source_hash":    indexSourceHash,
		"indexer_version":      version,
	})
	return model.HashBytes(payload)
}

func normalizeSnapshotHashes(snap *Snapshot) {
	if snap.IndexSourceHash == "" {
		snap.IndexSourceHash = snap.CorpusHash
	}
	if snap.CorpusHash == "" {
		snap.CorpusHash = snap.IndexSourceHash
	}
	if snap.IndexBuildHash == "" && snap.IndexSourceHash != "" {
		snap.IndexBuildHash = buildHash(snap.IndexSourceHash, firstNonEmpty(snap.IndexerVersion, indexerVersion))
	}
}

func attachmentDocuments(docs []model.Document, texts map[string]string) map[string]AttachmentDocument {
	out := make(map[string]AttachmentDocument, len(texts))
	for _, doc := range docs {
		for _, att := range doc.Attachments {
			text, ok := texts[att.ID]
			if !ok {
				continue
			}
			out[att.ID] = AttachmentDocument{Attachment: att, Text: text}
		}
	}
	return out
}

func attachmentTextMap(attachments map[string]AttachmentDocument) map[string]string {
	out := make(map[string]string, len(attachments))
	for id, attachment := range attachments {
		out[id] = attachment.Text
	}
	return out
}

func snapshotChunkIDs(chunks []SnapshotChunk) []string {
	ids := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		ids = append(ids, chunk.ID)
	}
	return ids
}

func chunkIDSetHash(ids []string) string {
	canonical := append([]string(nil), ids...)
	sort.Strings(canonical)
	payload, _ := json.Marshal(canonical)
	return model.HashBytes(payload)
}

func vectorSnapshotIDSetHash(snap VectorSnapshot) string {
	ids := make([]string, 0, len(snap.Vectors))
	for _, vector := range snap.Vectors {
		ids = append(ids, vector.ChunkID)
	}
	return chunkIDSetHash(ids)
}

func snapshotForValidation(docs []model.Document, attachments map[string]AttachmentDocument) (Snapshot, error) {
	documents, err := snapshotDocuments(docs, attachments)
	if err != nil {
		return Snapshot{}, err
	}
	indexSourceHash, err := corpus.IndexSourceHash(docs, attachmentTextMap(attachments))
	if err != nil {
		return Snapshot{}, err
	}
	engine := BuildWithAttachments(docs, attachments, nil)
	chunks := make([]SnapshotChunk, 0, len(engine.chunks))
	for _, chunk := range engine.chunks {
		chunks = append(chunks, SnapshotChunk{ID: chunk.ID})
	}
	return Snapshot{
		Version:         indexSnapshotFormatVersion,
		IndexerVersion:  indexerVersion,
		IndexSourceHash: indexSourceHash,
		IndexBuildHash:  buildHash(indexSourceHash, indexerVersion),
		CorpusHash:      indexSourceHash,
		Documents:       documents,
		Chunks:          chunks,
	}, nil
}

func inferVectorScope(expectedIDs []string, vectors map[string][]float64) VectorScope {
	if len(expectedIDs) != len(vectors) {
		return VectorScopeSample
	}
	for _, id := range expectedIDs {
		if _, ok := vectors[id]; !ok {
			return VectorScopeSample
		}
	}
	return VectorScopeFull
}

func normalizeVectorWriteOptions(snap Snapshot, vectors map[string][]float64, modelName string, dimensions int, option VectorWriteOptions) VectorWriteOptions {
	option.ModelRevision = strings.TrimSpace(option.ModelRevision)
	if option.Scope == "" {
		option.Scope = inferVectorScope(snapshotChunkIDs(snap.Chunks), vectors)
	}
	if option.GenerationID == "" {
		option.GenerationID = vectorGenerationID(snap, vectors, modelName, dimensions, option)
	}
	return option
}

func vectorGenerationID(snap Snapshot, vectors map[string][]float64, modelName string, dimensions int, option VectorWriteOptions) string {
	normalizeSnapshotHashes(&snap)
	ids := make([]string, 0, len(vectors))
	for id := range vectors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var vectorBytes bytes.Buffer
	for _, id := range ids {
		_ = binary.Write(&vectorBytes, binary.BigEndian, uint32(len(id)))
		_, _ = vectorBytes.WriteString(id)
		for _, value := range vectors[id] {
			_ = binary.Write(&vectorBytes, binary.BigEndian, math.Float32bits(float32(value)))
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"corpus_release_hash": snap.CorpusReleaseHash,
		"dimensions":          dimensions,
		"document_prefix":     option.DocumentPrefix,
		"index_build_hash":    snap.IndexBuildHash,
		"model":               modelName,
		"model_revision":      option.ModelRevision,
		"query_prefix":        option.QueryPrefix,
		"scope":               option.Scope,
		"vector_hash":         model.HashBytes(vectorBytes.Bytes()),
	})
	return model.HashBytes(payload)
}

func validateFiniteNonZeroFloat32Vector(vector []float64) error {
	hasNonZero := false
	for position, value := range vector {
		value32 := float32(value)
		if math.IsNaN(value) || math.IsInf(value, 0) || math.IsInf(float64(value32), 0) {
			return fmt.Errorf("value at position %d is not finite float32", position)
		}
		if value32 != 0 {
			hasNonZero = true
		}
	}
	if !hasNonZero {
		return fmt.Errorf("has zero float32 norm")
	}
	return nil
}

// ValidateVectorMap enforces chunk identity, dimensions, finite non-zero
// values, and optional full coverage before vectors can be persisted or served.
func ValidateVectorMap(vectors map[string][]float64, expectedIDs []string, dimensions int, requireFull bool) error {
	if dimensions <= 0 {
		return fmt.Errorf("vector dimensions must be positive")
	}
	expected := make(map[string]struct{}, len(expectedIDs))
	for _, id := range expectedIDs {
		if id == "" {
			return fmt.Errorf("expected chunk id is empty")
		}
		if _, duplicate := expected[id]; duplicate {
			return fmt.Errorf("duplicate expected chunk id %q", id)
		}
		expected[id] = struct{}{}
	}
	for id, vector := range vectors {
		if _, ok := expected[id]; !ok {
			return fmt.Errorf("vector references unknown chunk id %q", id)
		}
		if len(vector) != dimensions {
			return fmt.Errorf("vector %q dimensions=%d want=%d", id, len(vector), dimensions)
		}
		if err := validateFiniteNonZeroFloat32Vector(vector); err != nil {
			return fmt.Errorf("vector %q %w", id, err)
		}
	}
	if requireFull {
		if len(vectors) != len(expected) {
			return fmt.Errorf("full vector coverage requires %d vectors, got %d", len(expected), len(vectors))
		}
		for id := range expected {
			if _, ok := vectors[id]; !ok {
				return fmt.Errorf("full vector coverage is missing chunk %q", id)
			}
		}
	}
	return nil
}

func validateSnapshotStructure(snap Snapshot) error {
	if snap.IndexSourceHash == "" || snap.IndexBuildHash == "" || snap.IndexerVersion == "" {
		return fmt.Errorf("snapshot hash/version metadata is incomplete")
	}
	documents := make(map[string]struct{}, len(snap.Documents))
	for _, document := range snap.Documents {
		if document.ID == "" {
			return fmt.Errorf("document id is empty")
		}
		if _, duplicate := documents[document.ID]; duplicate {
			return fmt.Errorf("duplicate document id %q", document.ID)
		}
		documents[document.ID] = struct{}{}
	}
	chunks := make(map[string]struct{}, len(snap.Chunks))
	for _, chunk := range snap.Chunks {
		if chunk.ID == "" {
			return fmt.Errorf("chunk id is empty")
		}
		if _, duplicate := chunks[chunk.ID]; duplicate {
			return fmt.Errorf("duplicate chunk id %q", chunk.ID)
		}
		chunks[chunk.ID] = struct{}{}
		if _, ok := documents[chunk.DocID]; !ok {
			return fmt.Errorf("chunk %q references unknown document %q", chunk.ID, chunk.DocID)
		}
		if chunk.Source != "body" && chunk.Source != "attachment" {
			return fmt.Errorf("chunk %q has invalid source %q", chunk.ID, chunk.Source)
		}
		if chunk.Source == "attachment" && chunk.AttachmentID == "" {
			return fmt.Errorf("attachment chunk %q has no attachment id", chunk.ID)
		}
		if len(chunk.HeadingPath) > 64 {
			return fmt.Errorf("chunk %q heading path is too deep", chunk.ID)
		}
		articleFound := chunk.ArticleID == ""
		for _, heading := range chunk.HeadingPath {
			if strings.TrimSpace(heading) == "" {
				return fmt.Errorf("chunk %q has an empty heading path element", chunk.ID)
			}
			if chunk.ArticleID != "" && strings.HasPrefix(heading, chunk.ArticleID) {
				articleFound = true
			}
		}
		if !articleFound {
			return fmt.Errorf("chunk %q article %q is absent from heading path", chunk.ID, chunk.ArticleID)
		}
	}
	if len(snap.Chunks) > 0 && (math.IsNaN(snap.AvgDocLength) || math.IsInf(snap.AvgDocLength, 0) || snap.AvgDocLength <= 0) {
		return fmt.Errorf("invalid average document length")
	}
	return nil
}

func validateVectorSnapshotStructure(snap VectorSnapshot) error {
	if snap.Dimensions <= 0 || snap.Model == "" || snap.IndexSourceHash == "" {
		return fmt.Errorf("vector snapshot metadata is incomplete")
	}
	if snap.Version >= 2 {
		if snap.GenerationID == "" || snap.IndexBuildHash == "" {
			return fmt.Errorf("vector generation/build metadata is incomplete")
		}
		if snap.Scope != VectorScopeFull && snap.Scope != VectorScopeSample {
			return fmt.Errorf("invalid vector scope %q", snap.Scope)
		}
		if snap.ExpectedChunkCount < 0 || snap.ChunkIDSetHash == "" {
			return fmt.Errorf("vector coverage metadata is incomplete")
		}
	}
	documents := make(map[string]struct{}, len(snap.Documents))
	for _, document := range snap.Documents {
		if document.ID == "" {
			return fmt.Errorf("vector document id is empty")
		}
		if _, duplicate := documents[document.ID]; duplicate {
			return fmt.Errorf("duplicate vector document id %q", document.ID)
		}
		documents[document.ID] = struct{}{}
	}
	vectorIDs := make(map[string]struct{}, len(snap.Vectors))
	for _, item := range snap.Vectors {
		if item.ChunkID == "" {
			return fmt.Errorf("vector chunk id is empty")
		}
		if _, duplicate := vectorIDs[item.ChunkID]; duplicate {
			return fmt.Errorf("duplicate vector chunk id %q", item.ChunkID)
		}
		vectorIDs[item.ChunkID] = struct{}{}
		if len(item.Vector) != snap.Dimensions {
			return fmt.Errorf("vector %q dimensions=%d want=%d", item.ChunkID, len(item.Vector), snap.Dimensions)
		}
		for _, value := range item.Vector {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("vector %q contains non-finite value", item.ChunkID)
			}
		}
	}
	return nil
}

func BuildVectorMetadata(snap Snapshot, vectors map[string][]float64, modelName string, dimensions int, option VectorWriteOptions) VectorMetadata {
	normalizeSnapshotHashes(&snap)
	option = normalizeVectorWriteOptions(snap, vectors, modelName, dimensions, option)
	expectedIDs := snapshotChunkIDs(snap.Chunks)
	storedIDs := make([]string, 0, len(vectors))
	for id := range vectors {
		storedIDs = append(storedIDs, id)
	}
	return VectorMetadata{
		Version:              VectorMetadataFormatVersion,
		GeneratedAt:          nowRFC3339(),
		GenerationID:         option.GenerationID,
		IndexSourceHash:      snap.IndexSourceHash,
		IndexBuildHash:       snap.IndexBuildHash,
		CorpusReleaseHash:    snap.CorpusReleaseHash,
		CorpusHash:           snap.IndexSourceHash,
		Model:                modelName,
		ModelRevision:        option.ModelRevision,
		Dimensions:           dimensions,
		QueryPrefix:          option.QueryPrefix,
		DocumentPrefix:       option.DocumentPrefix,
		Scope:                option.Scope,
		ExpectedChunkCount:   len(expectedIDs),
		StoredVectorCount:    len(vectors),
		ChunkIDSetHash:       chunkIDSetHash(expectedIDs),
		StoredChunkIDSetHash: chunkIDSetHash(storedIDs),
	}
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) (retErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		if retErr != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
