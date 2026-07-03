package index

import "path/filepath"

const (
	DefaultIndexDir      = "index"
	BM25SnapshotFile     = "bm25.krxidx"
	VectorSnapshotFile   = "vectors.krxvec"
	VectorMetadataSuffix = ".meta.json"
)

func DefaultBM25Path(indexDir string) string {
	return filepath.Join(indexDir, BM25SnapshotFile)
}

func DefaultVectorPath(indexDir string) string {
	return filepath.Join(indexDir, VectorSnapshotFile)
}
