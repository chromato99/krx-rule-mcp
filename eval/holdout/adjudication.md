> Status: consumed validation from the 2026-09-06 improvement cycle. The exact original sealed fixture is archived as `fixture-20260905.json`; its one-shot result remains historical evidence.

# Reserved-source holdout

The implementation was frozen in `freeze.json` before the selected source
articles were read to write questions. `reservation.json` records the source
selection seed and question protocol. Main-body articles were ordered by
SHA-256 of seed + document ID + article ID; two Korean articles per reserved
source and one article per available English counterpart were selected.

The fixture has 39 new questions: 24 Korean and four English supported cases,
eight out-of-domain combinations, and three ambiguity controls. The intake
questions are preserved separately in `eval/source/rag-holdout-2026-09-05.json`.
Questions and targets were checked against the corpus without running retrieval.

The supported questions ask about the sampled provisions, including scope,
procedure, qualified deadlines, reporting fields, accounting conflicts, gold
price formulas and client margin multipliers. They do not insert the expected
numerical answers into the query. Three questions require contrary evidence:
unchanged listing attachments need not be resubmitted, the stated small-merger
exception is excluded, and the conflicted auditor's report is excluded.

The ambiguity controls explicitly request one unconditional answer despite
missing a necessary distinction:

- OTC currency valuation: the Exchange versus the general clearing member,
  depending on whose margin is being valued (Article 66).
- New-type securities reports: annual versus half-year reporting and their
  different starting events/deadlines (Article 21).
- New-type securities trading-method changes: different triggers and closing
  quotation periods carry different percentages (Article 24).

The emissions corpus contains a new Article 42 describing trade-notification
fields followed by an older deletion marker. The target is the substantive
2025 notification provision; all five reporting fields are required, so the
deletion marker alone cannot pass.

This is a small, internally authored evaluation. Canonical target documents are
disjoint from the 210 development/regression cases, but related rule families
can overlap and all documents remain in the searchable corpus. It is not an
independent external audit or a statistical guarantee. The report must retain
failures; implementation changes based on this run would consume the holdout.
