package index

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/model"
)

const (
	GenerationDescriptorVersion = 1
	GenerationsDirectory        = "generations"
	CurrentGenerationFile       = "current"
	GenerationDescriptorFile    = "generation.json"
	generationBuildLockFile     = ".generation-build.lock"
	maxGenerationDescriptorSize = 1 << 20
)

var generationIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type ArtifactDescriptor struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type VectorGenerationDescriptor struct {
	Artifact ArtifactDescriptor `json:"artifact"`
	Metadata ArtifactDescriptor `json:"metadata"`
	Scope    VectorScope        `json:"scope"`
	Model    string             `json:"model"`
	Revision string             `json:"revision,omitempty"`
	Vectors  int                `json:"vectors"`
}

type GenerationDescriptor struct {
	Version           int                         `json:"version"`
	GenerationID      string                      `json:"generation_id"`
	CreatedAt         string                      `json:"created_at"`
	CorpusReleaseHash string                      `json:"corpus_release_hash"`
	IndexSourceHash   string                      `json:"index_source_hash"`
	IndexBuildHash    string                      `json:"index_build_hash"`
	BM25              ArtifactDescriptor          `json:"bm25"`
	Vector            *VectorGenerationDescriptor `json:"vector,omitempty"`
}

type GenerationBuild struct {
	Snapshot         Snapshot
	IncludeVector    bool
	Vectors          map[string][]float64
	VectorModel      string
	VectorDimensions int
	VectorOptions    VectorWriteOptions
}

type GenerationBuildLock struct {
	file     *os.File
	indexDir string
}

func AcquireGenerationBuildLock(indexDir string) (*GenerationBuildLock, error) {
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return nil, fmt.Errorf("create index directory: %w", err)
	}
	path := filepath.Join(indexDir, generationBuildLockFile)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open generation build lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another index generation build is already active")
		}
		return nil, fmt.Errorf("lock generation build: %w", err)
	}
	return &GenerationBuildLock{file: file, indexDir: indexDir}, nil
}

