# Deployment

## Docker Compose

The root `compose.yaml` is the recommended Docker-only runtime. It starts:

- `krx-rule-mcp`: Go HTTP MCP server.
- `krx-rule-embeddings`: Hugging Face Text Embeddings Inference sidecar.

Prepare a corpus with `krx-rule-markdown`, copy it to a host path, and point Compose at that corpus plus a matching index directory.
This repository does not contain a generated corpus, but the source checkout may include a default immutable index generation under root-level `index/`.
Use that bundled generation when it matches your mounted corpus, or generate a separate `KRX_RULE_INDEX_DIR` for your deployment.
The server image does not contain corpus or index files, so both paths are still supplied as volumes.
Do not start the server until `$KRX_RULE_INDEX_DIR/current` selects a generation whose `generation.json` and BM25 artifact match the corpus release; otherwise startup fails while loading the repository.

```bash
cp .env.compose.example .env
vi .env  # set KRX_RULE_DATA_DIR, KRX_RULE_INDEX_DIR, and a strong random KRX_MCP_BEARER_TOKEN
# KRX_RULE_INDEX_DIR may point to this repository's ./index if it matches KRX_RULE_DATA_DIR.
# Otherwise publish at least a BM25 generation before starting the server.
# See "Manual Index Jobs" below for local and container commands.
docker compose up -d --build
curl http://localhost:8080/healthz
```

`KRX_RULE_DATA_DIR` is mounted read-only at `/app/data`; `KRX_RULE_INDEX_DIR` is mounted read-only at `/app/index`. The server image does not contain corpus or index files, but it does include the default domain lexicon at `/app/config/domain-lexicon.yaml`.
The host directories must be readable by the non-root container user. For local smoke tests with temporary directories, run `chmod -R a+rX "$KRX_RULE_DATA_DIR" "$KRX_RULE_INDEX_DIR"` after creating the corpus and indexes.
Compose requires the bearer token instead of falling back to a known default, binds the MCP port to `127.0.0.1` unless `KRX_MCP_BIND_ADDRESS` is set, and gives the server 20 seconds to drain. Set `KRX_EXPECTED_RELEASE_GENERATION` after calculating it if you want the same readiness gate locally.

HTTP request and complete JSON-RPC response bodies default to 1 MiB (`KRX_MCP_REQUEST_SIZE_LIMIT` and `KRX_MCP_RESPONSE_SIZE_LIMIT`, minimum 1024 bytes). Tool payload shaping has a separate 512 KiB default (`KRX_MCP_TOOL_OUTPUT_SIZE_LIMIT`). The response limit covers the final wire representation after SDK serialization, not only `structuredContent`.

## Manual Index Jobs

Index generation is explicit and can run locally or inside the server image.
Run it whenever the corpus changes, the indexer changes, or the repository-provided `index/` no longer matches your corpus.

To verify the repository-provided generation and every declared artifact digest:

```bash
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir ./index \
  --check
```

Add `--require-full-vector` only when the selected generation was published with a full vector artifact. Do not combine a current BM25-only generation with an unrelated legacy flat vector file.

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
`--vector-index` is retained as the vector-inclusion selector; BM25, vector, metadata, and `generation.json` are published together below `generations/<id>/`. The default vector settings are `intfloat/multilingual-e5-small`, 384 dimensions, `query: ` query prefix, and `passage: ` document prefix. If you use another embedding model, set the matching indexing/serving variables and publish a new generation.

## Images

`deploy/docker/Dockerfile` has one runtime target:

- `server`: distroless Go image containing `krx-rule-mcp`, `krx-rule-index`, and `config/domain-lexicon.yaml`.

The image is non-root and can run with a read-only filesystem. Corpus data is provided by a read-only volume. Search indexes are provided by a separate read-only volume for serving, and by a writable volume only when running `krx-rule-index`.

## Kubernetes

Manifests are in `deploy/kubernetes`.

The default manifest runs the Go MCP server and TEI sidecar in the same Pod. It expects a PVC named `krx-rule-data` mounted at `/app/data` with a validated schema-v2 corpus and a separate PVC named `krx-rule-index` mounted at `/app/index` with `current` plus immutable generation directories.
The Kubernetes ConfigMap uses `KRX_VECTOR_SEARCH_POLICY=required`, so a missing, malformed, partial, stale, or incompatible vector release prevents the Pod from becoming a BM25-only surprise. Compose defaults to `optional`; set vector search off to skip vector files entirely.

Before applying, update:

