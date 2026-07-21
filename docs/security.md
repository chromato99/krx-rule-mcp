# Security

HTTP MCP is designed for authenticated deployment.

Required controls:

- Bearer token for `/mcp`.
- Origin allowlist for browser-originated requests.
- Request body and complete serialized response size limits.
- Query-length, request-concurrency, and search/embedding-concurrency limits.
- Embedding and overall request deadlines plus bounded graceful shutdown.
- Per-IP rate limit.
- TLS termination at the ingress or reverse proxy.

HTTP MCP is stateless: no SDK session is retained between requests, so replicas do not require sticky routing. The implementation exposes unauthenticated `/healthz`, `/readyz`, and `/metrics` for platform checks on the cluster-internal Service. The provided Ingress routes only `/mcp`; it does not publish probes or metrics. If you use another proxy, keep `/metrics` private or add separate authentication.

Each POST uses a temporary request-scoped SDK session. Persistent GET/SSE session streams, replay/resumption, subscriptions that outlive a request, and server-to-client requests are not supported. Request-scoped notifications may still be delivered in the POST response. The current KRX tools/resources do not require the unsupported bidirectional features.

`/readyz` returns ready only after a non-empty repository is loaded and, when configured, the actual canonical release generation equals `KRX_EXPECTED_RELEASE_GENERATION`. The generation descriptor binds corpus/index source and build hashes, artifact digests, optional vector metadata, domain lexicon, runtime vector mode, and `KRX_SERVER_IMAGE_DIGEST`. This catches partial or mixed artifact mounts at readiness time; use a blue/green Service switch when a rollout must guarantee that old and new generations are never simultaneously routed.

Internal metrics include HTTP status counts, per-tool call count and duration sum, embedding fallback count by bounded reason, vector coverage, and release identity/digests. Labels are fixed server-controlled values; queries and document ids are never used as metric labels.

The built-in rate limiter keys on `RemoteAddr`. A high coarse limiter runs before authentication to absorb obvious abuse, while the lower MCP quota runs only after a valid bearer token, so rejected credentials do not consume the authenticated bucket. Behind an ingress or reverse proxy, `RemoteAddr` may still be the proxy IP rather than the original user, so enforce user-facing identity quotas at the trusted proxy; the application does not trust client-supplied forwarding headers by itself.

Recommended environment variables:

```bash
KRX_MCP_MODE=http
KRX_MCP_BEARER_TOKEN=<strong-random-secret>
KRX_MCP_ALLOWED_ORIGINS=https://chat.openai.com,https://chatgpt.com
KRX_EXPECTED_RELEASE_GENERATION=<64-character-release-generation>
KRX_SERVER_IMAGE_DIGEST=sha256:<published-image-digest>
KRX_MCP_MAX_QUERY_RUNES=1000
KRX_MCP_MAX_CONCURRENT_SEARCHES=16
KRX_MCP_MAX_CONCURRENT_REQUESTS=64
KRX_MCP_REQUEST_SIZE_LIMIT=1048576
KRX_MCP_RESPONSE_SIZE_LIMIT=1048576
KRX_MCP_TOOL_OUTPUT_SIZE_LIMIT=524288
KRX_MCP_EMBEDDING_TIMEOUT=3s
KRX_MCP_REQUEST_TIMEOUT=30s
KRX_MCP_SHUTDOWN_TIMEOUT=15s
```

The server rejects known placeholder secrets such as `change-me` and `REPLACE_WITH_STRONG_RANDOM_TOKEN`. For public deployment, run behind TLS, generate a random bearer token, and rotate it as a secret. Avoid logging Authorization headers.

The embeddings sidecar is an internal helper for vector search. Do not expose it publicly unless you intentionally operate it as an embeddings API. In Docker Compose it binds to `127.0.0.1` on the host and is reached by the MCP server over the compose network.
