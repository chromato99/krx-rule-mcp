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
	"strings"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/corpus"
	"github.com/chromato99/krx-rule-mcp/internal/model"
)

type Snapshot struct {
	Version           uint16
	IndexerVersion    string
	GeneratedAt       string
	IndexSourceHash   string
	IndexBuildHash    string
	CorpusReleaseHash string
	CorpusHash        string
	Documents         []SnapshotDocument
	AvgDocLength      float64
	DF                map[string]int
	Chunks            []SnapshotChunk
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
	ArticleID        string
	HeadingPath      []string
	Tokens           []string
	Vector           []float64
}

type VectorSnapshot struct {
	Version            uint16
	GeneratedAt        string
	GenerationID       string
	IndexSourceHash    string
	IndexBuildHash     string
	CorpusReleaseHash  string
	CorpusHash         string
	Model              string
	ModelRevision      string
	Dimensions         int
	QueryPrefix        string
	DocumentPrefix     string
	Scope              VectorScope
	ExpectedChunkCount int
	ChunkIDSetHash     string
	Documents          []SnapshotDocument
	Vectors            []SnapshotVector
}

type SnapshotVector struct {
	ChunkID string
	Vector  []float64
}

type Repository struct {
	DataRoot            string
	IndexPath           string
	VectorPath          string
	GenerationID        string
	GenerationDir       string
	BM25ArtifactDigest  string
	BM25SnapshotVersion uint16
	IndexerVersion      string
	VectorIndexes       []VectorIndexStatus
	IndexSourceHash     string
	IndexBuildHash      string
	CorpusReleaseHash   string
	IndexGeneratedAt    string
	VectorScope         VectorScope
	VectorCoverage      float64
	Documents           map[string]model.Document
	Attachments         map[string]AttachmentDocument
	Engine              *Engine
}

type AttachmentDocument struct {
	Attachment model.Attachment
	Text       string
}

type VectorIndexStatus struct {
	Path           string
	LoadedVectors  int
	RejectedReason string
	ArtifactDigest string
	MetadataDigest string
	Metadata       VectorMetadata
}

var (
	indexSnapshotMagic  = []byte("KRXIDX2\n")
	vectorSnapshotMagic = []byte("KRXVEC2\n")
)

const (
	maxSnapshotPayloadBytes = 1 << 30
	maxSnapshotDocuments    = 100_000
	maxSnapshotTerms        = 20_000_000
	maxSnapshotChunks       = 10_000_000
	maxChunkTokens          = 1_000_000
	maxVectorDimensions     = 65_536
	maxSnapshotVectors      = 10_000_000
)

func LoadRepository(dataRoot, indexPath string, vectorIndexPaths ...string) (*Repository, error) {
	return LoadRepositoryWithOptions(dataRoot, indexPath, RepositoryLoadOptions{
		VectorEnabled:    len(vectorIndexPaths) > 0,
		VectorIndexPaths: vectorIndexPaths,
	})
}

type RepositoryLoadOptions struct {
	VectorEnabled         bool
	RequireVector         bool
	RequireCorpusManifest bool
	VectorIndexPaths      []string
}

