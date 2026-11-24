# HTTP security

For a public deployment, expose `/mcp` through a TLS ingress or reverse proxy, keep bearer authentication enabled, and configure `RULE_MCP_ALLOWED_ORIGINS` for browser clients. Keep `/healthz`, `/readyz`, `/metrics` and the model services on the internal network.

## Bearer token registry

HTTP defaults to `RULE_MCP_AUTH_MODE=required`. Generate a token, give it privately to the client for its `Authorization: Bearer` header, and store only the digest on the server. These commands keep the token in a shell variable and display its digest.

```bash
TOKEN="$(openssl rand -hex 32)"
printf '%s' "$TOKEN" | openssl dgst -sha256 -r
```

Copy the 64-character lowercase digest into `bearer-tokens.yaml`. The server reads the path in `RULE_MCP_BEARER_TOKEN_FILE` (or `--bearer-token-file`). Compose mounts the auth directory at `/run/secrets/krx-rule-mcp` read-only.

```yaml
version: 1
tokens:
  - id: first-client
    sha256: "<64 lowercase hex characters>"
    enabled: true
```

The file accepts up to 1,024 records and 1 MiB. Use unique IDs and digests, explicit `enabled` values and at least one active token. IDs must match `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`. Unknown fields or malformed entries fail startup. The server hashes each presented token and compares it with all active digests in constant time.

## Token rotation

1. Add the new digest as enabled and restart every replica. Check that each `/readyz` reports the same `auth_registry_generation` and expected `active_bearer_tokens`.
2. Switch clients to the new token. Set the old record to `enabled: false` and restart all replicas again.
3. Wait for rollout completion and check each replica. A process accepts its startup registry until it exits. The registry digest is tracked separately from `release_generation`.

For Compose use `docker compose restart krx-rule-mcp`. For Kubernetes apply the updated Secret, then run `kubectl rollout restart deployment/krx-rule-mcp` and `kubectl rollout status deployment/krx-rule-mcp`. [Per-Pod checks](deployment.md#kubernetes)

## HTTP runtime

Each POST has a request-scoped MCP session. Replicas can serve requests without sticky routing. Persistent GET/SSE streams, replay and subscriptions across requests are unsupported. HTML browser navigation to `/mcp` redirects to the project on GitHub after authentication and Origin checks. MCP JSON/SSE requests stay on the protocol handler.

| Control | Behavior |
| --- | --- |
| Authentication and Origin | On a trusted private network, `RULE_MCP_AUTH_MODE=disabled` skips bearer validation. Origin checks and all resource limits still apply. |
| Size | Request and complete serialized response default to 1 MiB each. Structured tool output defaults to 512 KiB. Both copies of the result and the JSON-RPC envelope count toward the wire limit. Reduce page size if either limit is exceeded. |
| Concurrency and deadlines | Query length, concurrent HTTP requests and searches have separate limits. Embedding, reranking, overall request and graceful shutdown have separate deadlines. The template values depend on the deployment. [Compose settings](../compose.yaml) · [Kubernetes settings](../deploy/kubernetes/configmap.yaml) |
| Rate limit | A coarse IP limit precedes authentication. The MCP quota applies after successful authentication or directly in disabled mode. Limits use the connection's `RemoteAddr`. Apply per-user quotas at the trusted proxy. |
| Monitoring | Internal metrics expose HTTP status, tool timings, fallback reasons, coverage and generation IDs. Queries, document IDs and individual credentials are excluded from metric labels. |

To check a remote endpoint, send an MCP initialization POST. An authenticated deployment also requires the client's bearer header. The example public Ingress routes only `/mcp`.

```bash
curl --fail --silent --show-error https://krx-rule-mcp.chromato99.com/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"krx-smoke","version":"1"}}}'
```
