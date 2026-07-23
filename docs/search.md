# Search

## Published index generations

Indexes are published as immutable, content-addressed generations under the configured index directory:

- `index/current`: the lowercase SHA-256 ID of the active generation.
- `index/generations/<id>/generation.json`: artifact names, sizes, SHA-256 digests, corpus release hash, and index source/build hashes.
- `index/generations/<id>/bm25.krxidx`: required BM25 snapshot.
- `index/generations/<id>/vectors.krxvec`: optional vector snapshot.
- `index/generations/<id>/vectors.krxvec.meta.json`: optional vector metadata sidecar.

These files are generated from the maintained `krx-rule-markdown/data` corpus with the default E5 embedding settings:

| Field | Value |
| --- | --- |
| Model | `intfloat/multilingual-e5-small` |
| Dimensions | `384` |
| Document prefix | `passage: ` |
| Query prefix | `query: ` |

`krx-rule-index` requires the producer's strict schema-v2 `manifest.json`. It verifies manifest/document parity and both `index_source_hash` and `release_hash` before building. A non-blocking single-writer lock prevents concurrent publishers. BM25, vector, metadata, and `generation.json` are completed and validated in a sibling staging directory before `current` is replaced atomically, so a failed or killed build leaves the previous generation selected.

Check the bundled snapshots before serving:

```bash
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir ./index \
  --vector-index ./index/vectors.krxvec \
  --check
```

If the check reports a stale, missing, or invalid generation, rebuild it. The server resolves `current` once, verifies every artifact against `generation.json`, and then loads only that immutable generation. Required-vector mode rejects a generation without a complete compatible vector companion.

## BM25

BM25 is required for serving. Build it after placing a generated corpus in `KRX_RULE_DATA_DIR`:

```bash
export KRX_RULE_INDEX_DIR=/opt/krx-rule-index
mkdir -p "$KRX_RULE_INDEX_DIR"

go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR"
```

Freshness is based on the verified corpus release hash plus deterministic index source/build hashes, not file mtimes. The index source contract includes searchable document and attachment content and retrieval-relevant metadata; the build hash additionally binds the tokenizer/chunker/indexer version. `--index` remains a compatibility way to select the directory by naming `<dir>/bm25.krxidx`, but the CLI never writes that flat path: it publishes beneath `<dir>/generations/` and updates `<dir>/current`.

Useful flags:

- `--check`: write nothing; exit 0 when current, exit 1 when missing/stale.
- `--force`: rebuild even when current.

The tokenizer extracts Korean, Latin, and numeric tokens, and adds Korean 2-gram/3-gram tokens so partial Korean phrases can match without a morphological analyzer.

### Structured legal chunks

Snapshot format v6 treats the 1,600-rune chunk size as a target rather than a destructive hard limit. The chunker recognizes chapter/section headings, owning article headings, and Korean 항·호·목 markers. Index-layer search results and chunk context carry:

- `article_id`: the owning heading such as `제5조` or `제11조의2`.
- `heading_path`: the ordered chapter, section, full article heading, 항, 호, and 목 path.

Only a structural heading at the beginning of a block can change `article_id`. A sentence such as `제99조에 따른 ...` remains owned by the preceding article, so cited provisions are not presented as the chunk's source anchor. The former heuristic `article_range` is not serialized or populated.

Semantic units are kept intact:

- adjacent `hwp-equation` and `math` fenced blocks form one atomic chunk;
- Markdown and HTML tables split only between rows and repeat their header;
- a single table row or equation pair larger than 1,600 runes is emitted as one oversized chunk instead of being cut in the middle.

The hermetic retrieval benchmark runs in ordinary Go CI and gates Recall@5, attachment hits, anchors, and filter isolation. Its clearing expectation follows the current corpus wording: `clearing settlement 최종결제가격` resolves to `파생상품시장 업무규정 시행세칙` `제5조`, rather than requiring a title containing `청산`.

