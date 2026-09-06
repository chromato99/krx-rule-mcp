> Historical report for the bounded search/answerability cycle. The current caller-LLM contract is described in [the follow-up](caller-llm-retrieval.md). Raw runs can be restored from [the evaluation archive](../eval/archives/README.md). Publication status is recorded in the PR follow-up.

# RAG quality and efficiency: final bounded improvement report

The final runtime improves selected evidence and search cost, but **does not
meet the independent release-quality goals**. The fixed-question merge
comparison passes. No production deployment, commit/push, or GitHub workflow
execution was performed. These are local changes on top of PR #7's `3d6fe64`.

## Measured outcomes

An answer counts as correct only when its actually selected, retrievable evidence
satisfies the labelled target and required text. Precision uses assessed answers;
coverage uses answerable questions with substantive evidence expectations.
These are MCP evidence metrics, not evaluations of generated LLM answer prose.

| Question set | Correct before -> after | Selected precision before -> after |
|---|---:|---:|
| Existing 210 (142 evidence-eligible) | 125 -> 126 | 88.65% -> 89.36% |
| Consumed source validation 39 (28 eligible) | 10 -> 17 | 47.62% -> 68.00% |
| Consumed checkpoint 38 (27 eligible) | 5 -> 16 | 41.67% -> 80.00% |
| **Final reserved 38 (27 eligible)** | **12 -> 16** | **63.16% -> 72.73%** |

Before/after comparisons use identical question/target contracts within each
row. The datasets differ and must not be compared as successive accuracy rates.
On the existing 210, Korean correct answers increase 101/114 -> 102/114; English
remains 22/26. Eight document-only cases remain explicitly unassessed.

The final reserved evaluation has 24 Korean supported questions, three English
supported questions, eight insufficient controls and three ambiguous controls:

| Final reserved result | Korean | English | All |
|---|---:|---:|---:|
| Correct / answerable | 14/24 | 2/3 | 16/27 |
| Correct / assessed answers | 14/18 | 2/4 | 16/22 |
| Correct-answer coverage | 58.33% | 66.67% | 59.26% |
| Selected precision | 77.78% | 50.00% | 72.73% |
| Insufficient questions incorrectly supported | 1 | 1 | 2 |
| Ambiguous questions incorrectly supported | 1 | 0 | 1 |

The final release gate exits 1: precision is below 95% in both languages, Korean
coverage is below 90% and Document Hit@5 below 95%, and unsafe acceptance remains.
No thresholds were reduced. All context checks pass and filter leaks are zero.
None of the three final ambiguity questions received the expected clarification;
one was incorrectly supported and two were refused as insufficient.

## What changed

- Exact BM25 postings and cached metadata replace repeated exhaustive lexical
  work. Weighted coverage now avoids tokenizing when there are no applicable
  expansion weights, and reuses applicable tokenization within a request.
- Full named-source titles constrain the candidate stage; contained parent
  titles do not override enforcement-rule titles, and different named sources
  are not collapsed. Printed English titles supplement portal metadata.
  Source names are separated from lexical topic matching, while query embeddings
  still use the original query. Explicit markets precede soft rule-name hints.
- Complete source-owned article/paragraph context can supplement the first five
  document candidates, with 12-chunk/8,000-rune per-document limits. Parent
  coverage helps ranking. Added `context_only` fragments have zero retrieval
  scores and preserve document, attachment and article-instance ownership.
- General question/claim parsing distinguishes compound objects, method questions,
  source names and English determination outcomes. No new full-sentence aliases
  or fixture-ID rules were added; the 25-entry lexicon is unchanged.
- Candidate-owner and candidate-bundle diagnostics separate retrieval availability
  from actual selected success. The release descriptor v5 binds retrieval-policy
  and answerability-gate versions. Consumed fixtures are explicitly validation.

The corpus, embedding model and immutable index were unchanged during this
improvement cycle. Earlier converter/data changes remain in the worktree and
are described in the [historical report](rag-quality-results.md).

## Efficiency and tradeoffs

Three alternating before/after runs on the same 210 questions measured:

| Measurement | Before | Final |
|---|---:|---:|
| Median of search p95 across runs | 230.40 ms | 139.99 ms |
| Range of run p95 | 225.97-234.55 ms | 138.06-142.97 ms |
| Median peak RSS of offline evaluator | 1,198.33 MiB | 1,284.92 MiB |

Search p95 falls **39.2%**; peak evaluator RSS rises **7.2%** because additional
index/cache structures are retained. This is a single-host benchmark and includes
the offline evaluator, not a production memory SLO. It is not a zero-cost change.

A separate 10-iteration service profile isolated redundant tokenization. For a
cross-scope deadline query, latency dropped 752.56 -> 254.80 ms and allocations
157.81 -> 53.10 MB per request. Allocation volume is not retained memory or RSS.
Candidate depth remains 120 per channel; no additional embedding calls, model
service, vector database or unbounded context traversal were introduced.

## Why further small tuning did not meet the goals

All 27 final answerable questions have their target owners and required text
somewhere in the bounded candidate pool. Only 16 result in correct selected
answers. Eight are refused despite available candidates; three supported answers
select inadequate evidence. Thus deeper retrieval does not address the measured
primary bottleneck. Candidate-text availability is an oracle diagnostic, not
proof that the system selected or understood it.

Confirmed examples from the final run:

- `scope-03`: the English mediation question needs enforcement-rule forms, but
  the response selects the parent regulation and drops the target document.
- `scope-14`: the correct capital-variable document is returned, but its routine
  calculation/provision articles do not satisfy the requested purpose/role text.
