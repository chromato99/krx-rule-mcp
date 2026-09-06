# Retrieval and caller-answer quality contracts

The MCP server retrieves source material. The calling LLM judges applicability,
conditions, contradictions and whether to answer, search again or clarify.
`retrieval-v1` removes the server answerability classifier. This changes the
responsibility boundary, not the recorded outcome of earlier failed gates.
[Cycle 2](rag-improvement-cycle-2.md) remains historical.

## MCP retrieval evaluation

`rag-retrieval-evaluator-v3` produces report schema 2 through the public
`search_rules → get_context` path. Its main measures are:

- Document Hit@5: the labelled target policy is satisfied within the first five
  returned documents.
- Evidence bundle Hit@5: required text and reviewed contradiction phrases are
  present in addressable contexts from those first five documents. Fragments
  combine only within their document/article/attachment target, never across
  unrelated owners. `any`/`all`/`at_least` policies remain explicit.
- Context integrity: returned chunk IDs are nonempty, unique, retrievable without
  truncation, and consistent with source/document/article/attachment. A document
  candidate without chunks can be read with `get_rule`, but its ID alone does not
  count as substantive evidence.
- Filter isolation and the public version/status/count/result-limit contract.

Manual and document-only expectations do not count as substantive evidence
success. Within-document chunk depth and internal candidate recall are separate
diagnostics. `automatic_passed` describes retrieval checks only, not an answer.

The fixture labels `supported`/`insufficient`/`ambiguous` describe the expected
answer task. Related candidates for negative/ambiguous queries are candidate
availability, not false acceptance. Answer precision, coverage, refusal and
clarification accuracy require observing the calling model and are not emitted
by this evaluator.

## Retrieval regression gate

`--fail-on-gate` requires full-vector provenance, no filter leaks, valid returned
contracts and consistent contexts. Per-language Document Hit@5 and Evidence
bundle Hit@5 counts must not regress against the fixed baseline for the same
questions and targets. Corpus/index/vector artifact, lexicon, evaluator and
candidate budget must match. Returning nothing cannot pass this comparison.
MRR and internal candidate ranks remain diagnostics; source-group results allow
paired review of gains and losses hidden by an aggregate.

`eval/baselines/rag-retrieval-before.json` comes from captured public responses
before the caller-contract change. Old results were projected onto retrieval
metadata without adding/removing documents or passages and rescored with v3.
Old selected-answer metrics are not numerically compared with new retrieval
measures. Snapshot replay timing is not search latency.

The former `--gate-profile merge|release` and `--reranker-all` are removed.
The manual workflow checks retrieval regression; it does not approve or deploy
a model-backed product.

## Caller LLM evaluation

Fix host, model/version, prompt, tool budget, corpus release and retrieval
configuration. Record calls, inspected contexts, final answers and citations.
Assess claim correctness, supporting evidence, unsupported assertions,
appropriate clarification/refusal, coverage, latency and token cost.

Historical 95% precision/90% coverage objectives are not MCP protocol requirements
and are not silently relabelled as achieved. Product objectives must be defined
for the actual caller workflow before its independent test. See [the client
guide](llm-client.md).

## Data lifecycle

The 210 cases and all holdouts inspected in prior cycles are consumed development
or validation evidence. The 38-case fixture is now labelled development/validation; its exact
pre-change fixture is archived under `eval/experiments/20260906-caller-contract/`.
Historical lifecycle labels and failed reports remain reproducible; rerunning them does not certify generalization. This change alters
neither questions nor targets, lexicon entries, embedding model, nor the corpus
and index generation.

Reserve new canonical source groups before a final caller evaluation, keeping
English counterparts and attachments in the same group. Audit labels, freeze
implementation and prompt, and run the final test once. If its results guide
changes, record it as consumed. Do not add question-specific aliases or lower
failed objectives to make a run pass.
