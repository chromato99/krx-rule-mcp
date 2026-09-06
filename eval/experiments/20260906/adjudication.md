# Source-reserved paired evaluation

The runtime source was frozen before the sampled source excerpts were read to
write labels. `freeze.json` records its file hashes and both executable hashes.
The fixture contains 38 questions: 24 Korean and three English supported cases,
eight insufficient controls and three ambiguity controls. Every reserved source
is covered; canonical targets are disjoint from both the 210 and consumed 39.

The sampler's seeded article selection was retained, including purpose and
cross-reference articles. Four supported questions test contrary evidence:
precedence of separate listing rules, use of an unlicensed refiner trademark,
reporting only one transaction party, and the first-day/calendar assumptions in
disclosure periods. Contract/report lists and qualified deadlines require all
requested fields. Answers and article numbers were not inserted into questions.

The ambiguity controls ask for an applicable decision while omitting necessary
facts: distinct acts versus violation types, the current quotation price band,
and pre-opening after-hours/auction conditions. Questions that explicitly ask
for the conditional branches are labelled supported separately. In addition to
status/count metrics, the actual clarification text needs qualitative review.

English appendix references include a line-broken Appendix 4 in the source.
The source and index grounding checks passed before retrieval. If selected
fragments fail the phrase contract, inspect presence and ordering during manual
review; do not relax the label after results.

Both frozen implementations will run once on the same questions. No new
holdout results may guide changes, aliases, thresholds or labels in this cycle.
This is a small internally authored pilot. Related rule families can overlap
although canonical target documents do not. It is not an external audit, and
three English supported cases cannot certify broad English quality.
