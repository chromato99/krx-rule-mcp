# Bounded retrieval improvement, 2026-09-06

## Objectives and constraints

Improve the number of answers with correct selected evidence and reduce unsafe
answers on both the existing 210 questions and the consumed 39-question source
validation set. Preserve question/target contracts and report Korean and English
separately. Existing merge and release thresholds remain unchanged. A passing
development comparison is not independent release evidence.

Keep the current Go service, corpus, text-v1 embeddings, and immutable index.
Do not train on evaluation questions, add sentence-specific aliases, introduce
unbounded query expansion, or add a model service without a measured need.
Measure repeated paired latency, memory and work as well as retrieval quality.
Prefer exact computation reuse and bounded source-owned evidence assembly.

## Pre-registered order

1. Reproduce the current full-vector 210 and 39 baselines. Reserve new canonical
   sources before changes; the previous 39 questions are now consumed validation.
2. Remove repeated lexical work while preserving scores and ordering: inverted
   postings, query-independent metadata, and bounded scope lookup.
3. Test exact named-source selection and same-article/paragraph evidence
   assembly. Preserve concrete chunk IDs, source ownership, output bounds and
   ranking provenance; context expansion is not a new independent retrieval hit.
4. Repair general question-versus-assertion parsing and topic verification using
   grammatical counterexamples, not the literal failed evaluation sentences.
5. Compare contributions individually, reject variants that merely trade away
   correct answers or add disproportionate work. Inspect remaining candidate
   misses separately from ranking, incomplete context and entailment failures.
6. Freeze the implementation and construct new source-reserved questions from
   `eval/experiments/20260906/reservation.json`. Audit labels before retrieval,
   run once, and retain failures without subsequent tuning in this cycle.
7. Complete code, race, corpus/vector and actual MCP checks. Report why any gate
   remains out of reach and review larger alternatives without adopting them.

## Research informing the experiments