Converted attachments are indexed as chunks attached to their parent rule or notice. If an attachment chunk is the best match, the result returns the parent document and includes `matched_source: "attachment"` plus `attachment_matches`.
Each search result includes `matched_chunk_id` and the zero-based `matched_chunk_index`. Attachment matches include their own `chunk_id` and zero-based `chunk_index`; index 0 is serialized explicitly. The public API does not expose heuristic article-range guesses. Domain lexicon expansion terms are scored with lower BM25 weight than the original query terms. Use these ids with `get_context` to fetch the exact matched chunk and neighboring chunks before writing an answer.

`score`, `bm25_score`, and `vector_score` are ranking signals. They are useful for ordering and debugging retrieval, but they are not confidence probabilities.

## Domain Query Expansion

Before BM25/vector search, `search_rules` applies the KRX domain lexicon loaded at server startup. The default file is `config/domain-lexicon.yaml`. It is based on KRX official pages and corpus-derived rule terminology, and is meant to bridge user wording to official terms.

Example: `동적상하한가` is expanded with terms such as `실시간가격제한제도`, `실시간 가격제한의 가격변동폭`, `가격변동폭`, `파생상품시장 업무규정 시행세칙`, and `별표25`.

When expansion is applied, the response includes `query_expansion`:

```json
{
  "mode": "bm25+domain-expansion",
  "query_expansion": {
    "original_query": "동적상하한가",
    "expanded_query": "동적상하한가 실시간가격제한제도 ...",
    "applied_terms": [
      {
        "id": "derivatives_realtime_price_limit",
        "canonical": "실시간가격제한제도",
        "matched_terms": ["동적상하한가"],
        "confidence": "high",
        "review_status": "curated",
        "source_urls": ["https://regulation.krx.co.kr/contents/RGL/03/03050600/RGL03050600.jsp"]
      }
    ]
  }
}
```

The lexicon improves recall only. RAG answers should cite the actual rule, attachment, or context returned by MCP tools. See `docs/domain-lexicon.md` for source policy, current source URLs, and the YAML schema.

## Matched Context

RAG clients should use `search_rules` for recall and then call `get_context` for evidence:

```json
{
  "chunk_id": "210205830#att-210205830-210107342-hwp-3",
  "before_chunks": 1,
  "after_chunks": 1,
  "max_chars": 6000
}
```

`get_context` keeps context within the same source:

- body matches return nearby chunks from the same rule or notice body.
- attachment matches return nearby chunks from the same converted attachment.

The response includes `document`, `chunks`, and combined `content`. The combined content marks each chunk with an HTML comment containing `chunk_id`, `source`, and, for attachments, `attachment_id`.

Set `before_chunks` or `after_chunks` to `0` when only the target chunk is needed. `get_rule` and `get_attachment` default to 20,000 characters and allow at most 50,000 per call. If `truncated` is true, pass `next_offset` back as `offset`; `total_chars` is the full source length. `get_context` uses the same default and maximum cap. Resource text is also capped at 50,000 characters and reports truncation in `_meta`; use the corresponding paginated tool for continuation. Independently, serialized structured tool output is capped at 512KiB (`RULE_MCP_TOOL_OUTPUT_SIZE_LIMIT`) and the complete synchronous JSON-RPC wire response is capped at 1MiB (`RULE_MCP_RESPONSE_SIZE_LIMIT`) by default. Reduce `limit` or `max_chars` if a response would exceed either bound. List tools expose `limit`, `offset`, `total`, and `next_offset`; `list_recent_changes` defaults to 20 rows. Use `list_categories` to discover exact category strings before applying the `category` filter.

Inputs are validated strictly: `query` is required and bounded, `document_type` must be `rule` or `notice`, dates must be real `YYYY-MM-DD` values in ascending range, and negative or oversized limits/offsets are rejected rather than silently coerced. Public document and attachment DTOs omit local paths and converter error strings. A verified `official_source` contains only the KRX source page, POST endpoint, whitelisted stable parameters, and source-content hash. Each source reports `searchable`; false sources are excluded from text indexing. A document or matched attachment with degraded conversion metadata carries a `quality_notice` so the warning stays attached to the exact source.

