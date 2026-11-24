# Search

`search_rules` ranks Korean and English rules, amendment notices and their converted attachments. Each result links to a document and, where available, the passages that matched. For the response fields and reading sequence, see the [client guide](llm-client.md).

## How a query is ranked

1. **Select eligible sources.** Language, document type, category and effective dates apply to both search channels. A complete rule title can narrow the source before ranking. A specific enforcement-rule title takes precedence over a parent title contained within it. An index name such as KOSPI 200 can refer to a derivatives product.
2. **Find passages.** BM25 indexes Korean, Latin and numeric tokens. Korean 2-grams and 3-grams also match partial terms. Reviewed aliases such as `동적상하한가` add lower-weight terms such as `실시간가격제한제도`. Vector search embeds the original query. Applied terms and their sources appear in `query_expansion`. [Domain lexicon](domain-lexicon.md)
3. **Merge and group.** Each channel keeps up to 120 chunk candidates by default, independent of the requested result count. Reciprocal rank fusion (RRF) combines ranks by chunk ID, then groups evidence under its owning document. Query terms, numeric claims, reviewed phrases and agreement between channels help select up to three ranked passages per document.
4. **Return context.** An optional reranker scores candidate passages for Korean queries before the server groups them again. For the first five documents, the server can append a complete owning article or paragraph within a budget of 12 chunks and 8,000 characters per document. These additional passages carry `context_only: true` and zero retrieval scores.

The default response contains up to 10 documents (`limit` maximum 50). The `score`, `bm25_score`, `vector_score` and optional reranker fields explain the ranking. To decide whether a passage supports a claim, read it with `get_context`. The active ranking policy is `chunk-rrf-source-parent-scope-v5`. [Retrieval evaluation](rag-quality-contract.md)

## Article, attachment and formula matches

- `article_id` identifies the owning article. For example, a sentence citing `제99조에 따른 …` stays in the article that contains the sentence. English body headings such as `§5` establish ownership. Contents entries and inline citations retain their surrounding context.
- `heading_path` records the chapter, section, article and 항·호·목 where present. Body and attachment matches have distinct `chunk_id` values and zero-based `chunk_index` values. Context windows stay within that body or attachment.
- The chunk target is 1,600 characters. Markdown and HTML tables split between rows and repeat the header. A row or adjacent HWP EqEdit/LaTeX pair can exceed the target to keep it readable.
- Search formulas by label or notation, for example `의무호가 제시시간 의무발생시간` or `\frac \ge 시장조성일수`. Check preserved blocks and any conversion warning when quoting an exact formula. [HWP formula fields](data-format.md#hwp-formula-blocks)

## Embedding profile

The checked-in full-vector generation uses the following settings. The indexer and server must agree on all of them.

| Setting | Bundled generation |
| --- | --- |
| `KRX_EMBEDDING_MODEL` | `intfloat/multilingual-e5-small` |
| `KRX_EMBEDDING_MODEL_REVISION` | `614241f622f53c4eeff9890bdc4f31cfecc418b3` |
| `KRX_EMBEDDING_DIMENSIONS` | `384` |
| `KRX_EMBEDDING_QUERY_PREFIX`, `KRX_EMBEDDING_DOCUMENT_PREFIX` | `"query: "`, `"passage: "` including the trailing space |
| `KRX_EMBEDDING_INPUT_FORMAT` | `text-v1` embeds chunk text. The alternative `structured-v1` adds the title and source, plus category, language, article and heading path where available. |

Set `KRX_EMBEDDING_BASE_URL` to the base URL of an OpenAI-compatible embeddings API and supply its `OPENAI_API_KEY`. For local Compose TEI the host URL is `http://127.0.0.1:18081/v1` and the key defaults to `local`. To change models, set the model, dimensions, revision, prefixes and input format together, rebuild the vectors, and serve with the same settings. Use explicit empty prefixes when appropriate. With Compose, also set `RULE_MCP_TEI_MODEL_ID` to the model served by the sidecar and choose a revision it supports. [Index commands](deployment.md#manual-index-jobs)

## Vector availability

| Runtime | Behavior |
| --- | --- |
| Binary default | Loads the required BM25 snapshot. Vector search is off. |
| `KRX_VECTOR_SEARCH_ENABLED=true`, policy `optional` | Uses compatible vectors when available. Rejected vector data or a failed query embedding falls back to BM25. |
| `KRX_VECTOR_SEARCH_ENABLED=true` plus either `KRX_VECTOR_SEARCH_POLICY=required` or `--require-vector` | Requires full coverage and a matching embedding profile at startup. Runtime embedding failures return a tool error. HTTP readiness requires a valid live canary. Compose and Kubernetes use this policy. |

All modes verify the sizes and digests declared by the active generation. A missing file or digest mismatch prevents startup even under the optional policy. If embedding fails during an explicit `krx-rule-index --vector` build, the previously published generation stays active.