func (lock *GenerationBuildLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	closeErr := file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func (lock *GenerationBuildLock) Publish(build GenerationBuild) (GenerationDescriptor, error) {
	if lock == nil || lock.file == nil {
		return GenerationDescriptor{}, fmt.Errorf("generation build lock is not held")
	}
	snap := build.Snapshot
	normalizeSnapshotHashes(&snap)
	if snap.CorpusReleaseHash == "" {
		return GenerationDescriptor{}, fmt.Errorf("generation publish requires corpus release hash")
	}
	if err := validateSnapshotStructure(snap); err != nil {
		return GenerationDescriptor{}, fmt.Errorf("validate generation BM25 snapshot: %w", err)
	}
	generationsDir := filepath.Join(lock.indexDir, GenerationsDirectory)
	if err := os.MkdirAll(generationsDir, 0o755); err != nil {
		return GenerationDescriptor{}, err
	}
	staging, err := os.MkdirTemp(generationsDir, ".staging-")
	if err != nil {
		return GenerationDescriptor{}, fmt.Errorf("create generation staging directory: %w", err)
	}
	cleanupStaging := true
	defer func() {
		if cleanupStaging {
			_ = os.RemoveAll(staging)
		}
	}()

	bm25Path := filepath.Join(staging, BM25SnapshotFile)
	if err := WriteSnapshot(bm25Path, snap); err != nil {
		return GenerationDescriptor{}, err
	}
	loadedBM25, err := LoadSnapshot(bm25Path)
	if err != nil {
		return GenerationDescriptor{}, fmt.Errorf("validate staged BM25 snapshot: %w", err)
	}
	if loadedBM25.IndexBuildHash != snap.IndexBuildHash || loadedBM25.IndexSourceHash != snap.IndexSourceHash || loadedBM25.CorpusReleaseHash != snap.CorpusReleaseHash {
		return GenerationDescriptor{}, fmt.Errorf("staged BM25 snapshot identity mismatch")
	}
	bm25Artifact, err := describeArtifact(bm25Path, BM25SnapshotFile)
	if err != nil {
		return GenerationDescriptor{}, err
	}
	descriptor := GenerationDescriptor{
		Version:           GenerationDescriptorVersion,
		CreatedAt:         time.Now().UTC().Format(time.RFC3339Nano),
		CorpusReleaseHash: snap.CorpusReleaseHash,
		IndexSourceHash:   snap.IndexSourceHash,
		IndexBuildHash:    snap.IndexBuildHash,
		BM25:              bm25Artifact,
	}

	if build.IncludeVector {
		vectorPath := filepath.Join(staging, VectorSnapshotFile)
		if err := WriteVectorSnapshot(vectorPath, snap, build.Vectors, build.VectorModel, build.VectorDimensions, build.VectorOptions); err != nil {
			return GenerationDescriptor{}, err
		}
		metadata := BuildVectorMetadata(snap, build.Vectors, build.VectorModel, build.VectorDimensions, build.VectorOptions)
		metadataPath := VectorMetadataPath(vectorPath)
		if err := WriteVectorMetadata(metadataPath, metadata); err != nil {
			return GenerationDescriptor{}, err
		}
		vectorSnapshot, err := LoadVectorSnapshot(vectorPath)
		if err != nil {
			return GenerationDescriptor{}, fmt.Errorf("validate staged vector snapshot: %w", err)
		}
		loadedMetadata, err := LoadVectorMetadata(metadataPath)
		if err != nil {
			return GenerationDescriptor{}, fmt.Errorf("validate staged vector metadata: %w", err)
		}
		if reason := vectorMetadataRejectReasonWithoutEnvironment(loadedMetadata, vectorSnapshot); reason != "" {
			return GenerationDescriptor{}, fmt.Errorf("validate staged vector metadata: %s", reason)
		}
		vectorArtifact, err := describeArtifact(vectorPath, VectorSnapshotFile)
		if err != nil {
			return GenerationDescriptor{}, err
		}
		metadataArtifact, err := describeArtifact(metadataPath, VectorSnapshotFile+VectorMetadataSuffix)
		if err != nil {
			return GenerationDescriptor{}, err
		}
		descriptor.Vector = &VectorGenerationDescriptor{
			Artifact: vectorArtifact,
			Metadata: metadataArtifact,
			Scope:    vectorSnapshot.Scope,
			Model:    vectorSnapshot.Model,
			Revision: vectorSnapshot.ModelRevision,
			Vectors:  len(vectorSnapshot.Vectors),
		}
	}

	descriptor.GenerationID, err = generationDescriptorID(descriptor)
	if err != nil {
		return GenerationDescriptor{}, err
	}
	descriptorPath := filepath.Join(staging, GenerationDescriptorFile)
	if err := writeGenerationDescriptor(descriptorPath, descriptor); err != nil {
		return GenerationDescriptor{}, err
	}
	if err := syncDirectory(staging); err != nil {
		return GenerationDescriptor{}, err
	}
	finalDir := filepath.Join(generationsDir, descriptor.GenerationID)
	if _, err := os.Stat(finalDir); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(staging, finalDir); err != nil {
			return GenerationDescriptor{}, fmt.Errorf("publish generation directory: %w", err)
		}
		cleanupStaging = false
		if err := syncDirectory(generationsDir); err != nil {
			return GenerationDescriptor{}, err
		}
	} else if err != nil {
		return GenerationDescriptor{}, err
	} else {
		existing, err := validateGenerationDirectory(finalDir, descriptor.GenerationID)
		if err != nil {
			return GenerationDescriptor{}, fmt.Errorf("existing generation %s is invalid: %w", descriptor.GenerationID, err)
		}
		if err := os.RemoveAll(staging); err != nil {
			return GenerationDescriptor{}, err
		}
		cleanupStaging = false
		// A content-addressed generation may already exist from an earlier
		// successful build. Preserve its on-disk creation timestamp and return
		// exactly the descriptor that was validated, rather than the transient
		// descriptor assembled by this process.
		descriptor = existing
	}
	if err := writeFileAtomic(filepath.Join(lock.indexDir, CurrentGenerationFile), []byte(descriptor.GenerationID+"\n"), 0o644); err != nil {
		return GenerationDescriptor{}, fmt.Errorf("publish current generation pointer: %w", err)
	}
	return descriptor, nil
}

func ReadCurrentGeneration(indexDir string) (GenerationDescriptor, string, error) {
	pointerPath := filepath.Join(indexDir, CurrentGenerationFile)
	pointerInfo, err := os.Lstat(pointerPath)
	if err != nil {
		return GenerationDescriptor{}, "", err
	}
	if !pointerInfo.Mode().IsRegular() || pointerInfo.Size() > 128 {
		return GenerationDescriptor{}, "", fmt.Errorf("invalid current generation pointer file")
	}
	pointer, err := os.ReadFile(pointerPath)
	if err != nil {
		return GenerationDescriptor{}, "", err
	}
	id := string(bytes.TrimSpace(pointer))
	if !generationIDPattern.MatchString(id) {
		return GenerationDescriptor{}, "", fmt.Errorf("invalid current generation id %q", id)
	}
	dir := filepath.Join(indexDir, GenerationsDirectory, id)
	descriptor, err := validateGenerationDirectory(dir, id)
	if err != nil {
		return GenerationDescriptor{}, "", fmt.Errorf("validate current generation %q: %w", id, err)
	}
	return descriptor, dir, nil
}