Search results are discovery aids from a collected derivative snapshot. Ranking scores are not confidence probabilities, English text is not a substitute for the Korean legal text, and converted attachments may lose tables, images, or formula semantics. For current or compliance-sensitive answers, follow `source_url` and verify the effective Korean document on the official KRX portal.

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

If converted text looks formula-like but no preserved EqEdit block or generated LaTeX block is available, the server returns a weaker notice:

```json
{
  "formula_notice": {
    "severity": "info",
    "code": "formula_text_detected",
    "source_equation_available": false,
    "generated_latex_available": false,
    "formula_count": 2
  }
}
```

Treat `formula_text_detected` as a retrieval hint, not as confirmation that the original HWP equation was structurally preserved.

After formula/table conversion code or converted attachment Markdown changes, publish a new generation. `krx-rule-index --check` reports the active generation stale because corpus hashes include attachment metadata and content hashes.

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
- the active immutable generation contains a vector artifact and metadata
- the vector snapshot matches the current corpus/index generation
- vector metadata matches model, dimensions, query prefix, and document prefix
- query embeddings can be created at runtime

Build a vector snapshot with the local TEI sidecar:

```bash
docker compose up -d krx-rule-embeddings

OPENAI_API_KEY=local \
KRX_EMBEDDING_BASE_URL=http://127.0.0.1:18081/v1 \
KRX_EMBEDDING_MODEL=intfloat/multilingual-e5-small \
KRX_EMBEDDING_MODEL_REVISION=614241f622f53c4eeff9890bdc4f31cfecc418b3 \
KRX_EMBEDDING_DIMENSIONS=384 \
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR" \
  --vector-index "$KRX_RULE_INDEX_DIR/vectors.krxvec"
```

For a cheaper smoke test, add `--vector-sample-query "상장 심사"` and `--vector-sample-per-query 16`, or cap work with `--vector-limit`.

When using a different TEI model, set both the sidecar model and the MCP embedding model to the same id, set the correct output dimensions, then rebuild the vector snapshot:

```bash
export RULE_MCP_TEI_MODEL_ID=BAAI/bge-m3
export KRX_EMBEDDING_MODEL=BAAI/bge-m3
export KRX_EMBEDDING_DIMENSIONS=1024
export KRX_EMBEDDING_QUERY_PREFIX=""
export KRX_EMBEDDING_DOCUMENT_PREFIX=""

docker compose up -d --force-recreate krx-rule-embeddings

OPENAI_API_KEY=local \
KRX_EMBEDDING_BASE_URL=http://127.0.0.1:18081/v1 \
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR" \
  --vector-index "$KRX_RULE_INDEX_DIR/vectors.krxvec" \
  --force
```

Use the prefixes recommended by the model. E5 uses `query: ` and `passage: `; many non-E5 models use no prefix, which can be represented by setting the prefix environment variables to empty strings.

## External Embeddings API

Any OpenAI-compatible `/v1/embeddings` endpoint can replace TEI:

```bash
export KRX_EMBEDDING_BASE_URL=https://api.openai.com/v1
export OPENAI_API_KEY=...
export KRX_EMBEDDING_MODEL=text-embedding-3-small
export KRX_EMBEDDING_DIMENSIONS=1536
export KRX_EMBEDDING_QUERY_PREFIX=""
export KRX_EMBEDDING_DOCUMENT_PREFIX=""
```

Rebuild the vector snapshot after changing any embedding setting:

```bash
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR" \
  --vector-index "$KRX_RULE_INDEX_DIR/vectors.krxvec" \
  --force
```

Use the same settings for vector indexing and MCP serving. E5 defaults are:

```bash
export KRX_EMBEDDING_QUERY_PREFIX="query: "
export KRX_EMBEDDING_DOCUMENT_PREFIX="passage: "
```

When both BM25 and vector scores are available, results are merged with reciprocal rank fusion. Under the `optional` policy, an unavailable runtime embedder is logged and the server returns BM25 results. Under the `required` policy, embedding failures and invalid vectors return a tool error and `/readyz` returns 503 until a valid canary embedding succeeds.
