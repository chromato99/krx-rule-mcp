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
| Revision | `614241f622f53c4eeff9890bdc4f31cfecc418b3` |
| Dimensions | `384` |
| Document prefix | `passage: ` |
| Query prefix | `query: ` |
| Embedding input | `text-v1` |

`krx-rule-index` requires the producer's strict schema-v2 `manifest.json`. It verifies manifest/document parity and both `index_source_hash` and `release_hash` before building. A non-blocking single-writer lock prevents concurrent publishers. BM25, vector, metadata, and `generation.json` are completed and validated in a sibling staging directory before `current` is replaced atomically, so a failed or killed build leaves the previous generation selected.

Check the bundled snapshots before serving:

```bash
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir ./index \
  --vector \
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

Freshness is based on the verified corpus release hash plus deterministic index source/build hashes, not file mtimes. The index source contract includes searchable document and attachment content and retrieval-relevant metadata; the build hash additionally binds the tokenizer/chunker/indexer version. The CLI publishes only beneath `<dir>/generations/` and atomically updates `<dir>/current`.

Useful flags:

- `--check`: write nothing; exit 0 when current, exit 1 when missing/stale.
- `--force`: rebuild even when current.

The tokenizer extracts Korean, Latin, and numeric tokens, and adds Korean 2-gram/3-gram tokens so partial Korean phrases can match without a morphological analyzer.

### Structured legal chunks

Snapshot format v6 treats the 1,600-rune chunk size as a target rather than a destructive hard limit. The chunker recognizes chapter/section headings, owning Korean article headings, English body `§N`/`§N-N` headings, and Korean 항·호·목 markers. English table-of-contents entries and inline `[§N]` citations cannot become owning articles. Index-layer search results and chunk context carry:

- `article_id`: the owning heading such as `제5조` or `제11조의2`.
- `heading_path`: the ordered chapter, section, full article heading, 항, 호, and 목 path.

Only a structural heading at the beginning of a block can change `article_id`. A sentence such as `제99조에 따른 ...` remains owned by the preceding article, so cited provisions are not presented as the chunk's source anchor.

Semantic units are kept intact:

- adjacent `hwp-equation` and `math` fenced blocks form one atomic chunk;
- Markdown and HTML tables split only between rows and repeat their header;
- a single table row or equation pair larger than 1,600 runes is emitted as one oversized chunk instead of being cut in the middle.

The hermetic retrieval benchmark runs in ordinary Go CI and gates Recall@5, attachment hits, anchors, and filter isolation. Its clearing expectation follows the current corpus wording: `clearing settlement 최종결제가격` resolves to `파생상품시장 업무규정 시행세칙` `제5조`, rather than requiring a title containing `청산`.

Converted attachments are indexed as chunks attached to their parent rule or notice. If an attachment chunk is selected, the parent document includes it in `evidence_matches` and exposes attachment metadata in `attachment_matches`.
Each evidence match includes its `chunk_id`, zero-based `chunk_index`, source, owning article and heading path. Attachment matches include their own chunk anchor as well. Index 0 is serialized explicitly. Domain lexicon expansion terms are scored with lower BM25 weight than the original query terms. Use evidence chunk IDs with `get_context` to fetch the exact chunk and neighboring chunks before writing an answer.

BM25 and vector retrieval keep bounded chunk candidates independently. Reciprocal-rank fusion is keyed by chunk ID, not document ID; only after fusion are up to three diverse evidence chunks grouped into each document. A bounded lexical-coverage signal breaks weak RRF ties without treating a ranking score as confidence. Filter eligibility is computed once before both channel scans, and vector norms are precomputed when the generation is loaded.
문서 안의 최종 evidence 순서는 원 질의의 lexical coverage, high-confidence reviewed intent expansion, BM25/vector 채널 일치, 질의에 명시된 수치 claim을 사용해 다시 정렬합니다. Expansion phrase가 조문 heading에 직접 나타나면 간접 인용보다 우선하고, 구체적인 expansion phrase가 첨부 본문에 나타나면 일반 조문보다 우선할 수 있습니다. 각 evidence의 `score`는 이 최종 순서에 사용된 점수이고, `bm25_score`와 `vector_score`는 retrieval channel 진단값입니다. 이 신호들은 bounded retrieval 후보 안에서만 순서를 바꾸며 answerability confidence로 사용하지 않습니다.

선택형 한국어 reranker가 활성화되면 한국어 질의의 상위 20개 chunk를 cross-encoder로 재정렬합니다. 문서 점수는 baseline RRF로 고정되고 cross-encoder는 문서 내부 evidence 순서에 관여합니다. 실행과 채택에 답변 가능 판정을 사용하지 않습니다. `reranker_score`와 `reranker_rank`는 순위 진단값이며 confidence가 아닙니다.


`score`, `bm25_score`, and `vector_score` are ranking signals. They are useful for ordering and debugging retrieval, but they are not confidence probabilities.

## Domain Query Expansion

Before BM25/vector search, `search_rules` applies the KRX domain lexicon loaded at server startup. The default file is `config/domain-lexicon.yaml`. It is based on KRX official pages and corpus-derived rule terminology, and is meant to bridge user wording to official terms.

Example: `동적상하한가` is expanded with terms such as `실시간가격제한제도`, `실시간 가격제한의 가격변동폭`, `가격변동폭`, `파생상품시장 업무규정 시행세칙`, and `별표25`.

Expansion은 후보 탐색과 evidence 재정렬 신호입니다. 알려진 별칭이 일치해도 추가 조건이나 질문의 전제가 확인된 것은 아닙니다. 서버는 확장된 검색 후보를 반환하고 호출 LLM이 원 질문과 근거를 대조합니다.

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

## Retrieval contract and caller assessment

`search_rules` returns `contract_version: "retrieval-v1"`, ranked `results` and a
`retrieval` object with `status` and `returned_results`:

- `candidates_found`: at least one candidate is returned for review.
- `no_candidates`: this query and its filters returned no candidates. It does
  not establish that no applicable rule exists in the corpus or elsewhere.

Neither status declares an answer supported, insufficient, ambiguous or true.
The previous `answerable` and `answerability` fields are removed. Semantic
uncertainty no longer clears results. A query with a false premise can retrieve
contradicting evidence, and a broad question can return multiple candidates.
Index incompatibility, invalid input and required embedding/reranker failures
remain tool errors. Explicit filters, source ownership, output limits and
conversion quality notices remain enforced.

The default result limit is 10, with a maximum of 50. Candidate depth remains
120 per channel independently. Internal candidate traces are evaluation-only;
the public response includes only the bounded result set. `evidence_matches`
are candidate passages, not a certified answer bundle. A metadata-only document
candidate can be inspected with `get_rule`; it does not count as retrieved
substantive evidence in the evaluator.

The calling LLM reads `get_context`, checks subject/market/date, conditions,
exceptions and references, and decides whether to answer, search further or
ask for missing scope. See [the client workflow](llm-client.md). Server initialize
instructions and tool descriptions carry this guidance. Both `structuredContent`
and a serialized JSON text block contain the same tool result; a host should
forward one representation to the model. The complete HTTP response size limit
still covers both representations and the envelope.

The retrieval evaluator measures document Hit@5 and the required evidence
bundle within the first five returned results. It verifies every returned
chunk's owner and full context, while internal candidate recall stays diagnostic.
Question labels requiring refusal or clarification are reserved for caller
answer evaluation; returning a candidate is not counted as answering them.

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
- vector metadata matches model, revision, dimensions, query/document prefixes, and embedding input format
- query embeddings can be created at runtime

Build a vector snapshot with the local TEI sidecar:

```bash
docker compose up -d krx-rule-embeddings

