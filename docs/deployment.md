# Deployment

## Docker Compose

The root `compose.yaml` is the recommended Docker-only runtime. It starts:

- `krx-rule-mcp`: Go HTTP MCP server.
- `krx-rule-embeddings`: operator-selected, TEI-compatible embeddings sidecar.
- `krx-rule-reranker`: optional `reranker` profile for Korean evidence reranking.

Prepare a corpus with `krx-rule-markdown`, copy it to a host path, and point Compose at that corpus plus a matching index directory.
This repository does not contain a generated corpus, but the source checkout may include a default immutable index generation under root-level `index/`.
Use that bundled generation when it matches your mounted corpus, or generate a separate `KRX_RULE_INDEX_DIR` for your deployment.
The server image does not contain corpus or index files, so both paths are still supplied as volumes.
Do not start the server until `$KRX_RULE_INDEX_DIR/current` selects a generation whose `generation.json` and BM25 artifact match the corpus release; otherwise startup fails while loading the repository.

```bash
cp .env.compose.example .env
vi .env  # set data/index, auth registry directory, and a user-selected TEI image/digest
# KRX_RULE_INDEX_DIR may point to this repository's ./index if it matches KRX_RULE_DATA_DIR.
# Otherwise publish at least a BM25 generation before starting the server.
# See "Manual Index Jobs" below for local and container commands.
docker compose up -d --build
curl http://localhost:8080/healthz
```

`KRX_RULE_DATA_DIR` is mounted read-only at `/app/data`; `KRX_RULE_INDEX_DIR` is mounted read-only at `/app/index`. The server image does not contain corpus or index files, but it does include the default domain lexicon at `/app/config/domain-lexicon.yaml`.
`RULE_MCP_AUTH_CONFIG_DIR` is mounted read-only at `/run/secrets/krx-rule-mcp`
and, in the default required mode, must contain `bearer-tokens.yaml`. Create it
with the version-1 registry format in [security.md](security.md); the actual
bearer values remain with clients and only their SHA-256 digests are mounted.
The host directories must be readable by the non-root container user. For local
smoke tests with temporary directories, run
`chmod -R a+rX "$KRX_RULE_DATA_DIR" "$KRX_RULE_INDEX_DIR" "$RULE_MCP_AUTH_CONFIG_DIR"`
after creating them.

Compose defaults `RULE_MCP_AUTH_MODE` to `required`,
binds the MCP port to `127.0.0.1` unless `RULE_MCP_BIND_ADDRESS` is set, and
gives the server 50 seconds to drain. It does not select a TEI image: the
operator must provide `RULE_MCP_TEI_IMAGE` and its matching
`RULE_MCP_TEI_IMAGE_DIGEST` for the target architecture. TEI must pass
`/health` before the MCP server starts, the selected generation must contain
full vector coverage, and `/readyz` runs a live embedding canary. For production
set `RULE_MCP_IMAGE` to the GHCR manifest digest reference and set
`RULE_MCP_SERVER_IMAGE_DIGEST` to the same digest value. Set
`RULE_MCP_EXPECTED_RELEASE_GENERATION` after calculating it to reject a
mismatched release.

To run without bearer authentication on a trusted private network, set
`RULE_MCP_AUTH_MODE=disabled`. The authentication directory must still exist
for the Compose bind mount, but `bearer-tokens.yaml` may be absent. All other
HTTP controls remain enabled.

HTTP request and complete JSON-RPC response bodies default to 1 MiB (`RULE_MCP_REQUEST_SIZE_LIMIT` and `RULE_MCP_RESPONSE_SIZE_LIMIT`, minimum 1024 bytes). Tool payload shaping has a separate 512 KiB default (`RULE_MCP_TOOL_OUTPUT_SIZE_LIMIT`). The response limit covers the final wire representation after SDK serialization, not only `structuredContent`.

### Optional Korean reranker

The reranker is a separate TEI process because one TEI instance serves one model. Start the profile before enabling required mode in the server:

```bash
docker compose --profile reranker up -d krx-rule-reranker
docker compose up -d krx-rule-mcp
```

Set `KRX_RERANKER_ENABLED=true`, `KRX_RERANKER_POLICY=required`, and `KRX_REQUIRE_RERANKER=true` in `.env`. The maintained experiment pins `dragonkue/bge-reranker-v2-m3-ko` revision `2aca5884ecac490192af9ebd86836d9073d826cd`, F16, candidate K 20, and client batch 4. The service verifies `/info` model identity before serving and `/readyz` sends a live two-passage canary. `RULE_MCP_REQUEST_TIMEOUT` must exceed `RULE_MCP_RERANKER_TIMEOUT`; measured long legal inputs required a 30-minute reranker deadline in required mode.