func validateGenerationDirectory(dir, expectedID string) (GenerationDescriptor, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return GenerationDescriptor{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return GenerationDescriptor{}, fmt.Errorf("generation path is not a regular directory")
	}
	descriptor, err := readGenerationDescriptor(filepath.Join(dir, GenerationDescriptorFile))
	if err != nil {
		return GenerationDescriptor{}, err
	}
	if descriptor.GenerationID != expectedID {
		return GenerationDescriptor{}, fmt.Errorf("generation descriptor id %q does not match expected id %q", descriptor.GenerationID, expectedID)
	}
	want, err := generationDescriptorID(descriptor)
	if err != nil {
		return GenerationDescriptor{}, err
	}
	if want != expectedID {
		return GenerationDescriptor{}, fmt.Errorf("generation descriptor hash mismatch")
	}
	if err := validateArtifactDescriptor(dir, descriptor.BM25, BM25SnapshotFile); err != nil {
		return GenerationDescriptor{}, fmt.Errorf("BM25 artifact: %w", err)
	}
	if descriptor.Vector != nil {
		if descriptor.Vector.Vectors < 0 {
			return GenerationDescriptor{}, fmt.Errorf("negative vector count")
		}
		if err := validateArtifactDescriptor(dir, descriptor.Vector.Artifact, VectorSnapshotFile); err != nil {
			return GenerationDescriptor{}, fmt.Errorf("vector artifact: %w", err)
		}
		if err := validateArtifactDescriptor(dir, descriptor.Vector.Metadata, VectorSnapshotFile+VectorMetadataSuffix); err != nil {
			return GenerationDescriptor{}, fmt.Errorf("vector metadata artifact: %w", err)
		}
	}
	return descriptor, nil
}

func validateArtifactDescriptor(dir string, descriptor ArtifactDescriptor, expectedFile string) error {
	if descriptor.File != expectedFile {
		return fmt.Errorf("file %q must be %q", descriptor.File, expectedFile)
	}
	if !generationIDPattern.MatchString(descriptor.SHA256) {
		return fmt.Errorf("invalid SHA-256 digest %q", descriptor.SHA256)
	}
	if descriptor.Size < 0 {
		return fmt.Errorf("negative artifact size")
	}
	actual, err := describeArtifact(filepath.Join(dir, expectedFile), expectedFile)
	if err != nil {
		return err
	}
	if actual.Size != descriptor.Size {
		return fmt.Errorf("size mismatch: descriptor=%d actual=%d", descriptor.Size, actual.Size)
	}
	if actual.SHA256 != descriptor.SHA256 {
		return fmt.Errorf("SHA-256 mismatch: descriptor=%s actual=%s", descriptor.SHA256, actual.SHA256)
	}
	return nil
}

func describeArtifact(path, name string) (ArtifactDescriptor, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return ArtifactDescriptor{}, err
	}
	if !info.Mode().IsRegular() {
		return ArtifactDescriptor{}, fmt.Errorf("artifact %q is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return ArtifactDescriptor{}, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return ArtifactDescriptor{}, err
	}
	return ArtifactDescriptor{File: name, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size}, nil
}

func generationDescriptorID(descriptor GenerationDescriptor) (string, error) {
	payload := map[string]any{
		"version":             descriptor.Version,
		"corpus_release_hash": descriptor.CorpusReleaseHash,
		"index_source_hash":   descriptor.IndexSourceHash,
		"index_build_hash":    descriptor.IndexBuildHash,
		"bm25":                descriptor.BM25,
		"vector":              descriptor.Vector,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return model.HashBytes(data), nil
}

func writeGenerationDescriptor(path string, descriptor GenerationDescriptor) error {
	data, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0o644)
}

func readGenerationDescriptor(path string) (GenerationDescriptor, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return GenerationDescriptor{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxGenerationDescriptorSize {
		return GenerationDescriptor{}, fmt.Errorf("invalid generation descriptor file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return GenerationDescriptor{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var descriptor GenerationDescriptor
	if err := decoder.Decode(&descriptor); err != nil {
		return GenerationDescriptor{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = fmt.Errorf("unexpected second JSON value")
		}
		return GenerationDescriptor{}, fmt.Errorf("generation descriptor trailing data: %w", err)
	}
	if descriptor.Version != GenerationDescriptorVersion || !generationIDPattern.MatchString(descriptor.GenerationID) {
		return GenerationDescriptor{}, fmt.Errorf("invalid generation descriptor")
	}
	return descriptor, nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
