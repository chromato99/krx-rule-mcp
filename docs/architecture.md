# Architecture

## Project Boundary

`krx-rule-mcp` is the serving and indexing project.

1. Reads a prepared Markdown corpus from `KRX_RULE_DATA_DIR`.
2. Uses repository-provided BM25/vector snapshots from `index/` when they match the corpus, or builds fresh snapshots through `cmd/krx-rule-index` into `KRX_RULE_INDEX_DIR`.
3. Loads matching snapshots into a Go search engine.
4. Exposes MCP tools/resources over stdio or Streamable HTTP.

`krx-rule-markdown` is a separate project responsible for KRX legal portal sync, attachment conversion, quality reports, and corpus validation. The handoff between projects is the generated `data/` directory. Search snapshots are MCP-serving artifacts and should live outside that corpus directory. This repository keeps a default copy of those serving artifacts in root-level `index/` for the currently maintained corpus.

## Runtime Flow

1. `corpus.LoadDocuments` reads document bundle entrypoints such as `ko/rules/<title>/index.md`, `ko/notices/<title>/index.md`, and `en/rules/<title>/index.md`. Converted attachments are loaded from each document's `attachments` metadata and are indexed as parent-document chunks. Legacy flat `rules/*.md` and `notices/*.md` are still read as Korean corpus.
2. `krx-rule-index` computes a corpus hash from document `content_hash`, language/source metadata, source file metadata, plus attachment `id/status/text_path/content_hash`.
3. The BM25 snapshot is loaded from `KRX_RULE_INDEX_DIR/bm25.krxidx`, which may be the repository-provided `index/bm25.krxidx`. It is regenerated only when missing, stale, or forced.
4. If `--vector-index` is provided, document chunks are embedded through an OpenAI-compatible API and written as `KRXVEC2`.
5. Vector metadata sidecar records corpus hash, model, dimensions, and E5 query/document prefixes.
6. `krx-rule-mcp` refuses to start without a current BM25 snapshot.
7. Missing or stale vector snapshots are ignored and the server stays in BM25 mode.

## Packages

- `cmd/krx-rule-mcp`: stdio/HTTP MCP runtime.
- `cmd/krx-rule-index`: explicit BM25/vector snapshot generator.
- `internal/corpus`: Markdown/frontmatter loading.
- `internal/index`: Korean tokenization, BM25/vector scoring, snapshot IO, RRF merge.
- `internal/mcp`: MCP tool/resource registration.
- `internal/security`: bearer auth, Origin allowlist, request size, rate limit, basic metrics.

## Embeddings

The default local deployment uses Hugging Face Text Embeddings Inference as an OpenAI-compatible sidecar. The repository-provided vector snapshot uses the same settings.

Default settings:

- model: `intfloat/multilingual-e5-small`
- dimensions: `384`
- document prefix: `passage: `
- query prefix: `query: `

Other OpenAI-compatible embedding models can be used, but vector indexing and serving must use identical model, dimension, and prefix settings. Prefix-free models should set both prefix environment variables to empty strings before rebuilding the vector snapshot.

Indexing failures are strict: if vector indexing is explicitly requested and the embeddings API fails, `krx-rule-index` exits non-zero. Runtime query embedding failures are non-fatal and search falls back to BM25 results.

## Language-Aware RAG

Documents carry `language: "ko"` or `language: "en"`. English full-text documents generated from downloadable rule files use `{source_id}-en` as their id and keep the Korean rule id in `source_id`.

MCP search/list tools accept a `language` filter. Leave it blank for bilingual recall, set `ko` for authoritative Korean text, or set `en` for English-answer workflows.
