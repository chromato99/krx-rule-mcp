# Architecture

## Project Boundary

`krx-rule-mcp` is the serving and indexing project.

1. Reads a prepared Markdown corpus from `KRX_RULE_DATA_DIR`.
2. Verifies the producer's schema-v2 manifest, strict release profile, document parity, and canonical hashes.
3. Uses the immutable BM25/vector generation selected by `KRX_RULE_INDEX_DIR/current`, or publishes a fresh generation through `cmd/krx-rule-index`.
4. Exposes MCP tools/resources over stdio or stateless Streamable HTTP.

`krx-rule-markdown` is a separate project responsible for KRX legal portal sync, attachment conversion, quality reports, and corpus validation. The handoff between projects is the generated `data/` directory. Search snapshots are MCP-serving artifacts and should live outside that corpus directory. This repository keeps a default copy of those serving artifacts in root-level `index/` for the currently maintained corpus.

## Runtime Flow

1. `corpus.LoadWithOptions` reads schema-v2 bundle entrypoints and converted attachments, rejects invalid status/hash/path/symlink/global-ID combinations, and verifies `manifest.json` frontmatter parity plus `index_source_hash`/`release_hash`.
2. `krx-rule-index` derives `index_source_hash` from the shared producer/consumer canonical projection and derives a separate `index_build_hash` from the tokenizer/chunker/indexer version.
3. A filesystem lock admits one publisher. BM25 and optional vector/metadata are written and validated in `generations/.staging-*`; an interrupted build cannot alter `current`.
4. A completed content-addressed `generations/<id>/generation.json` records fixed artifact names, byte sizes and SHA-256 digests. Publication atomically replaces only the `current` pointer.
5. `krx-rule-mcp` resolves `current` once, reads only that immutable directory, and compares the digest of the exact decoded bytes with the descriptor. It refuses to start without a valid matching BM25 artifact.
6. When vector search is disabled, vector files are not opened. Optional mode falls back to BM25 with a bounded reason; required mode rejects missing, sample, partial, stale, malformed, or incompatible vector data.
7. Legal chunking records owning Korean and English `article_id` plus `heading_path`, keeps citations distinct, and treats formula pairs and table rows as atomic semantic units. BM25/vector retrieve bounded chunk candidates, fuse by chunk ID, select diverse evidence, and group documents only afterward.
8. `search_rules` applies a versioned answerability gate to original-query coverage, channel agreement, structural anchors, filter constraints, and claim-specific evidence checks. `insufficient` and incompatible `unknown` outcomes fail closed; scores remain ranking signals.
9. At runtime, a canonical release descriptor binds corpus release, index source/build hashes, fixed artifact digests, optional vector metadata and input format, domain lexicon, active vector mode, and the server and TEI runtime image digests. Its SHA-256 is exposed as `release_generation`; HTTP readiness optionally requires an exact configured match and, in required-vector mode, a valid live canary embedding.

## Packages

- `cmd/krx-rule-mcp`: stdio/HTTP MCP runtime.
- `cmd/krx-rule-index`: explicit BM25/vector snapshot generator.
- `internal/corpus`: strict schema-v2 Markdown/manifest/provenance contract loading.
- `internal/index`: structured legal chunking, Korean tokenization, BM25/vector scoring, generation publish/load, and RRF merge.
- `internal/mcp`: MCP tool/resource registration, strict input validation, and public response DTOs.
- `internal/security`: optional hashed multi-bearer auth, strict startup registry loading, Origin allowlist, request/query bounds, concurrency/deadline controls, rate limit, and runtime identity metrics.

## Embeddings

The local deployment examples expect an operator-selected TEI image that exposes
the compatible embeddings and health endpoints. The project does not choose,
publish, or endorse a particular TEI image. The repository-provided vector
snapshot records the model, revision, dimensions, prefix settings, and embedding
input format that the selected runtime must match.

Default settings:

- model: `intfloat/multilingual-e5-small`
- dimensions: `384`
- document prefix: `passage: `
- query prefix: `query: `
- document input: `text-v1`

Other OpenAI-compatible embedding models can be used, but vector indexing and serving must use identical model, revision, dimension, prefix, and input-format settings. Prefix-free models should set both prefix environment variables to empty strings before rebuilding the vector snapshot. The maintained generation uses raw chunk text with `text-v1`; `structured-v1` remains available for controlled comparisons and embeds fixed title/category/article/path/source fields with the chunk.

Indexing failures are strict: if vector indexing is explicitly requested and the embeddings API fails, `krx-rule-index` exits non-zero. Runtime query embedding failure falls back to BM25 only under the optional vector policy; required-vector mode returns a tool error and fails its readiness canary.

## Language-Aware RAG

Documents carry `language: "ko"` or `language: "en"`. English full-text documents generated from downloadable rule files use `{source_id}-en` as their id and keep the Korean rule id in `source_id`.

MCP search/list tools accept a `language` filter. Leave it blank for bilingual recall, set `ko` for Korean primary-text discovery, or set `en` for English-answer workflows. All returned content is still a collected derivative snapshot: current or legally sensitive answers must verify the effective Korean document at the returned official KRX source URL.