// LoadRepositoryGeneration resolves the current generation pointer once and
// loads only artifacts from that immutable, content-addressed directory. The
// digests recorded on Repository are computed from the exact bytes decoded by
// the loaders and checked against generation.json.
func LoadRepositoryGeneration(dataRoot, indexDir string, options RepositoryLoadOptions) (*Repository, error) {
	descriptor, generationDir, err := ReadCurrentGeneration(indexDir)
	if err != nil {
		return nil, fmt.Errorf("resolve current index generation: %w", err)
	}
	indexPath := filepath.Join(generationDir, BM25SnapshotFile)
	options.RequireCorpusManifest = true
	options.VectorIndexPaths = nil
	if options.VectorEnabled && descriptor.Vector != nil {
		options.VectorIndexPaths = []string{filepath.Join(generationDir, VectorSnapshotFile)}
	}
	repo, err := LoadRepositoryWithOptions(dataRoot, indexPath, options)
	if err != nil {
		return nil, err
	}
	if repo.BM25ArtifactDigest != descriptor.BM25.SHA256 {
		return nil, fmt.Errorf("generation BM25 digest mismatch: descriptor=%s loaded=%s", descriptor.BM25.SHA256, repo.BM25ArtifactDigest)
	}
	if repo.IndexSourceHash != descriptor.IndexSourceHash || repo.IndexBuildHash != descriptor.IndexBuildHash || repo.CorpusReleaseHash != descriptor.CorpusReleaseHash {
		return nil, fmt.Errorf("generation descriptor identity does not match loaded BM25 snapshot")
	}
	if options.VectorEnabled && descriptor.Vector != nil {
		if len(repo.VectorIndexes) != 1 {
			return nil, fmt.Errorf("generation vector artifact was not inspected")
		}
		status := repo.VectorIndexes[0]
		if status.ArtifactDigest != descriptor.Vector.Artifact.SHA256 {
			return nil, fmt.Errorf("generation vector digest mismatch: descriptor=%s loaded=%s", descriptor.Vector.Artifact.SHA256, status.ArtifactDigest)
		}
		if status.MetadataDigest != descriptor.Vector.Metadata.SHA256 {
			return nil, fmt.Errorf("generation vector metadata digest mismatch: descriptor=%s loaded=%s", descriptor.Vector.Metadata.SHA256, status.MetadataDigest)
		}
		if status.RejectedReason == "" && status.LoadedVectors > 0 && (status.Metadata.Model != descriptor.Vector.Model || status.Metadata.ModelRevision != descriptor.Vector.Revision || status.Metadata.StoredVectorCount != descriptor.Vector.Vectors) {
			return nil, fmt.Errorf("generation vector descriptor does not match loaded metadata")
		}
	}
	repo.GenerationID = descriptor.GenerationID
	repo.GenerationDir = generationDir
	return repo, nil
}

