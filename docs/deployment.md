# Deployment

## Docker Compose

The root `compose.yaml` is the recommended Docker-only runtime. It starts:

- `krx-rule-mcp`: Go HTTP MCP server.
- `krx-rule-embeddings`: Hugging Face Text Embeddings Inference sidecar.

Prepare a corpus with `krx-rule-markdown`, copy it to a host path, and point Compose at that path.

```bash
cp .env.compose.example .env
vi .env  # set KRX_RULE_DATA_DIR and KRX_MCP_BEARER_TOKEN
docker compose up -d --build
curl http://localhost:8080/healthz
```

`KRX_RULE_DATA_DIR` is mounted read-only at `/app/data`. The server image does not contain corpus files, but it does include the default domain lexicon at `/app/config/domain-lexicon.yaml`.
The host directory must be readable by the non-root container user. For local smoke tests with a temporary directory, run `chmod -R a+rX "$KRX_RULE_DATA_DIR"` after creating the corpus.

## Manual Index Jobs

Index generation is explicit and can run locally or inside the server image.

Local:

```bash
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index "$KRX_RULE_DATA_DIR/index/bm25.krxidx"
```

Container:

```bash
docker build -f deploy/docker/Dockerfile --target server -t krx-rule-mcp:local .
docker run --rm \
  --entrypoint /usr/local/bin/krx-rule-index \
  --user "$(id -u):$(id -g)" \
  -v "$KRX_RULE_DATA_DIR:/app/data" \
  krx-rule-mcp:local \
  --data-dir /app/data --index /app/data/index/bm25.krxidx
```

To build vectors with the compose TEI sidecar:

```bash
docker compose up -d krx-rule-embeddings
docker run --rm --network krx-rule-mcp_default \
  --entrypoint /usr/local/bin/krx-rule-index \
  --user "$(id -u):$(id -g)" \
  -v "$KRX_RULE_DATA_DIR:/app/data" \
  -e OPENAI_API_KEY=local \
  -e KRX_EMBEDDING_BASE_URL=http://krx-rule-embeddings:80/v1 \
  -e KRX_EMBEDDING_MODEL=intfloat/multilingual-e5-small \
  -e KRX_EMBEDDING_DIMENSIONS=384 \
  krx-rule-mcp:local \
  --data-dir /app/data \
  --index /app/data/index/bm25.krxidx \
  --vector-index /app/data/index/vectors.krxvec
```

The vector command builds the full corpus by default. For a cheap smoke test, add `--vector-sample-query "상장 심사" --vector-sample-per-query 16`.

## Images

`deploy/docker/Dockerfile` has one runtime target:

- `server`: distroless Go image containing `krx-rule-mcp`, `krx-rule-index`, and `config/domain-lexicon.yaml`.

The image is non-root and can run with a read-only filesystem. Corpus data is provided by a read-only volume for serving, and by a writable volume only when running `krx-rule-index`.

## Kubernetes

Manifests are in `deploy/kubernetes`.

```bash
kubectl apply -f deploy/kubernetes/
```

The default manifest runs the Go MCP server and TEI sidecar in the same Pod. It expects a PVC named `krx-rule-data` mounted at `/app/data` with a prebuilt corpus and indexes.

Before applying, update:

- image references
- `krx-rule-mcp-secret`
- ingress host and TLS secret
- allowed origins
- PVC/storage strategy for `krx-rule-data`

## GitHub Actions

This repository's CI should cover Go tests, Docker build, and smoke checks for `krx-rule-index`/server against a sample corpus. Scheduled sync workflows belong in the separate `krx-rule-markdown` repository.
