# Retrieval and caller-answer quality

Use the retrieval evaluator to check whether a caller can reach the required passages through `search_rules` and `get_context`. Evaluate the model's eventual answer in the client where it will be used.

## Run the regression check

Run the evaluator from the repository root with the matching corpus, index and a healthy E5 embeddings endpoint.

```bash
KRX_EMBEDDING_BASE_URL=http://127.0.0.1:18081/v1 \
go run ./cmd/krx-rule-eval \
  --data-dir ../krx-rule-markdown/data --index-dir ./index \
  --vector --require-vector --fail-on-gate \
  --output eval/results/retrieval.json
```

The [fixture](../eval/fixtures/retrieval.json) contains 210 maintained cases in [schema v2](../eval/schema/retrieval.schema.json). The [baseline](../eval/baselines/retrieval.json) records per-language floors, eligible counts and artifact identities. Twelve cases are marked `unavailable-source` because the requested source is absent from this corpus snapshot.

**Known English limitation.** For generation `7d9c7144…`, English document Hit@5 was 21/21 and evidence bundle Hit@5 was 20/21. In `audit-d-english-margin-repurpose`, the right document ranked first but its returned passages omitted §139. The release accepted this miss. The 21/21 evidence floor remains in force, so the full-vector gate reported a failure. [Index release evaluation](https://github.com/chromato99/krx-rule-mcp/pull/10)

## What the gate checks

| Measure | Passing condition |
| --- | --- |
| Document Hit@5 | The first five documents meet the labelled target policy (`any`, `all` or `at_least`) often enough to reach each language's floor. |
| Evidence bundle Hit@5 | Contexts reachable from those documents contain the required text or reviewed contradiction phrases for the labelled document, article or attachment. Manual and document-only targets are excluded from substantive evidence hits. |
| Returned evidence | Every returned chunk has a unique, nonempty ID, is readable in full, and belongs to the expected document, article and attachment. Status, result counts, limits and explicit filters must agree with the response. |
| Reproducibility | Full vector coverage and valid metadata. The case/target contract, corpus, index, embedding artifact, lexicon, evaluator version and first-stage candidate limit match the baseline. |

The evaluator is `rag-retrieval-evaluator-v4`. Internal candidate recall is diagnostic. A passage counts as available to the model when it is accessible through the returned results. Labels such as `insufficient` and `ambiguous` describe the caller's answer task. A related search match is a valid outcome for those queries.

- For BM25 diagnostics omit `--vector --require-vector --fail-on-gate`. Use `--split` or `--case-prefix` only for diagnostics. Use `--fixture` for a different schema-v2 set.
- Compare two reports from the same inputs with `python3 eval/compare.py before.json after.json`. Reports go in the ignored `eval/results/` directory. The [manual evaluation workflow](../.github/workflows/retrieval-eval.yml) takes the exact producer commit and runs the release gate.

## Caller LLM evaluation

Fix the client, model/version, prompt, tool budget, corpus release and retrieval settings. Record tool calls, contexts read, final claims and citations. Review correctness, unsupported claims, clarification or refusal, latency and token cost. Include ambiguous scopes, false premises, cross-document questions and cases that require more context. [Client workflow](llm-client.md)

For an independent test, reserve unseen canonical sources, grouping translations and attachments with their Korean parent. Freeze the implementation and prompt before examining results. A sealed fixture requires `--holdout-reservation` pointing to an external JSON file with `schema_version: 1` and `canonical_source_ids`. Every target must be reserved and every reserved source represented. Keep previously inspected cases for regression and preserve their thresholds.