- the immutable server image digest in both the `image:` reference and `KRX_SERVER_IMAGE_DIGEST`
- the immutable TEI image digest and the same exact model commit in TEI `--revision`, `KRX_EMBEDDING_MODEL_REVISION`, and vector build settings
- the rejected placeholder in `krx-rule-mcp-secret`
- `KRX_EXPECTED_RELEASE_GENERATION`
- ingress host and TLS secret
- allowed origins
- PVC/storage strategy for `krx-rule-data` and `krx-rule-index`

The checked-in all-zero server/TEI image digests are intentionally non-deployable, and the placeholder bearer token/model revision must be replaced. This prevents an example manifest from silently becoming a production deployment. Compose may use a mutable tag for local development; any reproducible or required-vector rollout must pin its TEI image digest and model commit too.

Calculate the release generation with the exact published image, mounted artifacts, vector settings, and server image digest that the Pod will use. The command does not call the embeddings endpoint; it only verifies that the configured runtime can adopt the loaded vector snapshot.

```bash
IMAGE='ghcr.io/chromato99/krx-rule-mcp@sha256:<published-image-digest>'
IMAGE_DIGEST='sha256:<published-image-digest>'

docker run --rm \
  -v "$KRX_RULE_DATA_DIR:/app/data:ro" \
  -v "$KRX_RULE_INDEX_DIR:/app/index:ro" \
  -e KRX_SERVER_IMAGE_DIGEST="$IMAGE_DIGEST" \
  -e KRX_VECTOR_SEARCH_ENABLED=true \
  -e KRX_VECTOR_SEARCH_POLICY=required \
  -e OPENAI_API_KEY=local \
  -e KRX_EMBEDDING_BASE_URL=http://127.0.0.1:80/v1 \
  -e KRX_EMBEDDING_MODEL=intfloat/multilingual-e5-small \
  -e KRX_EMBEDDING_DIMENSIONS=384 \
  "$IMAGE" --print-release-generation
```

Copy the printed `release_generation` into the ConfigMap, then apply:

```bash
kubectl apply -f deploy/kubernetes/
kubectl rollout status deployment/krx-rule-mcp
```

`/readyz` returns 503 when the loaded descriptor differs from the configured generation. The public Ingress routes only `/mcp`; `/healthz`, `/readyz`, and `/metrics` remain available through the cluster-internal Service for probes and monitoring. For strict no-mixed-generation cutovers, deploy a second labeled Service/Deployment and switch the public route only after every new Pod is ready.

After rollout, query each Pod directly through the Kubernetes API proxy instead of sampling the load-balanced Service. Every line must report the same configured generation:

```bash
NAMESPACE=default
for pod in $(kubectl -n "$NAMESPACE" get pods -l app=krx-rule-mcp -o jsonpath='{.items[*].metadata.name}'); do
  printf '%s ' "$pod"
  kubectl get --raw "/api/v1/namespaces/$NAMESPACE/pods/$pod:8080/proxy/readyz"
done
```

This is a post-deploy assertion only; the application does not add peer discovery or a distributed generation store.

### Memory profile behind the manifest

The server container request/limit is `1536Mi`/`2Gi`. With the schema-v2 corpus release containing 123 documents and 597 attachments, the structured generation contains 45,686 chunks and full 384-dimension vectors. Required-vector `--print-release-generation` startup took 5.39s and peaked at `923,280KiB`. Earlier BM25-only startup initially peaked at `1,509,264KiB`; removing a redundant validation-time rebuild reduced that run to `867,052KiB` without changing chunks or search scores. The request is above the measured startup peaks, while the limit leaves headroom for requests, Go GC pacing, and release growth. The separate TEI sidecar is not included in this number.

The full BM25+vector build took 48m36s and peaked at `1,756,900KiB` RSS with local CPU TEI. Run index publication as a separate release job with at least a `2Gi` request and a `3Gi` limit; do not assume the lower server startup figure applies to builds. The local TEI reported no immutable model SHA, so a production rollout must rebuild with a pinned model revision even when the model ID and dimensions match.

This is a corpus-specific baseline, not a permanent sizing guarantee. Re-run startup RSS plus representative concurrent `search_rules`, `get_context`, and long-page calls whenever corpus/chunk count, vector dimensions, index representation, Go version, or concurrency defaults change. Record p50/p95/p99 latency, allocation rate, peak RSS, and OOM/throttle events before lowering the request or limit.

## GitHub Actions

This repository's CI should cover Go tests, Docker build, and smoke checks for `krx-rule-index`/server against a sample corpus. Scheduled sync workflows belong in the separate `krx-rule-markdown` repository.
