# Retrieval and caller-answer quality

The MCP server retrieves source material. The calling LLM judges applicability,
conditions, contradictions and whether to answer, search again or clarify.
Retrieval checks do not certify the calling model's final answers.

## Maintained evaluation inputs

- `eval/fixtures/retrieval.json`: current schema-v2 regression questions and
  document/article/attachment targets. The source metadata records attribution;
  the fixture is self-contained and requires no historical intake file.
- `eval/baselines/retrieval.json`: fixed language-specific retrieval floors and
  corpus/index/embedding/lexicon/candidate-budget identities.
- `eval/schema/retrieval.schema.json`: the current fixture schema. Deprecated
  server-answerability controls are not part of this format.

The 210 regression/development/validation cases are maintained checks, not
independent evidence of generalization. Retain question, filter and target
contracts when comparing implementations. Removing obsolete fixture fields
changes its contract hash, but does not justify changing retrieval floors.

## Retrieval measures

`rag-retrieval-evaluator-v4` uses the public `search_rules → get_context` path.

- Document Hit@5 checks the labelled document target policy within the first
  five returned documents.
- Evidence bundle Hit@5 checks required text and reviewed contradiction phrases
  in contexts from those first five documents. Fragments combine only within
  their document/article/attachment target. `any`/`all`/`at_least` remain explicit.
- Every returned chunk ID must be nonempty, unique and retrievable without
  truncation, with matching source/document/article/attachment ownership.
- The version/status/count/result-limit contract and explicit filters must hold.

A document candidate without chunks can be inspected with `get_rule`; its ID
alone is not substantive evidence. Manual/document-only expectations do not
count as substantive evidence successes. Within-document depth and internal
candidate recall remain diagnostics. A target in the internal pool is not a
candidate already available to the LLM.

The fixture's `supported`/`insufficient`/`ambiguous` labels describe the answer
task. Related candidates for negative/ambiguous queries are not false acceptance.
`automatic_passed` describes retrieval checks only.

## Regression gate and workflow

`--fail-on-gate` requires full-vector integrity, no filter leaks, valid returned
contracts and consistent contexts. Document/evidence Hit@5 counts per language
must not regress against the same question and target contract. Artifact and
configuration identities must match. Returning nothing cannot pass.

`--split` and `--case-prefix` are diagnostic filters and cannot be used with the
gate. `--fixture` accepts another current-schema fixture. For a sealed holdout,
`--holdout-reservation` must name an external reservation file containing
`schema_version: 1` and `canonical_source_ids`; every reserved source must be
covered and every target must belong to a reserved source.

The manual `retrieval-eval.yml` workflow checks retrieval regression using a
pinned corpus commit. It does not deploy or approve a model-backed product.
Generated reports belong in ignored `eval/results/`. Experiment histories,
publication notes, sampling scripts and archived run output are not maintained
project files.

## Caller LLM evaluation

Fix the host, model/version, prompt, tool budget, corpus release and retrieval
configuration. Record tool calls, inspected contexts, final answers and citations.
Assess claim correctness, evidence, unsupported assertions, clarification/refusal,
coverage, latency and token cost. See [the client guide](llm-client.md).

Before final evaluation, reserve canonical source groups with English variants
and attachments in the same group. Freeze implementation and prompt before
inspecting results. A set used to select changes becomes validation data.
Do not add question-specific aliases or lower failed objectives to make a run
pass. Product answer targets must be assessed in the actual caller workflow.
