# Embedding model and retrieval evaluation

## Decision

Use only `intfloat/multilingual-e5-small` revision
`614241f622f53c4eeff9890bdc4f31cfecc418b3` for document and query
embeddings. The release gate rejects every other model, revision, dimension,
prefix, input format, partial vector scope, or incomplete coverage.

The prior Qwen experiment is closed. It was substantially heavier, did not
produce a clear aggregate improvement, regressed Korean retrieval and refusal
safety, and is not a candidate for a second vector channel or rank fusion.
No application, evaluator, fixture, gate, or release-schema version was raised
for this unmerged PR.

## Audited fixture

The fixture remains `rag-v1` and contains 210 cases: regression 50,
development 114, and holdout 46. It has 180 Korean, 28 English, and two
language-unspecified cross-language cases. Korean holdout has 35 cases and
English holdout has 11.

Every target is now checked against the exact corpus and immutable index before
retrieval evaluation starts. The audit verifies document, article, attachment,
input filter, required/forbidden text, and contradiction-polarity phrases.
The review found and corrected several label defects:

- ETF 3% and 6% thresholds were incorrectly described as an ETF/ETN
  distinction; the regulation distinguishes domestic and overseas underlying
  assets.
- A comparison query named KOSPI, KOSDAQ, and KONEX even though its targets
  were KOSPI ETF reporting and Class X beneficiary-certificate rules.
- Three market-making score queries omitted the multiplier in
  `min(10, 10 × trading performance / evaluation criterion)`.
- UTI replacement identifiers and customer-consent margin diversion were
  labelled `insufficient`, although the regulations directly require UTI and
  prohibit non-enumerated uses of customer margin. They are now supported
  contradiction cases.
- HWP and notice cases previously passed on attachment or document identity
  alone. They now require the actual formula or notice text.

Fixture SHA-256:
`5026836077883452ba19c64882fd8908e7463ed383ba318d7c877bf8afd07764`.

## Current E5 results

All values use 52,189/52,189 E5 vectors, `query: ` and `passage: ` prefixes,
and `text-v1` input.

| Metric | Korean overall | Korean holdout |
| --- | ---: | ---: |
| Cases | 180 | 35 |
| Document Hit@5 | 89.52% | 85.71% |
| MRR@5 | 0.810 | 0.746 |
| Evidence Hit@1 | 77.59% | 60.00% |
| Evidence Recall@3 | 85.34% | 75.00% |
| Candidate Recall@64 | 93.97% | 90.00% |
| Status accuracy | 93.89% | 94.29% |
| Insufficient refusal | 100.00% | 100.00% |
| False-supported | 0 | 0 |
| Context consistency | 100.00% | 100.00% |

The lower numbers compared with the earlier report are primarily an evaluation
correction, not a search-code regression: six false-negative queries are now
properly treated as answerable contradictions, document-only notices require
their actual body evidence, and HWP targets require the requested formula.

The 116 Korean evidence cases expose all three retrieval stages:

- 7 targets are absent from fused candidate rank 64, including the margin
  variable attachment, retrospective listing/margin paraphrases, and dispute
  record retention.
- 11 cases have a qualifying first-stage candidate but no matching final
  evidence in the top three contexts. Most are contradiction, notice, or HWP
  formula cases rejected by evidence selection or answerability.
- 9 cases have the right evidence at rank two or three rather than rank one.

Korean status errors are 11 false negatives (`supported` observed as
`insufficient`); false-supported remains zero. This is why candidate generation,
final evidence ranking, and the conservative answerability gate must be measured
separately.

English is monitored for non-regression rather than used as an absolute merge
criterion:

| Metric | English overall | English holdout |
| --- | ---: | ---: |
| Cases | 28 | 11 |
| Document Hit@5 | 88.89% | 90.91% |
| MRR@5 | 0.827 | 0.818 |
| Evidence Hit@1 | 70.37% | 72.73% |
| Evidence Recall@3 | 88.89% | 90.91% |
| Candidate Recall@64 | 92.59% | 90.91% |
| Status accuracy | 92.86% | 100.00% |

The exact counts are pinned in
`eval/baselines/rag-v1-e5-english.json`. A release fails if any English count
or MRR regresses, or if the fixture SHA or E5 embedding contract differs.