- `scope-22`: a question about review, notification and designation dates selects
  insufficient parts of the correct article and unrelated document evidence.
- `scope-negative-03` and `scope-negative-08`: petroleum-market words are mistaken
  for support about passport approval and a satellite's orbital period.
- The procedure/date/condition questions refused by lexical/topic/claim checks show the
  opposite problem: related evidence can be present without passing the parser.

These observations support a bottleneck in document/instance selection, complete
condition assembly and semantic answerability. They do not prove that every
unmatched target is a legally wrong alternative answer; labels and scope still
need expert review. The earlier checkpoint's sole automatic clarification success
was also manually inadequate: a KONEX question received KOSDAQ/derivatives
citations and a request to specify an already-given market. A nonempty clarification
is therefore not sufficient evidence of useful clarification.

The controlled experiments rejected several tempting fixes:

| Rejected variant | Evidence |
|---|---|
| Generic original-heading bonus + verb normalization | Lost two English correct answers and accepted an unsupported tax question on 210. |
| Global Korean verb normalization alone | Introduced the same unsafe control acceptance. |
| Ranking-only verb normalization | Added no correct answers and reduced first-evidence hits. |
| Document score from only its strongest chunk | Gained one on consumed38 but lost two on 210. |
| Bare Korean market aliases + early metadata scope guard | Reduced consumed38 from 15 to 14 correct and increased assessed errors. |
| Remove parent ranking/context | Lost the extra correct Korean answer on 210. |

Only explicit-market-before-soft-title ordering from the scope follow-up was
retained; it recovers the emissions mediation case without those regressions.
No case-specific exceptions were added to force the failed numbers to pass.

## Larger alternatives reviewed, not applied

[BEIR](https://arxiv.org/abs/2104.08663) supports checking domain transfer and
retaining a lexical baseline. The current candidate availability is not a reason
to replace the engine with a vector database or simply increase top-K.

A bounded semantic reranker is a plausible next experiment, following the
[Sentence Transformers retrieve/rerank design](https://www.sbert.net/examples/sentence_transformer/applications/retrieve_rerank/README.html).
An optional adapter already exists, but no local reranker service was available.
Its KRX gain and operating latency were not measured, so no model service or
production default was added. Relevance scores alone would still not prove a
causal, exception or scope claim.

[Contextual retrieval](https://www.anthropic.com/engineering/contextual-retrieval)
and [parent retrieval](https://developers.llamaindex.ai/python/framework/integrations/retrievers/auto_merging_retriever/)
motivated bounded use of existing legal structure. Generated contextual prose,
full re-embedding, a citation graph, typed condition/role extraction and semantic
verification by the RAG client would require broader contracts and new evaluation.
[M3-Embedding](https://arxiv.org/abs/2402.03216) is a model-family reference, not an
adopted replacement or a measured KRX improvement.

If work resumes, use exposed failures as validation, reserve new final sources,
and evaluate semantic source/condition selection with controlled latency. Changing
aliases or thresholds around these exact questions would not establish quality.

## Freeze, reproducibility and validation

Final runtime source SHA-256:
`031c48a6db078232b368bd8a9d3bbae42d066369efc60ae4ce6a9290e0b24a84`.
The new sources were reserved before scope changes; labels were written/audited
only after this freeze. Original and final binaries each ran once on the final
fixture. Source and label hashes were checked afterward. No subsequent tuning.

The 12 canonical target groups are disjoint from all 210 + 39 + 38 development
questions. Related rule families and translations can overlap. Questions are
internally authored; three English supported cases and this small pilot do not
constitute independent external certification or prove the absence of overfit.

- `go test ./...`, `go test -race ./...`, `go vet ./...`: passed.
- Actual corpus, full-vector and attachment/evaluation integration: passed.
- Source/fixture grounding, canonical exclusion and hash checks: passed.
- Immutable full-vector artifact check: passed; 51,929/51,929 vectors.
- Actual local HTTP MCP: initialize, bilingual search, selected context ownership,
  default/1/5/50 refusal, clarification and readiness: passed. The temporary
  server was stopped; the embedding container remains unchanged.
- Manual workflow remains dispatch-only and targets the current frozen fixture.
  No GitHub Actions run, production rollout, commit or push was performed.

Runtime generation: `27e34c90448e4b25b5d9763d2052fba60c10b1b9b4f09c47ca91be493d2d44fa`.
Corpus: `469cda6799dfa33d7f8e88e175a6f745f81851a4f76d99e4529e9a4f7a7f5c84`.
Index: `70e23cf74164d137ba35316e875edac3350ed7ff0f944c04a9e6fd443034e78f`.
Model: multilingual-e5-small, 384D, text-v1, revision
`614241f622f53c4eeff9890bdc4f31cfecc418b3`; the actual server cache revision was checked.

Inputs: [original final fixture](../eval/experiments/20260906-caller-contract/fixture-before.json),
[intake](../eval/source/rag-holdout-scope-2026-09-06.json).
Evidence: [summary and failure audit](../eval/experiments/20260906-scope/summary.json),
[freeze](../eval/experiments/20260906-scope/freeze.json),
[performance](../eval/experiments/20260906-scope/performance.json),
[adjudication](../eval/experiments/20260906-scope/adjudication.md),
[experiment history](../eval/experiments/20260906/experiment-log.md).
The current README and `eval/compare.py` use retrieval evaluator v3. This
historical cycle used selected-answer metrics; inspect its archived reports
and source snapshot rather than comparing their values as current retrieval
metrics. Full run reports are preserved in the evaluation archive.