- [BEIR](https://arxiv.org/abs/2104.08663) motivates retaining lexical baselines
  and evaluating domain transfer rather than assuming dense retrieval wins.
- [Sentence Transformers retrieve and rerank](https://www.sbert.net/examples/sentence_transformer/applications/retrieve_rerank/README.html)
  motivates a bounded reranker only when candidate recall supports it; scoring
  every corpus chunk with a cross-encoder would be inappropriate here.
- [Anthropic contextual retrieval](https://www.anthropic.com/engineering/contextual-retrieval)
  motivates checking lost chunk context. Our initial experiment uses existing
  legal ownership metadata, without generating contextual prose or rebuilding
  all embeddings. Their reported gains are not estimates for this corpus.

Results and rejected alternatives will be recorded here after evaluation.

## Intermediate evidence (not release approval)

The fresh 12-source reservation was written before this cycle's changes. The
39-case set is explicitly copied to `validation-39.json` with identical input
and target contracts; its old sealed fixture/result are historical artifacts.

- Reproduced current 210: 125 correct, 141 assessed, 16 incorrect selected
  answers, zero unsafe insufficient/ambiguous acceptance; search p95 270.85 ms.
- Exact postings/cache change preserved all quality metrics; p95 175.68 ms in
  the first run. Paired repeated latency and memory checks are still pending.
- Source/topic and parent-context candidate: consumed 39 correct 10 -> 17,
  precision 47.62% -> 68%, eight insufficient controls all refused. Two
  ambiguous questions are still incorrectly supported. The existing 210
  retains 125 correct / 141 assessed and passes its comparison gate.
- Broad original-heading weighting plus globally normalized Korean verb forms
  reached 18/28 on the consumed set but lost two English correct answers and
  falsely accepted a tax-exemption query on the 210. This combination is
  rejected; score features must not become answerability evidence.

Full intermediate reports and question/source provenance are under
`eval/experiments/20260906/`. These runs select implementation changes and are
not new holdout evidence. No lexicon entries or gate thresholds were changed.

Additional primary references reviewed: [bounded parent retrieval](https://developers.llamaindex.ai/python/framework/integrations/retrievers/auto_merging_retriever/)
and [M3-Embedding](https://arxiv.org/abs/2402.03216). Parent context is applied
only within existing ownership and byte/count limits. Multi-vector/model
replacement has not been adopted; its KRX quality and operating cost remain
unmeasured. No local reranker service was found on the inspected endpoint.

## Measured cost and corrected failure diagnosis

A 10-iteration actual-service benchmark isolated unnecessary re-tokenization:
`expandedTermCoverage` tokenized every candidate even for nil/unit-only weights,
and repeated it for evidence and document selection. Skipping inapplicable work
and reusing active token maps within a request changes no scores. The ordinary
query measured 97.62 -> 84.48 ms and 16.28 -> 12.63 MB allocated per request; the
scoped-deadline comparison measured 752.56 -> 254.80 ms and 157.81 -> 53.10 MB.
These are benchmark averages on this host, not production p95 or retained RAM.
Full 210 quality remained 125/142 correct, 125/141 precision and zero unsafe
controls; search p95 measured 145.08 ms. The consumed 39 retained 17/28 correct,
17/25 precision, zero insufficient false acceptance, and two ambiguous false
acceptances; search p95 measured 293.06 ms. Final repeated pairs are pending.

The old candidate diagnostic required each whole gold evidence expectation to
fit inside a single chunk. New non-gating owner/bundle diagnostics show that all
28 answerable validation questions have their owners AND complete required text
somewhere in the bounded candidate pool, even though only 21 have a complete
single-chunk match. Increasing retrieval depth is therefore not justified by
these apparent misses. Remaining work is document/evidence ranking and selecting
sufficient, correctly scoped evidence. Candidate bundles are diagnostic oracles,
not successful answers; the selected-evidence gates are unchanged.

Global Korean verb normalization alone reproduced the unsafe control regression.
Restricting that normalization to ranking preserved safety but added no correct
answers (and reduced within-document first-evidence hits). Both normalization
variants and the original-heading bonus have been removed.

The document-max-only ablation removed the second/third chunk contribution to
document score. It improved the consumed 39 from 17 to 18 correct answers, but
reduced the 210 from 125 to 123, with Korean coverage regression and lower total
correct answers across both sets. It was rejected and the original aggregation
restored. No language-specific exceptions or adjusted weights were introduced.

The bounded parent-ranking variant increased the existing 210 from 125 to 126
correct selected answers (Korean 101 -> 102; English remains 22), retained the
39-case result of 17, and kept all insufficient controls refused. Removing both
parent ranking and parent context in an isolated diagnostic build reverted the
210 to 125 and left the 39 at 17. The parent variant is retained with 12 chunks
and 8,000 runes per document, only for the first five document candidates; it
does not increase retrieval depth or add embedding calls. Ordinary search p95
was 146.19 ms versus 144.75 ms without it; the 39-case p95 was 326.46 versus
291.71 ms. These runs are diagnostic, not repeated performance confidence bounds.

Large alternatives remain review items: cross-encoder semantic ranking would
use the existing optional adapter but needs a separately operated model service
and measured query latency; it also does not by itself prove causal/conditional
answerability. Typed condition/role extraction, citation-graph traversal, or
client-side semantic evidence verification would change the retrieval/answer
contract and require new labelled evaluation. No vector database, new embedding
model, additional model service, or sentence-specific ranking rule was adopted.

## Frozen checkpoint and bounded scope follow-up

The `9f48d952...` checkpoint was frozen before new labels. Its paired 38-case
run improved correct selected answers 5/27 -> 15/27 and precision 5/12 -> 15/19.
Korean improved 4/24 -> 14/24; English remained 1/3. Both release gates failed.
There were two unsupported-topic acceptances and two wrong selected answers.
The one automatic clarification success was not semantically sufficient: the
question already specified KONEX, but the reply asked for a market and cited
KOSDAQ/derivatives deadlines instead of the missing auction/after-hours facts.

This manual audit exposed two bounded scope defects, so the checkpoint is now
consumed validation for a follow-up: recognize bare Korean market names (without
confusing KOSPI 200 products with the cash market), and apply explicit market
scope before generic document-title hints. Generic titles cannot narrow the
first stage when their metadata does not establish the requested market.
The runtime policy is now `chunk-rrf-source-parent-scope-v3`.

A new 12-source reservation was written in `20260906-scope/reservation.json`
before these changes. It excludes targets from all 210 + 39 + 38 questions,
stratifies three available English counterparts by the same seeded ordering,
and keeps the same question protocol. No excerpts or labels for those new
sources have yet been read/authored. Do not report the consumed 38 as independent
proof of the scope follow-up. The original sealed fixture remains in
`20260906/original-sealed-fixture.json`, and its two one-shot reports are retained.

The temporary HTTP MCP server for the frozen checkpoint passed initialize,
bilingual retrieval, selected context ownership, default/1/5/50 refusal,
clarification and readiness checks; it was stopped. The embeddings container
remains unchanged; its actual cache revision is the pinned E5 revision.

The combined bare-market and early-metadata guard follow-up preserved 210/39,
but reduced the consumed 38 from 15 to 14 correct, increased assessed errors
from 4 to 6, and accepted one ambiguous case. Bare-market recognition changes
both filtering and answerability source/topic subtraction, while the early
metadata guard misinterprets a within-market session (after-hours) as another
market that must appear in the title. Those components were removed. The next
bounded comparison isolates only the ordering of explicit market filtering
before soft generic document-title hints; no new synonyms or gate relaxation.

The priority-only follow-up passed: 210 remains 126/141 assessed, consumed39
remains17/25, and consumed38 improves15/19 ->16/20 (same four assessed errors).
It recovers the emissions dispute-hearing question without the regressions of
the combined bare-market/early-metadata patch. Keep only this ordering fix.
A final freeze and fresh evaluation on the new reserved sources are pending.
