# Deployment

Use Docker Compose to serve the bundled rules over HTTP, or run the Go binary against a matching corpus and index. Starting with v2.1.0, the server image includes the corpus and index. The checked-in snapshot contains 121 documents from corpus commit `4f3a111` and 48,139 vectors from generation `7d9c7144…`. The data stays fixed for the life of the image. TEI loads the embedding model weights separately.

## Docker Compose

1. Copy `.env.compose.example` to `.env`. Pin `RULE_MCP_TEI_IMAGE` to a compatible image for the target architecture (`registry/repository@sha256:<digest>`) and set `RULE_MCP_TEI_IMAGE_DIGEST` to that same `sha256:<digest>`. The bundled index uses the default E5 profile. [Environment template](../.env.compose.example)
2. Set `RULE_MCP_AUTH_CONFIG_DIR` to an existing directory. In the default `required` mode, place `bearer-tokens.yaml` there and give the container user (UID 65532) directory traversal and file read access. [Create the bearer registry](security.md#bearer-token-registry)
3. From the repository root, start the services and check readiness.

```bash
docker compose config --quiet
docker compose up -d --build
curl --fail --retry 30 --retry-connrefused --retry-delay 2 http://127.0.0.1:8080/readyz
```

The client endpoint is `http://127.0.0.1:8080/mcp`. Compose waits for the embeddings service's `/health` check and requires a full-vector index. The MCP port binds to loopback by default. For a published build, set `RULE_MCP_IMAGE=ghcr.io/chromato99/krx-rule-mcp@sha256:<manifest-digest>`, set `RULE_MCP_SERVER_IMAGE_DIGEST` to the same `sha256:…` value, and start with `docker compose up -d --no-build`.

### Use separately generated data

Build a matching corpus and full-vector index first. Set their existing host paths in `.env`, for example `KRX_RULE_DATA_DIR=/opt/krx-rule-data` and `KRX_RULE_INDEX_DIR=/opt/krx-rule-index`. Enable both read-only mounts with the override.

```bash
docker compose -f compose.yaml -f compose.external-data.yaml up -d
```

Use both `-f` options for subsequent Compose commands. The base file uses the bundled data. The override mounts the pair at `/app/data` and `/app/index`, both readable by UID 65532. When replacing the pair, recalculate any configured `RULE_MCP_EXPECTED_RELEASE_GENERATION` with those same mounts and recreate the server to load the new data and settings. [External-data override](../compose.external-data.yaml)

### Optional Korean reranker

To rerank Korean passages, start the separate TEI process, set `KRX_RERANKER_ENABLED=true`, `KRX_RERANKER_POLICY=required` and `KRX_REQUIRE_RERANKER=true` in `.env`, then recreate the server.

```bash
docker compose --profile reranker up -d --wait krx-rule-reranker
docker compose up -d krx-rule-mcp
```

The example uses `dragonkue/bge-reranker-v2-m3-ko` with a pinned revision, F16, 20 candidates and batches of four. Pin its runtime image and set `RULE_MCP_RERANKER_IMAGE_DIGEST` for reproducible deployments. Required mode verifies model identity at startup and runs a two-passage readiness check. Keep the HTTP deadline longer than the reranker deadline. This profile is off by default. In the recorded CPU trial, real queries took over two minutes at p95 and the retrieval evaluation missed its release targets.

## Manual index jobs

Run the following from `krx-rule-mcp/`. For the checked-in index, use the producer's [pinned corpus](https://github.com/chromato99/krx-rule-markdown/tree/4f3a111f7184737f6e959b50af62f9cc87db412f). To collect newer rules, follow the producer's commands for sync, conversion, cleanup and release validation. [Corpus generation](https://github.com/chromato99/krx-rule-markdown#corpus-생성)

```bash
export KRX_RULE_DATA_DIR=../krx-rule-markdown/data
go run ./cmd/krx-rule-index --index-dir ./index --vector --check --require-full-vector
```

`--check` is read-only and exits nonzero for a missing, stale or invalid generation. To **build** a generation, choose a writable index directory. For BM25 alone, run `go run ./cmd/krx-rule-index` with both directory variables set. For full-vector serving, use a healthy embeddings API and the same profile at build and query time.

```bash
export KRX_RULE_INDEX_DIR=/opt/krx-rule-index
mkdir -p "$KRX_RULE_INDEX_DIR"
docker compose up -d --wait krx-rule-embeddings
KRX_EMBEDDING_BASE_URL=http://127.0.0.1:18081/v1 go run ./cmd/krx-rule-index --vector
go run ./cmd/krx-rule-index --vector --check --require-full-vector
```

The example uses the Compose service configured above and its default host port. The container reaches TEI at `http://krx-rule-embeddings:80/v1`. For a different model, set its profile in both the indexer's shell and the server environment. `text-v1` can reuse exact chunk text from a verified previous full generation with the same pinned profile. `--force` recomputes vectors. For a sample, use a separate index directory and add `--vector-sample-query "상장 심사" --vector-sample-per-query 16` to the `--vector` build. [Embedding profile](search.md#embedding-profile)

The server image also contains the indexer. For a containerized build, select `--entrypoint /usr/local/bin/krx-rule-index`, mount the corpus read-only and the target index writable, and run as a user that can write to that target. Published generation directories use mode 0755 and artifacts use 0644.

## Serve from source

Set `KRX_RULE_DATA_DIR` and `KRX_RULE_INDEX_DIR` to the validated pair. The binary starts in stdio mode by default. For local HTTP with bearer authentication, set the registry path and listen address.

```bash
export RULE_MCP_BEARER_TOKEN_FILE=/opt/krx-rule-mcp-auth/bearer-tokens.yaml
go run ./cmd/krx-rule-mcp --mode http --addr 127.0.0.1:8080
```

To require hybrid search, set `KRX_VECTOR_SEARCH_ENABLED=true`, configure the matching embedding endpoint and pass `--require-vector` to the server. For a trusted private network, `RULE_MCP_AUTH_MODE=disabled` explicitly disables bearer authentication. Compose still requires its auth directory mount in this mode. [HTTP controls](security.md#http-runtime)

## Release verification

For a local build named `krx-rule-mcp:local`, verify the embedded artifacts offline. Then calculate `release_generation` with the exact image, mounts, image digests, lexicon and search configuration you will deploy. For a Compose deployment using bundled data, run the second command with the deployment's `.env`.

```bash
docker run --rm --read-only --network none \
  --entrypoint /usr/local/bin/krx-rule-index krx-rule-mcp:local \
  --vector --check --require-full-vector
docker compose run --rm --no-deps krx-rule-mcp --print-release-generation
```

Set `RULE_MCP_EXPECTED_RELEASE_GENERATION` to the printed `release_generation` and recreate the service. `/healthz` checks liveness. `/readyz` returns 503 for a generation mismatch or failed required-backend canary. Find release identity and backend metrics on the internal `/metrics` endpoint.

When updating bundled data, commit `index/current` and its generation together. In the Dockerfile, update `CORPUS_REVISION` and `CORPUS_ARCHIVE_SHA256` with the matching producer commit and its archive checksum. The build verifies the archive and the complete index before packaging the selected generation. CI tests the bundled image, and the publish workflow builds amd64/arm64 images for `v*` tags or manual dispatch. [Dockerfile](../deploy/docker/Dockerfile) · [CI](../.github/workflows/ci.yml) · [Image publication](../.github/workflows/publish-image.yml)

## Kubernetes

The example runs the MCP server and embeddings sidecar in each Pod. Before applying the [manifests](../deploy/kubernetes), configure the following.

| Setting | What to supply |
| --- | --- |
| Images and identity | Replace placeholder server and TEI image references and matching digest variables. Match the sidecar model and revision to the vector index. Calculate the expected release generation using this configuration. |
| Corpus and index | Populate the `krx-rule-data` and `krx-rule-index` PVCs. To use the image snapshot, remove both PVC mounts and their volumes from the Deployment. |
| Network and credentials | Replace the placeholder bearer registry, set the allowed origins, ingress host and TLS secret. Expose `/mcp` publicly and keep probes and metrics cluster-internal. |
| Resources | The server requests 1536 MiB and has a 2 GiB limit, separate from TEI. Size index jobs separately (historical CPU full build peaked at about 1.7 GiB RSS). Measure on the target hardware. A reranker needs its own container, resources and probe deadlines. |

```bash
NAMESPACE=default
kubectl -n "$NAMESPACE" apply -f deploy/kubernetes/
kubectl -n "$NAMESPACE" rollout status deployment/krx-rule-mcp
for pod in $(kubectl -n "$NAMESPACE" get pods -l app=krx-rule-mcp -o jsonpath='{.items[*].metadata.name}'); do
  printf '%s ' "$pod"
  kubectl get --raw "/api/v1/namespaces/$NAMESPACE/pods/$pod:8080/proxy/readyz"
done
```

Verify that **every Pod** reports the expected release and authentication registry generations. If every public request must see the same release during cutover, bring up a second Deployment and Service and switch the route after all new Pods are ready. [Token rotation](security.md#token-rotation)