OPENAI_API_KEY=local \
KRX_EMBEDDING_BASE_URL=http://127.0.0.1:18081/v1 \
KRX_EMBEDDING_MODEL=intfloat/multilingual-e5-small \
KRX_EMBEDDING_MODEL_REVISION=614241f622f53c4eeff9890bdc4f31cfecc418b3 \
KRX_EMBEDDING_DIMENSIONS=384 \
KRX_EMBEDDING_INPUT_FORMAT=text-v1 \
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR" \
  --vector
```

For a cheaper smoke test, add `--vector-sample-query "상장 심사"` and `--vector-sample-per-query 16`, or cap work with `--vector-limit`.

When using a different TEI model, set both the sidecar model and the MCP embedding model to the same id, set the correct output dimensions, then rebuild the vector snapshot:

```bash
export RULE_MCP_TEI_MODEL_ID=BAAI/bge-m3
export KRX_EMBEDDING_MODEL=BAAI/bge-m3
export KRX_EMBEDDING_MODEL_REVISION=replace-with-pinned-model-revision
export KRX_EMBEDDING_DIMENSIONS=1024
export KRX_EMBEDDING_QUERY_PREFIX=""
export KRX_EMBEDDING_DOCUMENT_PREFIX=""

docker compose up -d --force-recreate krx-rule-embeddings

