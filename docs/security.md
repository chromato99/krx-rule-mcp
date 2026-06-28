# Security

HTTP MCP is designed for authenticated deployment.

Required controls:

- Bearer token for `/mcp`.
- Origin allowlist for browser-originated requests.
- Request body size limit.
- Per-IP rate limit.
- TLS termination at the ingress or reverse proxy.

The implementation exposes unauthenticated `/healthz`, `/readyz`, and `/metrics` for platform checks. Do not expose `/metrics` publicly unless your platform already protects it.

Recommended environment variables:

```bash
KRX_MCP_MODE=http
KRX_MCP_BEARER_TOKEN=...
KRX_MCP_ALLOWED_ORIGINS=https://chat.openai.com,https://chatgpt.com
```

For public deployment, run behind TLS and rotate the bearer token as a secret. Avoid logging Authorization headers.

The embeddings sidecar is an internal helper for vector search. Do not expose it publicly unless you intentionally operate it as an embeddings API. In Docker Compose it binds to `127.0.0.1` on the host and is reached by the MCP server over the compose network.