On the current x86_64 CPU workstation, the constrained TEI process used about 2 GiB when idle. Three short passages took 4.79 seconds; selected real legal queries had a reranker p95 above two minutes. Treat these measurements as hardware-specific. The profile is optional because the protected audit did not meet release-quality gates even though it improved one retrospective evidence case.

## Bearer Token Rotation

The registry is loaded once at process startup. Editing the host file alone
does not change accepted credentials. For Compose, add or disable a registry
record and then run:

```bash
docker compose restart krx-rule-mcp
curl --fail http://localhost:8080/readyz
```

Add a replacement token and restart before moving clients. After every client
uses it, set the old record to `enabled: false` and restart again. Compare the
`auth_registry_generation` and `active_bearer_tokens` fields from `/readyz`
after each restart.

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

Add `--vector --require-full-vector` when checking a generation that must contain a full vector artifact.

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
  -e KRX_EMBEDDING_MODEL_REVISION=614241f622f53c4eeff9890bdc4f31cfecc418b3 \
  -e KRX_EMBEDDING_DIMENSIONS=384 \
  -e KRX_EMBEDDING_INPUT_FORMAT=text-v1 \
  krx-rule-mcp:local \
  --data-dir /app/data \
  --index-dir /app/index \
  --vector

docker run --rm \
  --entrypoint /usr/local/bin/krx-rule-index \
  -v "$KRX_RULE_DATA_DIR:/app/data:ro" \
  -v "$KRX_RULE_INDEX_DIR:/app/index:ro" \
  -e KRX_EMBEDDING_MODEL_REVISION=614241f622f53c4eeff9890bdc4f31cfecc418b3 \
  krx-rule-mcp:local \
  --data-dir /app/data --index-dir /app/index --vector --check --require-full-vector
```

The vector command builds the full corpus by default. For a cheap smoke test, add `--vector-sample-query "상장 심사" --vector-sample-per-query 16`.
`--vector` publishes BM25, vector, metadata, and `generation.json` together below `generations/<id>/`. The checked-in reference artifact uses `intfloat/multilingual-e5-small` revision `614241f622f53c4eeff9890bdc4f31cfecc418b3`, 384 dimensions, `query: ` / `passage: ` prefixes, and `text-v1` document input. Other OpenAI-compatible embedding profiles are valid release candidates when rebuilt with their own explicit settings, complete coverage, matching runtime provenance, and the same Korean-primary quality gate.

## Images

`deploy/docker/Dockerfile` has one runtime target:

- `server`: distroless Go image containing `krx-rule-mcp`, `krx-rule-index`, and `config/domain-lexicon.yaml`.

The image is non-root and can run with a read-only filesystem. Corpus data is provided by a read-only volume. Search indexes are provided by a separate read-only volume for serving, and by a writable volume only when running `krx-rule-index`.

## Kubernetes

Manifests are in `deploy/kubernetes`.

The example manifest runs the Go MCP server and an operator-selected embedding TEI
sidecar in the same Pod. It expects a PVC named `krx-rule-data` mounted at
`/app/data` with a validated schema-v2 corpus and a separate PVC named
`krx-rule-index` mounted at `/app/index` with `current` plus immutable
generation directories.
The Kubernetes ConfigMap and Compose runtime use
`KRX_VECTOR_SEARCH_POLICY=required`, so a missing, malformed, partial, stale,
or incompatible vector release prevents the replica from becoming a BM25-only
surprise. The operator-selected TEI image must support the deployment's target
architecture and sidecar contract. In required mode, runtime embedding errors,
timeouts, count/dimension mismatches, and non-finite vectors return a tool error
instead of BM25 results. Optional mode remains available when running the
binary directly for deployments that explicitly accept BM25 fallback. Re-measure the TEI sidecar resources on the target CPU architecture before rollout. The checked-in Kubernetes example does not add the optional reranker sidecar. A deployment that enables it must add a separately resourced TEI container, set the reranker environment contract and image digest, extend readiness timeouts, and recalculate `release_generation`.
The ConfigMap also uses `RULE_MCP_AUTH_MODE=required`. The Secret key
`bearer-tokens.yaml` is mounted as a file under
`/run/secrets/krx-rule-mcp`; the server rejects a missing or invalid registry
before listening. The Secret volume itself is optional only so an operator can
set authentication mode to `disabled` without creating a dummy Secret.

Before applying, update:

- the immutable server image digest in both the `image:` reference and `RULE_MCP_SERVER_IMAGE_DIGEST`
- the operator-selected immutable TEI image and matching
  `RULE_MCP_TEI_IMAGE_DIGEST`; it must support the selected embedding profile
  shared by TEI, the `KRX_EMBEDDING_*` settings, and the vector build metadata
- the rejected bearer-registry placeholder in `krx-rule-mcp-secret`
- `RULE_MCP_EXPECTED_RELEASE_GENERATION`
- ingress host and TLS secret
- allowed origins
- PVC/storage strategy for `krx-rule-data` and `krx-rule-index`

The checked-in all-zero server/TEI image digests and placeholder bearer registry are intentionally non-deployable. This prevents an example manifest from silently becoming a production deployment. Compose may use a mutable tag for local development; any reproducible or required-vector rollout must pin its TEI image digest and retain the model commit too.

Calculate the release generation with the exact published image, mounted artifacts, vector settings, and server and TEI image digests that the Pod will use. The command does not call the embeddings endpoint; it only verifies that the configured runtime can adopt the loaded vector snapshot.

```bash
IMAGE='ghcr.io/chromato99/krx-rule-mcp@sha256:<published-image-digest>'
IMAGE_DIGEST='sha256:<published-image-digest>'
TEI_IMAGE_DIGEST='sha256:<tei-runtime-image-digest>'