OPENAI_API_KEY=local \
KRX_EMBEDDING_BASE_URL=http://127.0.0.1:18081/v1 \
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR" \
  --vector \
  --force
```

Use the prefixes and document format recommended by the model. E5 uses `query: ` and `passage: `; those prefixes and the pinned E5 revision are defaults only when the selected model is the repository default. A non-default model must set `KRX_EMBEDDING_DIMENSIONS` and otherwise starts with an empty revision and empty prefixes, preventing E5 conventions from silently contaminating another profile. The maintained generation's `text-v1` embeds only raw chunk text. `structured-v1` prepends fixed `title`, `category`, `article`, `path`, and `source` fields and remains available for controlled comparisons. Any format change requires a full vector rebuild because `input_format` is validated in vector metadata and the immutable generation descriptor.

## External Embeddings API

Any OpenAI-compatible `/v1/embeddings` endpoint can replace TEI:

```bash
export KRX_EMBEDDING_BASE_URL=https://api.openai.com/v1
export OPENAI_API_KEY=...
export KRX_EMBEDDING_MODEL=text-embedding-3-small
export KRX_EMBEDDING_DIMENSIONS=1536
export KRX_EMBEDDING_QUERY_PREFIX=""
export KRX_EMBEDDING_DOCUMENT_PREFIX=""
export KRX_EMBEDDING_INPUT_FORMAT=structured-v1
```

Rebuild the vector snapshot after changing any embedding setting:

```bash
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR" \
  --vector \
  --force
```

Use the same settings for vector indexing and MCP serving. E5 defaults are:

```bash
export KRX_EMBEDDING_QUERY_PREFIX="query: "
export KRX_EMBEDDING_DOCUMENT_PREFIX="passage: "
export KRX_EMBEDDING_INPUT_FORMAT=text-v1
```

When both BM25 and vector scores are available, bounded chunk candidates are merged with reciprocal rank fusion before document grouping. The query embedding always uses the original user query; reviewed lexicon expansion remains a lower-weight BM25 signal and does not overwrite the vector query. Under the `optional` policy, an unavailable runtime embedder is logged and the server returns BM25 results. Under the `required` policy, embedding failures and invalid vectors return a tool error and `/readyz` returns 503 until a valid canary embedding succeeds.

## Current bounded retrieval policy

`chunk-rrf-source-parent-scope-v5` keeps 120 candidates per channel independent
of the requested result count. BM25 uses derived in-memory postings with the
same scoring formula. Query-independent metadata is cached; weighted expansion
coverage skips inactive weights and reuses tokenization within each request.

A complete named rule title can constrain retrieval before top-K truncation.
Contained parent titles do not override a more specific enforcement-rule title;
different named sources are not collapsed into the longest title. Printed
English legal titles supplement abbreviated/Korean portal titles. Within a
named source, its name is removed from lexical topic matching; the embedding
still receives the original query. Explicit market filtering precedes softer
generic rule-title hints. Numbered index names such as KOSPI 200 and KOSDAQ 150
do not impose a cash-market filter on derivatives/product queries.

Within the first five document candidates, complete owning-article or paragraph
context can supplement retrieved evidence, bounded by 12 chunks and 8,000 runes
per document. It preserves document, attachment and article-instance ownership.
Supplemental evidence is marked `context_only`; its retrieval scores and lexical
coverage remain zero. Ranking may use bounded parent coverage; the calling LLM assesses
the concrete source text retrieved through the returned chunk IDs. These ranking
signals are not confidence estimates or proof of every legal condition.

The release descriptor v6 includes the retrieval policy and public search
contract version (`retrieval-v1`). Retrieval reports distinguish internal
candidate presence from accessible evidence in the actual top-5 results.
See [the maintained evaluation contract](rag-quality-contract.md).
