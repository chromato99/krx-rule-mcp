# Embedding profiles and retrieval evaluation

## Decision

The project accepts any OpenAI-compatible embedding model that can produce a
complete immutable generation. A generation uses exactly one embedding profile:
model, optional revision, dimensions, query/document prefixes, and input format.
Runtime settings must match that profile exactly. The release gate rejects
missing or partial coverage and incompatible provenance, but does not whitelist
a model name.

`intfloat/multilingual-e5-small` revision
`614241f622f53c4eeff9890bdc4f31cfecc418b3` remains the repository's default
and currently audited reference profile. Its defaults are not inherited by a
different model: non-default profiles must specify dimensions, and use empty
prefixes unless the operator explicitly configures model-appropriate values.

The prior Qwen experiment is closed as a replacement for the current default.
It was substantially heavier, did not produce a clear aggregate improvement,
and regressed Korean retrieval and refusal safety. This evidence does not ban
other compatible profiles. Cross-model score fusion remains out of scope: raw
similarity distributions are not comparable and each generation selects one
profile.
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
- A different UTI question asked whether a driver's-license number was also
  required. The UTI article is not an exhaustive list of reporting fields, so
  that question is now `insufficient` instead of inferring a prohibition from
  silence.
- Intraday member margin in Article 82 and intraday client margin in Article 88
  were conflated. Formal member/client queries now target the corresponding
  article, while two market-unspecified deadline questions are `ambiguous`.
- HWP and notice cases previously passed on attachment or document identity
  alone. They now require the actual formula or notice text.
- Both margin-variable cases accept Article 20's direct definition as well as
  the detailed HWP attachment.

Fixture SHA-256:
`00fd17323cb91e8f11a143fcccf7f13bc6db32daf2dd060d9f6d3ce3af4d9cb6`.

## Current E5 results

All values use 52,189/52,189 E5 vectors, `query: ` and `passage: ` prefixes,
and `text-v1` input.

| Metric | Korean overall | Korean holdout |
| --- | ---: | ---: |
| Cases | 180 | 35 |
| Document Hit@5 | 98.36% | 90.48% |
| MRR@5 | 0.894 | 0.794 |
| Evidence Hit@1 | 94.74% | 75.00% |
| Evidence Recall@3 | 97.37% | 85.00% |
| Candidate Recall@64 | 99.12% | 95.00% |
| Status accuracy | 98.33% | 97.14% |
| Insufficient refusal | 100.00% | 100.00% |
| False-supported | 0 | 0 |
| Context consistency | 100.00% | 100.00% |

The current implementation separates broad recall expansions from reviewed
evidence terms, uses compositional intent groups instead of evaluation-sentence
aliases, checks explicit identifiers across a multi-chunk evidence bundle, and
validates English calculation targets. On the 173 Korean cases whose status,
relation, and target meaning were unchanged, document Hit@5 increased from 108
to 115 cases, Evidence Hit@1 from 93 to 103, Evidence Recall@3 from 98 to 106,
and correct status from 165 to 172.

The current Korean diagnostic failures are one candidate-generation/document
ranking miss, two evidence-selection misses, three top-one ranking misses, and
three answerability misses. HWP evidence is 21/21, supported Korean
contradiction Evidence Hit@1 is 15/16, and both the `semantic` and
`semantic-variant` groups are 8/8 at rank one. Two of the answerability misses
are deliberately retained development cases where a deadline question omits
the applicable market; a simple category-count ambiguity rule was rejected
because it also rejected scoped ESG, gold, and emissions questions.

English is monitored as a model-independent quality floor rather than using the
Korean absolute merge thresholds:

| Metric | English overall | English holdout |
| --- | ---: | ---: |
| Cases | 28 | 11 |
| Document Hit@5 | 96.15% | 90.91% |
| MRR@5 | 0.872 | 0.818 |
| Evidence Hit@1 | 76.92% | 72.73% |
| Evidence Recall@3 | 96.15% | 90.91% |
| Candidate Recall@64 | 92.31% | 90.91% |
| Status accuracy | 100.00% | 100.00% |

The exact counts are pinned in
`eval/baselines/rag-v1-english-floor.json`. A release using any embedding
profile fails if an English count or MRR falls below this floor. The fixture SHA
must match, but the floor does not require the E5 model identity. Same-profile
before/after deltas are meaningful only when the complete embedding profile and
the remaining evaluation provenance match.

Latest audited local full report SHA-256:
`610e9f684bf29fb0be5648c4114d6bfd60b7f05c7f84232942ec12e65c9cc77d`.

The full search p95 is 237.13 ms, versus 222.92 ms in the previous report.
This 14.21 ms increase is recorded for capacity planning but is not a quality
gate.