docker run --rm \
  -v "$KRX_RULE_DATA_DIR:/app/data:ro" \
  -v "$KRX_RULE_INDEX_DIR:/app/index:ro" \
  -e RULE_MCP_SERVER_IMAGE_DIGEST="$IMAGE_DIGEST" \
  -e RULE_MCP_TEI_IMAGE_DIGEST="$TEI_IMAGE_DIGEST" \
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

`/readyz` returns 503 when the loaded descriptor differs from the configured generation. In required-vector mode it also performs a bounded canary embedding (five-second default) and validates the returned count, configured dimensions, finite values, and an exact match with the loaded vector index; a TEI outage therefore removes the replica from service and readiness automatically recovers with TEI. A successful response includes `auth_mode`, `auth_registry_generation`, and `active_bearer_tokens`; these values do not include individual token IDs or hashes. The public Ingress routes only `/mcp`; `/healthz`, `/readyz`, and `/metrics` remain available through the cluster-internal Service for probes and monitoring. For strict no-mixed-generation cutovers, deploy a second labeled Service/Deployment and switch the public route only after every new Pod is ready.

After changing `bearer-tokens.yaml`, update the Secret and restart all server
Pods. The process intentionally does not watch mounted Secret updates:

```bash
kubectl apply -f deploy/kubernetes/secret.yaml
kubectl rollout restart deployment/krx-rule-mcp
kubectl rollout status deployment/krx-rule-mcp
```

Wait for rollout completion before considering an old token revoked. During
the default RollingUpdate, an old Pod may continue accepting the previous
startup snapshot. Follow the same add–restart–move clients–disable–restart
sequence used by Compose.

After rollout, query each Pod directly through the Kubernetes API proxy instead of sampling the load-balanced Service. Every line must report the same release and authentication registry generations:

```bash
NAMESPACE=default
for pod in $(kubectl -n "$NAMESPACE" get pods -l app=krx-rule-mcp -o jsonpath='{.items[*].metadata.name}'); do
  printf '%s ' "$pod"
  kubectl get --raw "/api/v1/namespaces/$NAMESPACE/pods/$pod:8080/proxy/readyz"
done
```

This is a post-deploy assertion only; the application does not add peer discovery or a distributed generation store.

### Memory profile behind the manifest

The server container request/limit is `1536Mi`/`2Gi`. Re-measure startup memory whenever the corpus, index format, or embedding dimensions change; the separate TEI sidecar is not included in the server figure.

The full BM25+vector build took 48m36s and peaked at `1,756,900KiB` RSS with local CPU TEI. Run index publication as a separate release job with at least a `2Gi` request and a `3Gi` limit; do not assume the lower server startup figure applies to builds. The local TEI reported no immutable model SHA, so a production rollout must rebuild with a pinned model revision even when the model ID and dimensions match.

This is a corpus-specific baseline, not a permanent sizing guarantee. Re-run startup RSS plus representative concurrent `search_rules`, `get_context`, and long-page calls whenever corpus/chunk count, vector dimensions, index representation, Go version, or concurrency defaults change. Record p50/p95/p99 latency, allocation rate, peak RSS, and OOM/throttle events before lowering the request or limit.

## GitHub Actions

CI covers Go tests, Docker build, and smoke checks for `krx-rule-index`/server against a sample corpus. `.github/workflows/publish-image.yml` publishes a single GHCR manifest for `linux/amd64` and `linux/arm64` on `v*` tags or manual dispatch; record its reported manifest digest in the deployment release manifest and deploy by digest. Scheduled sync workflows belong in the separate `krx-rule-markdown` repository.
