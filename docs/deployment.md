# Deployment

## Docker Compose

The root `compose.yaml` is the recommended Docker-only runtime. It starts:

- `krx-rule-mcp`: Go HTTP MCP server.
- `krx-rule-embeddings`: Hugging Face Text Embeddings Inference sidecar.

Prepare a corpus with `krx-rule-markdown`, copy it to a host path, and point Compose at that corpus plus a matching index directory.
This repository does not contain a generated corpus, but the source checkout does include default BM25/vector snapshots under root-level `index/`.
Use that bundled `index/` when it matches your mounted corpus, or generate a separate `KRX_RULE_INDEX_DIR` for your deployment.
The server image does not contain corpus or index files, so both paths are still supplied as volumes.
Do not start the server until `$KRX_RULE_INDEX_DIR/bm25.krxidx` exists and matches the corpus; otherwise startup fails while loading the repository.

```bash
cp .env.compose.example .env
vi .env  # set KRX_RULE_DATA_DIR, KRX_RULE_INDEX_DIR, and KRX_MCP_BEARER_TOKEN
# KRX_RULE_INDEX_DIR may point to this repository's ./index if it matches KRX_RULE_DATA_DIR.
# Otherwise build at least the required BM25 snapshot before starting the server.
# See "Manual Index Jobs" below for local and container commands.
docker compose up -d --build
curl http://localhost:8080/healthz
```

`KRX_RULE_DATA_DIR` is mounted read-only at `/app/data`; `KRX_RULE_INDEX_DIR` is mounted read-only at `/app/index`. The server image does not contain corpus or index files, but it does include the default domain lexicon at `/app/config/domain-lexicon.yaml`.
The host directories must be readable by the non-root container user. For local smoke tests with temporary directories, run `chmod -R a+rX "$KRX_RULE_DATA_DIR" "$KRX_RULE_INDEX_DIR"` after creating the corpus and indexes.

## Manual Index Jobs

Index generation is explicit and can run locally or inside the server image.
Run it whenever the corpus changes, the indexer changes, or the repository-provided `index/` no longer matches your corpus.

To verify the repository-provided snapshots:

```bash
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir ./index \
  --vector-index ./index/vectors.krxvec \
  --check
```

Local:

```bash
export KRX_RULE_INDEX_DIR=/opt/krx-rule-index
mkdir -p "$KRX_RULE_INDEX_DIR"

go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR"
```

Container:

```bash
docker build -f deploy/docker/Dockerfile --target server -t krx-rule-mcp:local .
docker run --rm \
  --entrypoint /usr/local/bin/krx-rule-index \
  --user "$(id -u):$(id -g)" \
  -v "$KRX_RULE_DATA_DIR:/app/data:ro" \
  -v "$KRX_RULE_INDEX_DIR:/app/index" \
  krx-rule-mcp:local \
  --data-dir /app/data --index-dir /app/index
```

To build vectors with the compose TEI sidecar:

```bash
docker compose up -d krx-rule-embeddings
docker run --rm --network krx-rule-mcp_default \
  --entrypoint /usr/local/bin/krx-rule-index \
  --user "$(id -u):$(id -g)" \
  -v "$KRX_RULE_DATA_DIR:/app/data:ro" \
  -v "$KRX_RULE_INDEX_DIR:/app/index" \
  -e OPENAI_API_KEY=local \
  -e KRX_EMBEDDING_BASE_URL=http://krx-rule-embeddings:80/v1 \
  -e KRX_EMBEDDING_MODEL=intfloat/multilingual-e5-small \
  -e KRX_EMBEDDING_DIMENSIONS=384 \
  krx-rule-mcp:local \
  --data-dir /app/data \
  --index-dir /app/index \
  --vector-index /app/index/vectors.krxvec
```

The vector command builds the full corpus by default. For a cheap smoke test, add `--vector-sample-query "상장 심사" --vector-sample-per-query 16`.
The repository-provided vector snapshot is generated with `intfloat/multilingual-e5-small`, 384 dimensions, `query: ` query prefix, and `passage: ` document prefix. If you use another embedding model, set `KRX_TEI_MODEL_ID` for the sidecar when applicable, set the matching `KRX_EMBEDDING_MODEL`, `KRX_EMBEDDING_DIMENSIONS`, `KRX_EMBEDDING_QUERY_PREFIX`, and `KRX_EMBEDDING_DOCUMENT_PREFIX` for both indexing and serving, then rebuild `vectors.krxvec`.

## Images

`deploy/docker/Dockerfile` has one runtime target:

- `server`: distroless Go image containing `krx-rule-mcp`, `krx-rule-index`, and `config/domain-lexicon.yaml`.

The image is non-root and can run with a read-only filesystem. Corpus data is provided by a read-only volume. Search indexes are provided by a separate read-only volume for serving, and by a writable volume only when running `krx-rule-index`.

## Kubernetes

Manifests are in `deploy/kubernetes`.

```bash
kubectl apply -f deploy/kubernetes/
```

The default manifest runs the Go MCP server and TEI sidecar in the same Pod. It expects a PVC named `krx-rule-data` mounted at `/app/data` with a prebuilt corpus and a separate PVC named `krx-rule-index` mounted at `/app/index` with prebuilt BM25/vector snapshots.

Before applying, update:

- image references
- `krx-rule-mcp-secret`
- ingress host and TLS secret
- allowed origins
- PVC/storage strategy for `krx-rule-data` and `krx-rule-index`

## GitHub Actions

This repository's CI should cover Go tests, Docker build, and smoke checks for `krx-rule-index`/server against a sample corpus. Scheduled sync workflows belong in the separate `krx-rule-markdown` repository.
