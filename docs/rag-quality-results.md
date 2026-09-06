> Historical first-cycle report. The current work and new reserved-source evaluation are recorded in [cycle 2](rag-improvement-cycle-2.md). The 39 questions below are now consumed validation.

# Selected-evidence implementation and evaluation

The changes are based on PR #7 commit `3d6fe64` and remain a local working-tree
change. The implementation was frozen before new holdout labels were written;
`eval/holdout/freeze.json` records the complete source-file list and digest.

The code-comparison gate passes. The new-question release gate fails.
These are different outcomes: correcting the known defects and preserving the
existing question set does not establish production answer quality.

## Implemented changes

- Evaluate the actually selected evidence IDs, including same-target fragments,
  owner consistency, truncation, unsafe ambiguity acceptance, and unassessed
  answers. Preserve question/target hashes when relabelling consumed splits.
- Fix the default candidate budget at 120 per channel independently of result
  limit; record the effective value in runtime/evaluation identity.
- Require complete numerical boundaries and calculation objects, distinguish
  requested time values from assertions, and keep rule-name agreement separate
  from substantive topic evidence.
- Respect explicit market and named-rule scope without confusing a bare market
  with a particular business/listing rule. Comparisons preserve both scopes.
- For unscoped Korean deadline questions, inspect bounded per-category
  alternatives when global candidates omit another market. Only real, topical
  conflicting deadlines cause clarification; those alternatives are returned
  for context inspection and never borrowed to support the primary answer.
- Restore geometric PDF reading order, invalidate only the PDF conversion cache,
  and stop the English chunker from interpreting `parties` as `PART`.
- Rebuild an immutable full-vector generation, reusing vectors only for exact
  unchanged text under the same verified revision-pinned text-v1 profile.

## Fixed-question comparison

The same 210 question/target contracts were used before and after retrieval
changes. The former 46-case holdout is now explicitly `validation`.

| Metric | Korean before | Korean after | English before | English after |
|---|---:|---:|---:|---:|
| Correct selected answers | 101/114 | 101/114 | 21/26 | 22/26 |
| Selected evidence precision | 87.83% | 89.38% | 80.77% | 84.62% |
| Correct answer coverage | 88.60% | 88.60% | 80.77% | 84.62% |
| Unsafe insufficient acceptance | 0 | 0 | 0 | 0 |
| Unsafe ambiguous acceptance | 2 | 0 | 0 | 0 |

All context checks pass and filter leaks remain zero. Eight document-only
answers remain explicitly unassessed. Sixteen assessed supported answers do
not satisfy the fixture's selected-target expectations; the merge gate does
not hide these disagreements or claim they meet release precision.

The final full-vector search p95 was 338.79 ms; latency is diagnostic. Scope
probes add work only for potentially ambiguous Korean deadline questions.

## Contribution checks

Official-terms-only and no-lexicon experiments substantially reduced correct
selected evidence on the development/validation set. The reviewed lexicon was
retained; this proves contribution on these questions, not generalization.

Removing normative counter-evidence processing from the final candidate reduced
correct Korean answers from 101 to 97 of 114 and did not improve selected
precision. That variant was rejected. BM25-only produced 98/114 correct Korean
answers and 20/26 English answers; the full-vector profile remains the evaluated
release path. No cross-encoder or structured-v1 generation was adopted.

## New reserved-source evaluation

Twelve canonical source groups were reserved before retrieval changes. After
implementation freeze, deterministic source-article sampling and manual label
review produced 39 questions: 28 supported, eight insufficient, three ambiguous.
All targets passed the source/index grounding audit before retrieval ran.

| Metric | Korean (34 cases) | English (5 cases) |
|---|---:|---:|
| Document Hit@5 | 58.33% | 100% |
| Correct selected answers | 8/24 | 2/4 |
| Selected evidence precision | 50.00% | 40.00% |
| Correct answer coverage | 33.33% | 50.00% |
| Unsafe insufficient acceptance | 1 | 1 |
| Unsafe ambiguous acceptance | 1 | 0 |

The release gate failed. Overall, 11 of 21 assessed answers did not satisfy the
expected safe-answer contract. There are nine candidate-generation failure
labels, nine evidence-selection labels, eight selected-evidence labels and
14 answerability labels; labels overlap and must not be summed as independent
cases. Context consistency is 100% and filter leaks are zero.

This result was run once. No implementation, alias, weight, threshold, question
or target was changed after observing it. The sample is small and internally
authored; related rule families can overlap despite disjoint canonical target
documents. It is not a statistical certificate. It nevertheless shows that the
new release is not ready for autonomous answering across unfamiliar questions.

The next development cycle should use these failures as validation evidence,
separate candidate misses from missing same-article context/conditions and
unsupported topic/role inferences, and reserve a new final test before tuning.
The source-group report should guide whether parent-child assembly or broader
retrieval is warranted; do not add aliases for the exact failed sentences.

## Validation and artifact identity

- `go test ./...`, `go test -race ./...`, `go vet ./...`: passed.
- Actual corpus, full-vector retrieval and fixture grounding integration tests:
  passed.
- Producer tests with HWP/PDF dependencies: 126 passed.
- Full-vector immutable artifact check: passed.
- Actual local HTTP MCP client: initialize, bilingual retrieval, selected
  get_context owner/window checks, default/1/5/50 refusal, and deadline
  clarification passed; `/readyz` passed. The temporary server was stopped.
- Source PDF/HWP files were not modified. Corpus reconversion's initial attempt
  failed closed because its Python environment lacked pyhwp; the active corpus
  was preserved. Re-running with the complete conversion environment passed.
- No production deployment or GitHub workflow execution was performed.

Corpus release:
`469cda6799dfa33d7f8e88e175a6f745f81851a4f76d99e4529e9a4f7a7f5c84`.

Index generation:
`70e23cf74164d137ba35316e875edac3350ed7ff0f944c04a9e6fd443034e78f`.

There are 51,929 chunks/vectors: 51,184 exact text vectors reused and 745 newly
embedded. The profile remains multilingual-e5-small, revision
`614241f622f53c4eeff9890bdc4f31cfecc418b3`, 384 dimensions, `query: ` / `passage: ` prefixes
and text-v1. The previous immutable generation remains available.

Compact holdout results are in `eval/holdout/result.json`; full local reports
are under `eval/results/issue6-selected-20260905/`. These historical selected-answer reports use an earlier evaluator. The current
`eval/compare.py` compares retrieval-v3 reports only. Raw historical runs are
preserved in [the evaluation archive](../eval/archives/README.md).
