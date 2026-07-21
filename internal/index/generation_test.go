package index

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromato99/krx-rule-mcp/internal/corpus"
	"github.com/chromato99/krx-rule-mcp/internal/model"
	"gopkg.in/yaml.v3"
)

func TestGenerationBuildLockIsSingleWriter(t *testing.T) {
	indexDir := t.TempDir()
	first, err := AcquireGenerationBuildLock(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireGenerationBuildLock(indexDir); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("second lock error = %v, want active-writer rejection", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireGenerationBuildLock(indexDir)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationPublishAndReadCurrent(t *testing.T) {
	indexDir := t.TempDir()
	lock, err := AcquireGenerationBuildLock(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	build := GenerationBuild{Snapshot: generationTestSnapshot(t)}
	first, err := lock.Publish(build)
	if err != nil {
		t.Fatal(err)
	}
	second, err := lock.Publish(build)
	if err != nil {
		t.Fatalf("republish identical generation: %v", err)
	}
	if second != first {
		t.Fatalf("republish returned transient descriptor:\nfirst=%#v\nsecond=%#v", first, second)
	}
	current, dir, err := ReadCurrentGeneration(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	if current != first || filepath.Base(dir) != first.GenerationID {
		t.Fatalf("current = %#v, %q; want %#v", current, dir, first)
	}
	loaded, digest, err := LoadSnapshotWithDigest(filepath.Join(dir, BM25SnapshotFile))
	if err != nil {
		t.Fatal(err)
	}
	if digest != first.BM25.SHA256 || loaded.CorpusReleaseHash != build.Snapshot.CorpusReleaseHash {
		t.Fatalf("loaded digest/release = %s/%s", digest, loaded.CorpusReleaseHash)
	}
}

func TestGenerationFailedBuildPreservesCurrent(t *testing.T) {
	indexDir := t.TempDir()
	lock, err := AcquireGenerationBuildLock(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	good := GenerationBuild{Snapshot: generationTestSnapshot(t)}
	first, err := lock.Publish(good)
	if err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.IncludeVector = true
	bad.VectorModel = "test-model"
	bad.VectorDimensions = 2
	bad.VectorOptions = VectorWriteOptions{Scope: VectorScopeFull}
	if _, err := lock.Publish(bad); err == nil {
		t.Fatal("invalid full vector build unexpectedly published")
	}
	current, _, err := ReadCurrentGeneration(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	if current.GenerationID != first.GenerationID {
		t.Fatalf("current generation changed after failure: got %s want %s", current.GenerationID, first.GenerationID)
	}
}

func TestGenerationRejectsCorruptExistingGenerationWithoutPointerChange(t *testing.T) {
	indexDir := t.TempDir()
	lock, err := AcquireGenerationBuildLock(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	build := GenerationBuild{Snapshot: generationTestSnapshot(t)}
	published, err := lock.Publish(build)
	if err != nil {
		t.Fatal(err)
	}
	pointerBefore, err := os.ReadFile(filepath.Join(indexDir, CurrentGenerationFile))
	if err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(indexDir, GenerationsDirectory, published.GenerationID, BM25SnapshotFile)
	if err := os.WriteFile(artifactPath, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Publish(build); err == nil || !strings.Contains(err.Error(), "existing generation") {
		t.Fatalf("republish corrupt existing error = %v", err)
	}
	pointerAfter, err := os.ReadFile(filepath.Join(indexDir, CurrentGenerationFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(pointerAfter) != string(pointerBefore) {
		t.Fatalf("pointer changed after corrupt-generation failure: before=%q after=%q", pointerBefore, pointerAfter)
	}
}

func TestReadCurrentGenerationRejectsSymlinkGenerationDirectory(t *testing.T) {
	indexDir := t.TempDir()
	lock, err := AcquireGenerationBuildLock(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := lock.Publish(GenerationBuild{Snapshot: generationTestSnapshot(t)})
	if err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	generationDir := filepath.Join(indexDir, GenerationsDirectory, descriptor.GenerationID)
	relocated := filepath.Join(t.TempDir(), descriptor.GenerationID)
	if err := os.Rename(generationDir, relocated); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relocated, generationDir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadCurrentGeneration(indexDir); err == nil || !strings.Contains(err.Error(), "regular directory") {
		t.Fatalf("ReadCurrentGeneration symlink error = %v", err)
	}
}

func TestReadCurrentGenerationRejectsSymlinkDescriptor(t *testing.T) {
	indexDir := t.TempDir()
	lock, err := AcquireGenerationBuildLock(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := lock.Publish(GenerationBuild{Snapshot: generationTestSnapshot(t)})
	if err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	descriptorPath := filepath.Join(indexDir, GenerationsDirectory, descriptor.GenerationID, GenerationDescriptorFile)
	relocated := filepath.Join(t.TempDir(), GenerationDescriptorFile)
	if err := os.Rename(descriptorPath, relocated); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relocated, descriptorPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadCurrentGeneration(indexDir); err == nil || !strings.Contains(err.Error(), "descriptor file") {
		t.Fatalf("ReadCurrentGeneration descriptor symlink error = %v", err)
	}
}

func TestGenerationPublishesValidatedVectorCompanion(t *testing.T) {
	indexDir := t.TempDir()
	lock, err := AcquireGenerationBuildLock(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	snapshot := generationTestSnapshot(t)
	vectors := make(map[string][]float64, len(snapshot.Chunks))
	for _, chunk := range snapshot.Chunks {
		vectors[chunk.ID] = []float64{1, 0}
	}
	descriptor, err := lock.Publish(GenerationBuild{
		Snapshot:         snapshot,
		IncludeVector:    true,
		Vectors:          vectors,
		VectorModel:      "test-model",
		VectorDimensions: 2,
		VectorOptions: VectorWriteOptions{
			Scope:          VectorScopeFull,
			ModelRevision:  "test-revision",
			QueryPrefix:    "query: ",
			DocumentPrefix: "passage: ",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Vector == nil || descriptor.Vector.Vectors != len(snapshot.Chunks) {
		t.Fatalf("vector descriptor = %#v", descriptor.Vector)
	}
	current, dir, err := ReadCurrentGeneration(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	if current.Vector == nil {
		t.Fatal("current generation lost vector descriptor")
	}
	vector, vectorDigest, err := LoadVectorSnapshotWithDigest(filepath.Join(dir, VectorSnapshotFile))
	if err != nil {
		t.Fatal(err)
	}
	metadata, metadataDigest, err := LoadVectorMetadataWithDigest(filepath.Join(dir, VectorSnapshotFile+VectorMetadataSuffix))
	if err != nil {
		t.Fatal(err)
	}
	if vectorDigest != current.Vector.Artifact.SHA256 || metadataDigest != current.Vector.Metadata.SHA256 {
		t.Fatalf("vector artifact digest mismatch")
	}
	if reason := vectorMetadataRejectReasonWithoutEnvironment(metadata, vector); reason != "" {
		t.Fatalf("vector metadata rejected: %s", reason)
	}
}

func TestLoadRepositoryGenerationUsesManifestAndFixedArtifactDigests(t *testing.T) {
	t.Setenv("KRX_EMBEDDING_MODEL", "test-model")
	t.Setenv("KRX_EMBEDDING_MODEL_REVISION", "test-revision")
	t.Setenv("KRX_EMBEDDING_DIMENSIONS", "2")
	dataRoot := t.TempDir()
	doc := generationTestDocument()
	doc.SchemaVersion = 2
	doc.PreservationStatus = "preserved"
	documentPath := writeIndexTestDocument(t, dataRoot, doc)
	releaseHash := writeGenerationTestManifest(t, dataRoot, documentPath)
	snapshot, _, err := BuildReleaseSnapshot(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CorpusReleaseHash != releaseHash {
		t.Fatalf("snapshot release = %s, want %s", snapshot.CorpusReleaseHash, releaseHash)
	}
	vectors := make(map[string][]float64, len(snapshot.Chunks))
	for _, chunk := range snapshot.Chunks {
		vectors[chunk.ID] = []float64{1, 0}
	}
	indexDir := filepath.Join(t.TempDir(), "index")
	lock, err := AcquireGenerationBuildLock(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := lock.Publish(GenerationBuild{
		Snapshot:         snapshot,
		IncludeVector:    true,
		Vectors:          vectors,
		VectorModel:      "test-model",
		VectorDimensions: 2,
		VectorOptions: VectorWriteOptions{
			Scope:          VectorScopeFull,
			ModelRevision:  "test-revision",
			QueryPrefix:    "query: ",
			DocumentPrefix: "passage: ",
		},
	})
	if err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	repository, err := LoadRepositoryGeneration(dataRoot, indexDir, RepositoryLoadOptions{
		VectorEnabled: true,
		RequireVector: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if repository.GenerationID != descriptor.GenerationID || repository.GenerationDir != filepath.Join(indexDir, GenerationsDirectory, descriptor.GenerationID) {
		t.Fatalf("repository generation identity = %q %q", repository.GenerationID, repository.GenerationDir)
	}
	if repository.BM25ArtifactDigest != descriptor.BM25.SHA256 || repository.BM25SnapshotVersion != indexSnapshotFormatVersion || repository.IndexerVersion != snapshot.IndexerVersion {
		t.Fatalf("repository fixed BM25 metadata = %#v", repository)
	}
	if len(repository.VectorIndexes) != 1 || repository.VectorIndexes[0].ArtifactDigest != descriptor.Vector.Artifact.SHA256 || repository.VectorIndexes[0].MetadataDigest != descriptor.Vector.Metadata.SHA256 {
		t.Fatalf("repository fixed vector metadata = %#v", repository.VectorIndexes)
	}
}

func TestLoadRepositoryGenerationMalformedOptionalVectorFallsBack(t *testing.T) {
	dataRoot := t.TempDir()
	doc := generationTestDocument()
	doc.SchemaVersion = 2
	doc.PreservationStatus = "preserved"
	documentPath := writeIndexTestDocument(t, dataRoot, doc)
	writeGenerationTestManifest(t, dataRoot, documentPath)
	snapshot, _, err := BuildReleaseSnapshot(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	vectors := make(map[string][]float64, len(snapshot.Chunks))
	for _, chunk := range snapshot.Chunks {
		vectors[chunk.ID] = []float64{1, 0}
	}
	indexDir := filepath.Join(t.TempDir(), "index")
	lock, err := AcquireGenerationBuildLock(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := lock.Publish(GenerationBuild{
		Snapshot: snapshot, IncludeVector: true, Vectors: vectors,
		VectorModel: "test-model", VectorDimensions: 2,
		VectorOptions: VectorWriteOptions{Scope: VectorScopeFull, ModelRevision: "test-revision"},
	})
	if closeErr := lock.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}

	oldDir := filepath.Join(indexDir, GenerationsDirectory, descriptor.GenerationID)
	vectorPath := filepath.Join(oldDir, VectorSnapshotFile)
	if err := os.WriteFile(vectorPath, []byte("KRXVEC2\nmalformed-gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	descriptor.Vector.Artifact, err = describeArtifact(vectorPath, VectorSnapshotFile)
	if err != nil {
		t.Fatal(err)
	}
	newID, err := generationDescriptorID(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	descriptor.GenerationID = newID
	if err := writeGenerationDescriptor(filepath.Join(oldDir, GenerationDescriptorFile), descriptor); err != nil {
		t.Fatal(err)
	}
	newDir := filepath.Join(indexDir, GenerationsDirectory, newID)
	if err := os.Rename(oldDir, newDir); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(indexDir, CurrentGenerationFile), []byte(newID+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repository, err := LoadRepositoryGeneration(dataRoot, indexDir, RepositoryLoadOptions{VectorEnabled: true})
	if err != nil {
		t.Fatalf("optional vector generation must fall back: %v", err)
	}
	if repository.Engine.HasVectors() || repository.VectorPath != "" || len(repository.VectorIndexes) != 1 {
		t.Fatalf("optional malformed vector was adopted: %#v", repository.VectorIndexes)
	}
	status := repository.VectorIndexes[0]
	if !strings.HasPrefix(status.RejectedReason, "load_failed:") || status.ArtifactDigest != descriptor.Vector.Artifact.SHA256 || status.MetadataDigest != descriptor.Vector.Metadata.SHA256 {
		t.Fatalf("optional malformed vector status = %#v", status)
	}
	if len([]rune(status.RejectedReason)) > 513 {
		t.Fatalf("unbounded vector rejection reason: %d", len([]rune(status.RejectedReason)))
	}
	if _, err := LoadRepositoryGeneration(dataRoot, indexDir, RepositoryLoadOptions{VectorEnabled: true, RequireVector: true}); err == nil {
		t.Fatal("required malformed vector generation unexpectedly loaded")
	}
}

func generationTestSnapshot(t *testing.T) Snapshot {
	t.Helper()
	root := t.TempDir()
	writeIndexTestDocument(t, root, generationTestDocument())
	snapshot, _, err := BuildSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.CorpusReleaseHash = strings.Repeat("a", 64)
	return snapshot
}

func generationTestDocument() model.Document {
	return model.Document{
		ID:           "generation-rule",
		Title:        "세대 게시 검증 규정",
		SourceURL:    "https://example.test/generation-rule",
		CollectedAt:  time.Now().UTC(),
		ContentHash:  "render-helper-replaces-this",
		DocumentType: model.DocumentTypeRule,
		Body:         "제1조(세대 게시) 원자적 세대 게시의 검증 기준을 정한다.",
	}
}

func writeGenerationTestManifest(t *testing.T, root, documentPath string) string {
	t.Helper()
	loaded, err := corpus.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	indexHash, err := corpus.IndexSourceHash(loaded.Documents, loaded.AttachmentTexts)
	if err != nil {
		t.Fatal(err)
	}
	markdown, err := os.ReadFile(documentPath)
	if err != nil {
		t.Fatal(err)
	}
	rest := markdown[len("---\n"):]
	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		t.Fatal("test document has no frontmatter terminator")
	}
	var frontmatter map[string]any
	if err := yaml.Unmarshal(rest[:end], &frontmatter); err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(root, documentPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestDocument := make(map[string]any, len(frontmatter)+1)
	for key, value := range frontmatter {
		manifestDocument[key] = value
	}
	manifestDocument["path"] = filepath.ToSlash(relative)
	payload := map[string]any{
		"schema_version":    2,
		"version":           "generation-test",
		"release_profile":   map[string]any{"version": 1, "default": "strict", "allowed_failure_ids": []any{}},
		"documents":         []any{manifestDocument},
		"index_source_hash": indexHash,
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	releaseHash := model.HashBytes(canonical)
	payload["release_hash"] = releaseHash
	payload["generated_at"] = "2026-07-10T00:00:00Z"
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return releaseHash
}