The final BM25-only development diagnostic records Document Hit@5 85.53%, MRR
0.797, Evidence Hit@1 and Recall@3 87.67%, Candidate Recall@64 95.89%, status
accuracy 90.35%, insufficient refusal 100%, false-supported zero, and a
169.28 ms search p95. It confirms that the structural changes are usable
without one named embedding model, but full hybrid retrieval remains materially
better and is the release path being gated.

## Historical rejected Qwen experiment

`Qwen/Qwen3-Embedding-0.6B` revision
`97b0c614be4d77ee51c0cef4e5f07c00f9eb65b3` was previously evaluated on the
pre-audit fixture. It improved English Evidence Hit@1 from 76% to 84%, but
reduced Korean Document Hit@5 from 92.50% to 91.67%, Korean Evidence Recall@3
from 90.48% to 88.57%, and Korean refusal from 100% to 93.02%. It also produced
three false-supported cases.

The full Qwen generation took 30,686.31 seconds (8 hours 31 minutes 26 seconds)
on the local GTX 1650 Ti path, produced a 190,716,171-byte vector artifact, and
had a 304.33 ms search p95. The E5 vector artifact is 72,264,816 bytes; the
previous E5 report had a 222.92 ms search p95 and the current report has
237.13 ms. Speed remains diagnostic, not a quality gate.

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
and all HWP checks must pass. English must equal or exceed the model-independent
quality floor established by the audited reference result.

The current result does not pass this gate. Korean overall misses MRR by 0.006
(0.894) and has 17/19 ambiguous clarifications (89.47%). Korean holdout misses
Document Hit@5 (90.48%), MRR (0.794), Evidence Hit@1 (75%), and Evidence
Recall@3 (85%). Holdout candidate Recall@64 is exactly 95%, status accuracy is
97.14%, HWP is 5/5, semantic groups are 100%, and contradiction Hit@1 is above
90%. Thus the threshold set is somewhat coarse on the small holdout, but the
ranking and evidence gaps are too large to justify lowering it for this PR.

## Next stage: model-independent retrieval

The existing 46-case holdout has been consumed: it rejected an over-broad
deadline ambiguity rule, and the margin-variable alternative target was then
made consistent with the regression case. Do not tune against it again.

1. Promote the consumed cases to development/audit status and reserve a new
   document-disjoint holdout only after the next design is frozen. Keep source
   and fixture checksums so later reports cannot silently reinterpret either
   set.
2. Add hierarchical retrieval rather than merely increasing a fixed candidate
   depth. First retrieve document/section candidates from title, category, and
   article-heading fields; then retrieve chunks inside those candidates. Fuse
   independent BM25 and vector ranks by chunk ID. The earlier 120-to-256 depth
   experiment increased raw recall but reduced Evidence Recall@3 and introduced
   a false-supported result, demonstrating why pool depth and final evidence
   selection must remain separate.
3. Repair structural ownership before ranking. The known English JCF failure is
   caused by a PDF `CHAPTER 3` marker clearing the Article 10 owner. Preserve
   article/section ancestry in `krx-rule-markdown`, rebuild the immutable index,
   and validate chunk-to-source provenance before measuring retrieval.
4. Introduce parent-child evidence assembly. Retrieve compact paragraphs, then
   assemble the selected article and narrowly bounded neighbors so a heading,
   condition, exception, and answer value can be judged together. Candidate
   recall, document rank, and returned evidence order must remain separately
   observable.
5. Rank evidence with general legal structure: query-to-heading alignment,
   normative predicates, requested answer facets, and body-versus-attachment
   intent. Forms should not displace a directly responsive rule article merely
   because they repeat more query nouns; explicit annex/formula queries must
   still prefer their attachments.
6. Replace category-count ambiguity heuristics with competing-interpretation
   detection. Generate the best answer-bearing evidence per document scope,
   compare market/product metadata and extracted deadline/value spans, and ask
   for clarification only when credible scopes conflict. A product or rule name
   already grounded in the top document should suppress that clarification.
7. Mine reusable hard negatives from same-title articles, neighboring
   provisions, forms, and parallel market rules. Keep them phrase-independent
   and run BM25-only plus every candidate embedding profile. Compare `text-v1`
   with `structured-v1` across representative profiles; a view that helps only
   one model remains opt-in.
8. Retry an optional cross-encoder only after the new candidate stage is stable.
   It may improve Evidence Hit@1 but cannot repair a missing positive and must
   not become a baseline requirement.

After these steps, freeze all retrieval and answerability choices, create the
new holdout, and run it once. Do not lower the gate or add query-specific
exceptions to favor E5 or any other model.