Audited E5 report SHA-256:
`f7b84af82895ea0a152d1d026cdc0a51672a5f3756961bdac4f24073deb7e60c`.

## Historical rejected Qwen experiment

`Qwen/Qwen3-Embedding-0.6B` revision
`97b0c614be4d77ee51c0cef4e5f07c00f9eb65b3` was previously evaluated on the
pre-audit fixture. It improved English Evidence Hit@1 from 76% to 84%, but
reduced Korean Document Hit@5 from 92.50% to 91.67%, Korean Evidence Recall@3
from 90.48% to 88.57%, and Korean refusal from 100% to 93.02%. It also produced
three false-supported cases.

The full Qwen generation took 30,686.31 seconds (8 hours 31 minutes 26 seconds)
on the local GTX 1650 Ti path, produced a 190,716,171-byte vector artifact, and
had a 304.33 ms search p95. The E5 vector artifact is 72,264,816 bytes and the
audited E5 run had a search p95 around 191-193 ms. Speed remains diagnostic,
not a quality gate.

These Qwen values are historical rejection evidence only. Qwen is not rerun on
the corrected fixture and must not be used as a current baseline.

## Korean-primary release gate

Korean overall and Korean holdout independently require:

- Document Hit@5 at least 95%
- MRR@5 at least 0.90
- Evidence Hit@1 at least 90%
- Evidence Recall@3 at least 95%
- Candidate Recall@64 at least 95%
- insufficient refusal at least 95%
- ambiguous clarification at least 90%
- filter leaks zero and context consistency 100%

Korean `semantic`, `semantic-variant`, and supported contradiction cases
additionally require Evidence Hit@1 of at least 90%. Global protocol invariants
and all HWP checks must pass. English has no independent absolute threshold; it
must equal or exceed the pinned audited E5 baseline.

## Next stage: E5 only

Do not change aliases, weights, or answerability rules in response to sealed
holdout failures.

1. Keep the corrected 46 holdout cases sealed. Use regression and development
   data only for model or retrieval design.
2. Separate candidate misses from final-ranking misses. For each Korean
   development query, retain the labelled positive chunk and review the closest
   wrong BM25 and E5 chunks, especially same-document wrong articles, adjacent
   articles, similar market rules, notices, and formula attachments.
3. Correct general evidence semantics using development cases only. Direct
   mandatory or exhaustive norms such as "include UTI" and "uses other than
   these are prohibited" must be able to refute a proposed replacement without
   requiring the replacement word itself in the article. Keep the existing
   unknown-term and composite-claim checks so mixed in-domain/OOD questions
   still fail closed. Normalize formula operators and spacing before HWP
   evidence matching rather than adding formula-specific query aliases.
4. Mine hard negatives using the maintained E5 model and BM25 only. Sentence
   Transformers documents
   [hard-negative mining](https://www.sbert.net/docs/package_reference/util/hard_negatives.html)
   and optional cross-encoder rescoring for this purpose.
5. Compare an E5-only multi-view candidate index: retain the canonical
   `text-v1` vector and add a second E5 representation containing the document
   title, article heading/path, and canonical text. Fuse ranks by chunk ID; do
   not mix raw cosine scores. This tests whether structure fixes candidate
   recall without training a new model.
6. If Korean Candidate Recall@64 remains below 95%, fine-tune the same E5 model
   with reviewed Korean query-positive-hard-negative examples. Include the
   English evaluation pairs as replay data and reject the model if the pinned
   English baseline regresses. The [E5 paper](https://arxiv.org/abs/2212.03533)
   reports strong fine-tuned retrieval performance from contrastive training,
   and Sentence Transformers provides
   [task-specific losses](https://www.sbert.net/docs/package_reference/sentence_transformer/losses.html).
7. Only after candidate recall passes should a Korean cross-encoder be retried
   for Evidence Hit@1. A reranker cannot repair a positive chunk absent from
   the candidate pool and remains disabled unless it improves sealed Korean
   holdout without an English or safety regression.

If these E5-only stages still miss the Korean gate, the next proposal must be
based on the remaining error stage and fresh document-disjoint holdout evidence;
it must not reintroduce Qwen or loosen the gate.