func LoadRepositoryWithOptions(dataRoot, indexPath string, options RepositoryLoadOptions) (*Repository, error) {
	if options.RequireVector && !options.VectorEnabled {
		return nil, fmt.Errorf("require-vector policy requires vector search to be enabled")
	}
	loadedCorpus, err := corpus.LoadWithOptions(dataRoot, corpus.LoadOptions{RequireManifest: options.RequireCorpusManifest})
	if err != nil {
		return nil, fmt.Errorf("load markdown corpus: %w", err)
	}
	docs := loadedCorpus.Documents
	snap, bm25Digest, err := LoadSnapshotWithDigest(indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("BM25 index snapshot %q not found; run krx-rule-index --data-dir %s --index %s", indexPath, dataRoot, indexPath)
	}
	if err != nil {
		return nil, err
	}
	attachments := attachmentDocuments(docs, loadedCorpus.AttachmentTexts)
	if !snapshotMatches(snap, docs, attachments) {
		return nil, fmt.Errorf("BM25 index snapshot %q does not match Markdown corpus; run krx-rule-index --data-dir %s --index %s", indexPath, dataRoot, indexPath)
	}
	if snap.CorpusReleaseHash != loadedCorpus.ReleaseHash {
		return nil, fmt.Errorf("BM25 index corpus release %q does not match corpus manifest release %q", snap.CorpusReleaseHash, loadedCorpus.ReleaseHash)
	}
	vectors := map[string][]float64{}
	var vectorPath string
	var vectorStatuses []VectorIndexStatus
	if options.VectorEnabled && len(options.VectorIndexPaths) == 0 && options.RequireVector {
		return nil, fmt.Errorf("require-vector policy needs at least one vector snapshot path")
	}
	for _, path := range options.VectorIndexPaths {
		if !options.VectorEnabled {
			break
		}
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		status := VectorIndexStatus{Path: path}
		loadedResult, err := loadVectorArtifactForSnapshot(path, docs, attachments, snap, options.RequireVector)
		status.ArtifactDigest = loadedResult.ArtifactDigest
		status.MetadataDigest = loadedResult.MetadataDigest
		status.Metadata = loadedResult.Metadata
		if errors.Is(err, os.ErrNotExist) {
			status.RejectedReason = "missing"
			vectorStatuses = append(vectorStatuses, status)
			if options.RequireVector {
				return nil, fmt.Errorf("required vector snapshot %q is missing", path)
			}
			continue
		}
		if err != nil {
			status.RejectedReason = boundedVectorRejection("load_failed: " + err.Error())
			vectorStatuses = append(vectorStatuses, status)
			if options.RequireVector {
				return nil, fmt.Errorf("load required vector snapshot %q: %w", path, err)
			}
			continue
		}
		loaded := loadedResult.Vectors
		status.LoadedVectors = len(loaded)
		status.RejectedReason = boundedVectorRejection(loadedResult.Reason)
		vectorStatuses = append(vectorStatuses, status)
		if loadedResult.Reason != "" && options.RequireVector {
			return nil, fmt.Errorf("required vector snapshot %q rejected: %s", path, loadedResult.Reason)
		}
		for id, vector := range loaded {
			vectors[id] = vector
		}
		if len(loaded) > 0 && vectorPath == "" {
			vectorPath = path
		}
	}
	if options.RequireVector && len(vectors) == 0 {
		return nil, fmt.Errorf("require-vector policy loaded no vectors")
	}
	loadedVectorScope := VectorScope("")
	if len(vectors) > 0 {
		loadedVectorScope = inferVectorScope(snapshotChunkIDs(snap.Chunks), vectors)
	}
	engine := engineFromSnapshot(docs, snap, vectors)
	repo := &Repository{
		DataRoot:            dataRoot,
		IndexPath:           indexPath,
		VectorPath:          vectorPath,
		BM25ArtifactDigest:  bm25Digest,
		BM25SnapshotVersion: snap.Version,
		IndexerVersion:      snap.IndexerVersion,
		VectorIndexes:       vectorStatuses,
		IndexSourceHash:     snap.IndexSourceHash,
		IndexBuildHash:      snap.IndexBuildHash,
		CorpusReleaseHash:   snap.CorpusReleaseHash,
		IndexGeneratedAt:    snap.GeneratedAt,
		VectorScope:         loadedVectorScope,
		Documents:           map[string]model.Document{},
		Attachments:         attachments,
		Engine:              engine,
	}
	if len(snap.Chunks) > 0 {
		repo.VectorCoverage = float64(len(vectors)) / float64(len(snap.Chunks))
	}
	for _, doc := range docs {
		repo.Documents[doc.ID] = doc
	}
	return repo, nil
}

func boundedVectorRejection(reason string) string {
	reason = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(reason))
	runes := []rune(reason)
	if len(runes) > 512 {
		return string(runes[:512]) + "…"
	}
	return reason
}

func LoadSnapshot(path string) (Snapshot, error) {
	snapshot, _, err := LoadSnapshotWithDigest(path)
	return snapshot, err
}

// LoadSnapshotWithDigest decodes a snapshot and returns the SHA-256 digest of
// the exact bytes that were decoded. This avoids integrity metadata being
// computed by reopening a mutable path after validation.
func LoadSnapshotWithDigest(path string) (Snapshot, string, error) {
	if path == "" {
		return Snapshot{}, "", os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, "", err
	}
	snapshot, err := decodeSnapshot(data)
	if err != nil {
		return Snapshot{}, "", err
	}
	return snapshot, model.HashBytes(data), nil
}

