# Architecture

The handoff between projects is a validated `data/` release. [krx-rule-markdown](https://github.com/chromato99/krx-rule-markdown) collects rules from the KRX portal and converts the attachments. This repository builds the search index and serves the corpus over stdio or stateless Streamable HTTP.

## From collection to a query

| Stage | Input and result |
| --- | --- |
| Load corpus | Verify the schema-v2 manifest, document frontmatter, paths, IDs and hashes. Read the searchable bodies and attachments. [Data contract](data-format.md) |
| Publish index | A single writer builds BM25 and optional vectors in staging, verifies the artifacts, then atomically replaces `index/current`. An interrupted build leaves the previous generation available. |
| Start server | Resolve `current` once. Verify the selected BM25 snapshot against the corpus and validate any vectors the server adopts. Load the domain lexicon and apply the configured vector/reranker policies. |
| Serve request | `search_rules` selects candidate passages. The caller reads the source using `get_context`, `get_rule` or `get_attachment`. [Search and ranking](search.md) |

## Which generation identifies what?

- **Index generation** identifies the on-disk artifact set. The corpus release hash covers the producer release. The index source hash covers retrieval inputs, and the build hash also reflects the tokenizer, chunker and indexer version. Artifact sizes and SHA-256 digests are recorded in `generations/<id>/generation.json`.
- **Release generation** identifies the serving configuration. It incorporates the loaded artifacts, embedding and reranker settings, image digests, domain lexicon, retrieval policy, candidate budget and search contract version. Use `--print-release-generation` to obtain the value for readiness checks. [Deployment](deployment.md#release-verification)
- **Authentication registry generation** identifies the loaded token file. Compare it across replicas after a credential change. [Token rotation](security.md#token-rotation)

## Code map

| Area | Entry points |
| --- | --- |
| Commands | [MCP server](../cmd/krx-rule-mcp), [index builder](../cmd/krx-rule-index), [retrieval evaluator](../cmd/krx-rule-eval) |
| Corpus and index | [Corpus validation](../internal/corpus), [shared data types](../internal/model), [chunking, ranking and generation storage](../internal/index) |
| Serving | [MCP tools and resource DTOs](../internal/mcp), [HTTP controls and metrics](../internal/security) |
| Quality | [Retrieval checks](../internal/eval), [fixtures and baselines](../eval) |
