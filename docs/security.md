# Security

HTTP MCP is designed for authenticated deployment.

Required controls:

- Bearer token for `/mcp`.
- Origin allowlist for browser-originated requests.
- Request body size limit.
- Per-IP rate limit.
- TLS termination at the ingress or reverse proxy.

The implementation exposes unauthenticated `/healthz`, `/readyz`, and `/metrics` for platform checks. `/readyz` returns ready only after a repository with at least one document is loaded. Do not expose `/metrics` publicly unless your platform already protects it.

The built-in rate limiter keys on `RemoteAddr`. Behind an ingress or reverse proxy, that may be the proxy IP rather than the original client IP, so enforce user-facing rate limits at the proxy layer or route trusted `X-Forwarded-For` handling before traffic reaches this server.

Recommended environment variables:

```bash
KRX_MCP_MODE=http
KRX_MCP_BEARER_TOKEN=...
KRX_MCP_ALLOWED_ORIGINS=https://chat.openai.com,https://chatgpt.com
```

For public deployment, run behind TLS and rotate the bearer token as a secret. Avoid logging Authorization headers.

The embeddings sidecar is an internal helper for vector search. Do not expose it publicly unless you intentionally operate it as an embeddings API. In Docker Compose it binds to `127.0.0.1` on the host and is reached by the MCP server over the compose network.
