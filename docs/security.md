# Security

HTTP MCP defaults to authenticated deployment. Set `RULE_MCP_AUTH_MODE=required`
or `--auth-mode required` to enforce the default explicitly. The only other
mode is `disabled`, which must be limited to a trusted private network. There
is no request-by-request optional mode.

Required public-deployment controls:

- Hashed bearer-token registry for `/mcp`.
- Origin allowlist for browser-originated requests.
- Request body and complete serialized response size limits.
- Query-length, request-concurrency, and search/embedding-concurrency limits.
- Embedding and overall request deadlines plus bounded graceful shutdown.
- Per-IP rate limit.
- TLS termination at the ingress or reverse proxy.

Authentication mode does not change the other HTTP controls. In `disabled`
mode the server skips only bearer validation and emits a startup warning.
Unauthenticated `/healthz`, `/readyz`, and `/metrics` remain intended for the
cluster-internal Service. The provided Ingress publishes only `/mcp`.

## Bearer token registry

Required mode loads one immutable registry snapshot at startup:

```yaml
version: 1
tokens:
  - id: chatgpt-production
    sha256: <64-lowercase-hex-sha256-of-token>
    enabled: true
```

Generate a 256-bit token and hash the exact token string:

```bash
TOKEN="$(openssl rand -hex 32)"
TOKEN_SHA256="$(printf '%s' "$TOKEN" | openssl dgst -sha256 -r | awk '{print $1}')"
printf 'Bearer token (shown once): %s\nSHA-256: %s\n' "$TOKEN" "$TOKEN_SHA256"
```

Give `TOKEN` only to the client and put `TOKEN_SHA256` in the registry. The
registry is strict YAML, limited to 1 MiB and 1,024 records. IDs and hashes
must be unique, IDs must match `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`,
`enabled` must be explicit, hashes must be 64 lowercase hexadecimal
characters, and required mode needs at least one enabled record.
The server hashes the presented bearer value and compares it against every
enabled digest using constant-time comparison. It never logs token values,
registry token IDs, stored token hashes, or Authorization headers.

`RULE_MCP_BEARER_TOKEN` and `--token` have been removed. Use
`RULE_MCP_BEARER_TOKEN_FILE` or `--bearer-token-file`; the old environment
variable produces a migration error.

The server does not watch or reload the file. To rotate credentials:

1. Add the new digest as enabled, replace the mounted registry, restart every
   server replica, and wait for readiness.
2. Move clients to the new token.
3. Set the old record to `enabled: false`, replace the registry again, restart
   every replica, and wait for readiness.

During a Kubernetes RollingUpdate, old Pods retain the old startup snapshot.
Revocation is complete only after the rollout finishes. `/readyz`, startup
logs, and internal metrics expose the authentication mode, active-token count,
and whole-file registry digest so operators can compare replicas without
exposing individual credential data. The authentication registry digest is
not part of the RAG `release_generation`.

## HTTP runtime

HTTP MCP is stateless: no SDK session is retained between requests, so replicas
do not require sticky routing. Each POST uses a temporary request-scoped SDK
session. Persistent GET/SSE session streams, replay/resumption, subscriptions
that outlive a request, and server-to-client requests are not supported.

`/readyz` requires a non-empty repository and, when configured, the expected
canonical release generation. Required-vector policy additionally checks full
vector coverage and a deadline-bounded live embedding canary. Internal metrics
include HTTP statuses, tool duration/counts, embedding fallback reasons, vector
coverage, release identity, authentication mode, active-token count, and the
registry digest. Queries, document IDs, and token IDs are not metric labels.

The built-in rate limiter keys on `RemoteAddr`. A high coarse limiter runs
before authentication. The lower MCP quota runs after a valid token in
required mode and applies directly to requests in disabled mode. Rejected
credentials therefore do not consume the authenticated bucket. Enforce
user-facing identity quotas at a trusted proxy because the application does
not trust client-supplied forwarding headers.

Recommended environment variables:

```bash
RULE_MCP_MODE=http
RULE_MCP_AUTH_MODE=required
RULE_MCP_BEARER_TOKEN_FILE=/run/secrets/krx-rule-mcp/bearer-tokens.yaml
RULE_MCP_ALLOWED_ORIGINS=https://chat.openai.com,https://chatgpt.com
RULE_MCP_EXPECTED_RELEASE_GENERATION=<64-character-release-generation>
RULE_MCP_SERVER_IMAGE_DIGEST=sha256:<published-image-digest>
RULE_MCP_TEI_IMAGE_DIGEST=sha256:<tei-runtime-image-digest>
RULE_MCP_MAX_QUERY_RUNES=1000
RULE_MCP_MAX_CONCURRENT_SEARCHES=4
RULE_MCP_MAX_CONCURRENT_REQUESTS=16
RULE_MCP_REQUEST_SIZE_LIMIT=1048576
RULE_MCP_RESPONSE_SIZE_LIMIT=1048576
RULE_MCP_TOOL_OUTPUT_SIZE_LIMIT=524288
RULE_MCP_EMBEDDING_TIMEOUT=5s
RULE_MCP_READINESS_EMBEDDING_TIMEOUT=5s
RULE_MCP_REQUEST_TIMEOUT=40s
RULE_MCP_SHUTDOWN_TIMEOUT=45s
KRX_VECTOR_SEARCH_ENABLED=true
KRX_VECTOR_SEARCH_POLICY=required
KRX_REQUIRE_VECTOR=true
```

For public deployment, keep authentication required, terminate TLS at the
trusted ingress or reverse proxy, and rotate tokens after any suspected
exposure. Do not expose the embeddings sidecar unless it is intentionally
operated as a separate embeddings API.