func decodeSnapshot(data []byte) (Snapshot, error) {
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
	if snap.Version != indexSnapshotFormatVersion && snap.Version != 5 && snap.Version != 4 {
		return Snapshot{}, fmt.Errorf("read index snapshot: unsupported snapshot version %d", snap.Version)
	}
	snap.GeneratedAt, err = readString(r)
	if err != nil {
		return Snapshot{}, err
	}
	if snap.Version >= 5 {
		snap.IndexSourceHash, err = readString(r)
		if err != nil {
			return Snapshot{}, err
		}
		snap.IndexBuildHash, err = readString(r)
		if err != nil {
			return Snapshot{}, err
		}
		if snap.Version >= 6 {
			snap.CorpusReleaseHash, err = readString(r)
			if err != nil {
				return Snapshot{}, err
			}
		}
		snap.CorpusHash = snap.IndexSourceHash
	} else {
		snap.CorpusHash, err = readString(r)
		if err != nil {
			return Snapshot{}, err
		}
		snap.IndexSourceHash = snap.CorpusHash
	}
	snap.IndexerVersion, err = readString(r)
	if err != nil {
		return Snapshot{}, err
	}
	if snap.IndexBuildHash == "" {
		snap.IndexBuildHash = buildHash(snap.IndexSourceHash, snap.IndexerVersion)
	}
	docCount, err := readU32(r)
	if err != nil {
		return Snapshot{}, err
	}
	if docCount > maxSnapshotDocuments {
		return Snapshot{}, fmt.Errorf("read index snapshot: document count %d exceeds limit", docCount)
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
	if dfCount > maxSnapshotTerms {
		return Snapshot{}, fmt.Errorf("read index snapshot: term count %d exceeds limit", dfCount)
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
	if chunkCount > maxSnapshotChunks {
		return Snapshot{}, fmt.Errorf("read index snapshot: chunk count %d exceeds limit", chunkCount)
	}
	snap.Chunks = make([]SnapshotChunk, 0, chunkCount)
	for range chunkCount {
		chunk, err := readSnapshotChunk(r, snap.Version)
		if err != nil {
			return Snapshot{}, err
		}
		snap.Chunks = append(snap.Chunks, chunk)
	}
	if r.Len() != 0 {
		return Snapshot{}, fmt.Errorf("read index snapshot: trailing data")
	}
	if err := validateSnapshotStructure(snap); err != nil {
		return Snapshot{}, fmt.Errorf("read index snapshot: %w", err)
	}
	return snap, nil
}

func LoadVectorSnapshot(path string) (VectorSnapshot, error) {
	snapshot, _, err := LoadVectorSnapshotWithDigest(path)
	return snapshot, err
}

// LoadVectorSnapshotWithDigest is the vector counterpart of
// LoadSnapshotWithDigest.
func LoadVectorSnapshotWithDigest(path string) (VectorSnapshot, string, error) {
	if path == "" {
		return VectorSnapshot{}, "", os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return VectorSnapshot{}, "", err
	}
	digest := model.HashBytes(data)
	snapshot, err := decodeVectorSnapshot(data)
	if err != nil {
		return VectorSnapshot{}, digest, err
	}
	return snapshot, digest, nil
}

func decodeVectorSnapshot(data []byte) (VectorSnapshot, error) {
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
	if snap.Version != vectorSnapshotFormatVersion && snap.Version != 1 {
		return VectorSnapshot{}, fmt.Errorf("read vector snapshot: unsupported snapshot version %d", snap.Version)
	}
	snap.GeneratedAt, err = readString(r)
	if err != nil {
		return VectorSnapshot{}, err
	}
	if snap.Version >= 2 {
		snap.GenerationID, err = readString(r)
		if err != nil {
			return VectorSnapshot{}, err
		}
		snap.IndexSourceHash, err = readString(r)
		if err != nil {
			return VectorSnapshot{}, err
		}
		snap.IndexBuildHash, err = readString(r)
		if err != nil {
			return VectorSnapshot{}, err
		}
		if snap.Version >= 3 {
			snap.CorpusReleaseHash, err = readString(r)
			if err != nil {
				return VectorSnapshot{}, err
			}
		}
		snap.CorpusHash = snap.IndexSourceHash
	} else {
		snap.CorpusHash, err = readString(r)
		if err != nil {
			return VectorSnapshot{}, err
		}
		snap.IndexSourceHash = snap.CorpusHash
	}
	snap.Model, err = readString(r)
	if err != nil {
		return VectorSnapshot{}, err
	}
	if snap.Version >= 2 {
		snap.ModelRevision, err = readString(r)
		if err != nil {
			return VectorSnapshot{}, err
		}
	}
	dimensions, err := readU32(r)
	if err != nil {
		return VectorSnapshot{}, err
	}
	if dimensions == 0 || dimensions > maxVectorDimensions {
		return VectorSnapshot{}, fmt.Errorf("read vector snapshot: dimensions %d outside limit", dimensions)
	}
	snap.Dimensions = int(dimensions)
	if snap.Version >= 2 {
		snap.QueryPrefix, err = readString(r)
		if err != nil {
			return VectorSnapshot{}, err
		}
		snap.DocumentPrefix, err = readString(r)
		if err != nil {
			return VectorSnapshot{}, err
		}
		scope, err := readString(r)
		if err != nil {
			return VectorSnapshot{}, err
		}
		snap.Scope = VectorScope(scope)
		expectedCount, err := readU32(r)
		if err != nil {
			return VectorSnapshot{}, err
		}
		snap.ExpectedChunkCount = int(expectedCount)
		snap.ChunkIDSetHash, err = readString(r)
		if err != nil {
			return VectorSnapshot{}, err
		}
	}
	docCount, err := readU32(r)
	if err != nil {
		return VectorSnapshot{}, err
	}
	if docCount > maxSnapshotDocuments {
		return VectorSnapshot{}, fmt.Errorf("read vector snapshot: document count %d exceeds limit", docCount)
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
	if vectorCount > maxSnapshotVectors {
		return VectorSnapshot{}, fmt.Errorf("read vector snapshot: vector count %d exceeds limit", vectorCount)
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
		if size > maxVectorDimensions {
			return VectorSnapshot{}, fmt.Errorf("read vector snapshot: vector dimensions %d exceed limit", size)
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
	if r.Len() != 0 {
		return VectorSnapshot{}, fmt.Errorf("read vector snapshot: trailing data")
	}
	if err := validateVectorSnapshotStructure(snap); err != nil {
		return VectorSnapshot{}, fmt.Errorf("read vector snapshot: %w", err)
	}
	return snap, nil
}

func LoadVectorMap(path string, docs []model.Document, attachments map[string]AttachmentDocument) (map[string][]float64, string, error) {
	current, err := snapshotForValidation(docs, attachments)
	if err != nil {
		return nil, "", err
	}
	loaded, err := loadVectorArtifactForSnapshot(path, docs, attachments, current, false)
	return loaded.Vectors, loaded.Reason, err
}

func loadVectorMapForSnapshot(path string, docs []model.Document, attachments map[string]AttachmentDocument, bm25 Snapshot, requireFull bool) (map[string][]float64, VectorSnapshot, string, error) {
	loaded, err := loadVectorArtifactForSnapshot(path, docs, attachments, bm25, requireFull)
	return loaded.Vectors, loaded.Snapshot, loaded.Reason, err
}

type loadedVectorArtifact struct {
	Vectors        map[string][]float64
	Snapshot       VectorSnapshot
	Metadata       VectorMetadata
	ArtifactDigest string
	MetadataDigest string
	Reason         string
}

func loadVectorArtifactForSnapshot(path string, docs []model.Document, attachments map[string]AttachmentDocument, bm25 Snapshot, requireFull bool) (loadedVectorArtifact, error) {
	snap, artifactDigest, vectorErr := LoadVectorSnapshotWithDigest(path)
	result := loadedVectorArtifact{Snapshot: snap, ArtifactDigest: artifactDigest}
	metadata, metadataDigest, metadataErr := LoadVectorMetadataWithDigest(VectorMetadataPath(path))
	result.Metadata = metadata
	result.MetadataDigest = metadataDigest
	if vectorErr != nil {
		return result, vectorErr
	}
	if snap.Version < vectorSnapshotFormatVersion {
		result.Reason = "legacy_vector_format"
		return result, nil
	}
	if snap.IndexSourceHash != bm25.IndexSourceHash {
		result.Reason = "index_source_hash_mismatch"
		return result, nil
	}
	if snap.IndexBuildHash != bm25.IndexBuildHash {
		result.Reason = "index_build_hash_mismatch"
		return result, nil
	}
	if snap.CorpusReleaseHash != bm25.CorpusReleaseHash {
		result.Reason = "corpus_release_hash_mismatch"
		return result, nil
	}
	if !snapshotDocumentsMatch(snap.Documents, docs, attachments) {
		result.Reason = "document_hash_mismatch"
		return result, nil
	}
	expectedIDs := snapshotChunkIDs(bm25.Chunks)
	if snap.ExpectedChunkCount != len(expectedIDs) || snap.ChunkIDSetHash != chunkIDSetHash(expectedIDs) {
		result.Reason = "chunk_coverage_metadata_mismatch"
		return result, nil
	}
	if metadataErr != nil {
		result.Reason = "metadata_load_failed: " + metadataErr.Error()
		return result, nil
	}
	if reason := vectorMetadataRejectReason(metadata, snap); reason != "" {
		result.Reason = reason
		return result, nil
	}
	out := make(map[string][]float64, len(snap.Vectors))
	for _, item := range snap.Vectors {
		out[item.ChunkID] = item.Vector
	}
	result.Vectors = out
	if len(out) == 0 {
		result.Reason = "no_vectors"
		return result, nil
	}
	fullRequired := requireFull || snap.Scope == VectorScopeFull
	if err := ValidateVectorMap(out, expectedIDs, snap.Dimensions, fullRequired); err != nil {
		result.Vectors = nil
		result.Reason = "vector_validation_failed: " + err.Error()
		return result, nil
	}
	if requireFull && snap.Scope != VectorScopeFull {
		result.Vectors = nil
		result.Reason = "full_vector_required"
		return result, nil
	}
	return result, nil
}

func vectorMetadataMatches(metadata VectorMetadata, snap VectorSnapshot) bool {
	return vectorMetadataRejectReason(metadata, snap) == ""
}

func vectorMetadataRejectReason(metadata VectorMetadata, snap VectorSnapshot) string {
	if reason := vectorMetadataRejectReasonWithoutEnvironment(metadata, snap); reason != "" {
		return reason
	}
	embedder, err := newOpenAIEmbedderFromEnv()
	if err != nil {
		return "embedding_config_invalid: " + err.Error()
	}
	switch {
	case metadata.Model != embedder.Model:
		return "embedding_model_mismatch"
	case metadata.ModelRevision != embedder.ModelRevision:
		return "embedding_model_revision_mismatch"
	case metadata.Dimensions != embedder.Dimensions:
		return "embedding_dimensions_mismatch"
	case metadata.QueryPrefix != envDefaultPreserveSpace("KRX_EMBEDDING_QUERY_PREFIX", "query: "):
		return "embedding_query_prefix_mismatch"
	case metadata.DocumentPrefix != envDefaultPreserveSpace("KRX_EMBEDDING_DOCUMENT_PREFIX", "passage: "):
		return "embedding_document_prefix_mismatch"
	default:
		return ""
	}
}

func vectorMetadataRejectReasonWithoutEnvironment(metadata VectorMetadata, snap VectorSnapshot) string {
	switch {
	case metadata.Version != VectorMetadataFormatVersion:
		return "metadata_version_mismatch"
	case metadata.GenerationID != snap.GenerationID:
		return "metadata_generation_mismatch"
	case metadata.IndexSourceHash != snap.IndexSourceHash:
		return "metadata_index_source_hash_mismatch"
	case metadata.IndexBuildHash != snap.IndexBuildHash:
		return "metadata_index_build_hash_mismatch"
	case metadata.CorpusReleaseHash != snap.CorpusReleaseHash:
		return "metadata_corpus_release_hash_mismatch"
	case metadata.Model != snap.Model:
		return "metadata_snapshot_model_mismatch"
	case metadata.ModelRevision != snap.ModelRevision:
		return "metadata_snapshot_model_revision_mismatch"
	case metadata.Dimensions != snap.Dimensions:
		return "metadata_snapshot_dimensions_mismatch"
	case metadata.QueryPrefix != snap.QueryPrefix:
		return "metadata_snapshot_query_prefix_mismatch"
	case metadata.DocumentPrefix != snap.DocumentPrefix:
		return "metadata_snapshot_document_prefix_mismatch"
	case metadata.Scope != snap.Scope:
		return "metadata_scope_mismatch"
	case metadata.ExpectedChunkCount != snap.ExpectedChunkCount:
		return "metadata_expected_chunk_count_mismatch"
	case metadata.StoredVectorCount != len(snap.Vectors):
		return "metadata_stored_vector_count_mismatch"
	case metadata.ChunkIDSetHash != snap.ChunkIDSetHash:
		return "metadata_chunk_id_set_hash_mismatch"
	case metadata.StoredChunkIDSetHash != vectorSnapshotIDSetHash(snap):
		return "metadata_stored_chunk_id_set_hash_mismatch"
	default:
		return ""
	}
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
			ArticleID:        item.ArticleID,
			HeadingPath:      append([]string(nil), item.HeadingPath...),
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
	if snap.Version != indexSnapshotFormatVersion || snap.IndexerVersion != indexerVersion {
		return false
	}
	// Freshness is fully determined by the canonical source/build hashes and
	// per-document identities already stored in the snapshot. Rebuilding all
	// chunks here would duplicate the runtime token index during startup.
	indexSourceHash, err := corpus.IndexSourceHash(docs, attachmentTextMap(attachments))
	if err != nil {
		return false
	}
	documents, err := snapshotDocuments(docs, attachments)
	if err != nil {
		return false
	}
	return snap.IndexSourceHash == indexSourceHash &&
		snap.IndexBuildHash == buildHash(indexSourceHash, indexerVersion) &&
		snapshotDocumentsEqual(snap.Documents, documents)
}

func snapshotDocumentsMatch(snapshotDocs []SnapshotDocument, docs []model.Document, attachments map[string]AttachmentDocument) bool {
	want, err := snapshotDocuments(docs, attachments)
	return err == nil && snapshotDocumentsEqual(snapshotDocs, want)
}

func snapshotDocumentsEqual(got, want []SnapshotDocument) bool {
	if len(got) != len(want) {
		return false
	}
	wantByID := make(map[string]SnapshotDocument, len(want))
	for _, document := range want {
		wantByID[document.ID] = document
	}
	for _, document := range got {
		expected, ok := wantByID[document.ID]
		if !ok || expected.IndexHash != document.IndexHash || expected.ContentHash != document.ContentHash {
			return false
		}
	}
	return true
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

func readSnapshotChunk(r *bytes.Reader, version uint16) (SnapshotChunk, error) {
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
	articleID := ""
	var headingPath []string
	if version >= 6 {
		articleID, err = readString(r)
		if err != nil {
			return SnapshotChunk{}, err
		}
		headingCount, err := readU32(r)
		if err != nil {
			return SnapshotChunk{}, err
		}
		if headingCount > 64 {
			return SnapshotChunk{}, fmt.Errorf("heading path depth %d exceeds limit", headingCount)
		}
		headingPath = make([]string, 0, headingCount)
		for range headingCount {
			heading, err := readString(r)
			if err != nil {
				return SnapshotChunk{}, err
			}
			headingPath = append(headingPath, heading)
		}
	} else {
		// v4/v5 stored the untrusted article_range field here. Consume it for
		// binary compatibility, but never expose or use it as an anchor.
		if _, err := readString(r); err != nil {
			return SnapshotChunk{}, err
		}
	}
	tokenCount, err := readU32(r)
	if err != nil {
		return SnapshotChunk{}, err
	}
	if tokenCount > maxChunkTokens {
		return SnapshotChunk{}, fmt.Errorf("chunk token count %d exceeds limit", tokenCount)
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
		ArticleID:        articleID,
		HeadingPath:      headingPath,
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
	limited := io.LimitReader(gz, maxSnapshotPayloadBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(payload) > maxSnapshotPayloadBytes {
		return nil, fmt.Errorf("decompressed snapshot exceeds %d bytes", maxSnapshotPayloadBytes)
	}
	return payload, nil
}

func parseSnapshotTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}
