# Search

## BM25

BM25 is required for serving. Build it after placing a generated corpus in `KRX_RULE_DATA_DIR`:

```bash
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index "$KRX_RULE_DATA_DIR/index/bm25.krxidx"
```

Freshness is based on the corpus hash, not file mtimes. The hash includes document `content_hash` and each attachment's `id`, `status`, `text_path`, and `content_hash`.
It also includes language/source metadata so adding or changing English full-text documents invalidates stale snapshots.

Useful flags:

- `--check`: write nothing; exit 0 when current, exit 1 when missing/stale.
- `--force`: rebuild even when current.

The tokenizer extracts Korean, Latin, and numeric tokens, and adds Korean 2-gram/3-gram tokens so partial Korean phrases can match without a morphological analyzer.

Converted attachments are indexed as chunks attached to their parent rule or notice. If an attachment chunk is the best match, the result returns the parent document and includes `matched_source: "attachment"` plus `attachment_matches`.

## Formula-Aware Retrieval

HWP formulas converted by `krx-rule-markdown` are indexed as ordinary attachment Markdown. A formula section contains both the original `hwp-equation` source and the generated LaTeX `math` block, so queries can match either representation.

Useful query shapes include:

```json
{"query": "의무호가 제시시간 의무발생시간 일중의무이행률", "language": "ko"}
```

```json
{"query": "\\frac \\ge 시장조성일수 의무충족일수", "language": "ko"}
```

The generated LaTeX is intended to improve retrieval and synthesis, not to replace the original HWP equation. For exact formula answers, call `get_attachment` or read `krx-rule://attachments/{id}` and inspect the adjacent `hwp-equation` and `math` blocks together.

When a matched attachment contains HWP formulas, `search_rules` adds `formula_notice` to the result and to the matching `attachment_matches` item:

```json
{
  "formula_notice": {
    "severity": "info",
    "code": "hwp_formula_latex_best_effort",
    "source_equation_available": true,
    "generated_latex_available": true,
    "formula_count": 1
  }
}
```

The notice is intentionally informational rather than fatal. It tells RAG clients that the result is usable, but exact formula claims should be verified against the adjacent `hwp-equation` source or the original HWP attachment.

After formula conversion code or converted attachment Markdown changes, rebuild indexes. `krx-rule-index --check` will report stale snapshots because corpus hashes include attachment metadata and content hashes.

## Language Filtering

Search, list, and recent-change tools accept `language`.

```json
{"query": "listing review", "language": "en"}
```

```json
{"query": "상장 심사", "language": "ko"}
```

Leave `language` empty for bilingual recall. Search results include `language` and, for English full-text documents, `source_id` linking back to the Korean rule id.

## Vector Search

Vector search is optional. It is enabled only when all of these are true:

- `KRX_VECTOR_SEARCH_ENABLED=true`
- a vector snapshot path is configured
- the vector snapshot matches the current corpus
- vector metadata matches model, dimensions, query prefix, and document prefix
- query embeddings can be created at runtime

Build a vector snapshot with the local TEI sidecar:

```bash
docker compose up -d krx-rule-embeddings

OPENAI_API_KEY=local \
KRX_EMBEDDING_BASE_URL=http://127.0.0.1:18081/v1 \
KRX_EMBEDDING_MODEL=intfloat/multilingual-e5-small \
KRX_EMBEDDING_DIMENSIONS=384 \
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index "$KRX_RULE_DATA_DIR/index/bm25.krxidx" \
  --vector-index "$KRX_RULE_DATA_DIR/index/vectors.krxvec"
```

For a cheaper smoke test, add `--vector-sample-query "상장 심사"` and `--vector-sample-per-query 16`, or cap work with `--vector-limit`.

## External Embeddings API

Any OpenAI-compatible `/v1/embeddings` endpoint can replace TEI:

```bash
export KRX_EMBEDDING_BASE_URL=https://api.openai.com/v1
export OPENAI_API_KEY=...
export KRX_EMBEDDING_MODEL=text-embedding-3-small
export KRX_EMBEDDING_DIMENSIONS=1536
```

Use the same settings for vector indexing and MCP serving. E5 defaults are:

```bash
export KRX_EMBEDDING_QUERY_PREFIX="query: "
export KRX_EMBEDDING_DOCUMENT_PREFIX="passage: "
```

When both BM25 and vector scores are available, results are merged with reciprocal rank fusion. If vector search is unavailable at runtime, the server logs the reason and returns BM25 results.
