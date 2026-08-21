# Evaluation fixture audit

## Scope

The 210-case `eval/golden/rag-v1.json` fixture was reviewed against corpus
release `cb9c80f4717b63afd9cde0eecea8064a1dab789d622f8158a110513bf9d4b7af`
and index generation
`dea7521bc7a29ebd6703282a9971a0127df60b54339116070816a90761ece12b`.
The audit covered all regression, development, and holdout cases rather than
only cases that failed retrieval.

## Checks

The executable grounding audit verifies for every target:

- document existence and ownership of the labelled attachment
- exact article or attachment anchor existence
- compatibility with language, document type, category, and effective-date
  filters
- `must_contain_all`, `must_contain_any`, and `must_not_contain` against the
  labelled article, attachment, or reviewed document body
- `relation_must_contain_any` for every contradiction target
- current corpus and immutable index identity before any retrieval query runs

Raw article text is also checked. This keeps a PDF extraction or index-heading
defect from being mistaken for a wrong legal label.

## Corrections

The review found the following fixture defects and weak expectations.

### ETF and NAV descriptions

- `fact-nav-threshold`, `fact-nav-threshold-variant`, and
  `audit-a-nav-threshold-reordered` incorrectly described 3% and 6% as an
  ETF/ETN distinction. Article 20-4 distinguishes domestic and overseas
  underlying assets. The queries now state that distinction.
- `multi-nav-markets-at-least-two` named KOSPI, KOSDAQ, and KONEX although its
  targets were the KOSPI ETF business/reporting rules and the Class X
  beneficiary-certificate rule. The query now names the actual products and
  duties.
- `alias-nav-variant` asked specifically for ETF evidence but accepted the
  Class X beneficiary-certificate rule. That target was removed.

### Market-making formulas

- `audit-b-score-minimum-formula`, `audit-c-score-formula-source`, and
  `audit-d-score-min-expression` omitted the multiplier from the source
  equation. They now ask for
  `min(10, 10 × product trading performance / evaluation criterion)`.
- Formula cases now require the actual equation variables and `10 TIMES`
  source, not only the parent attachment ID.
- Duty-rate, intraday-duty, margin-variable, and five-market-making-day cases
  now require their substantive terms. An attachment header alone no longer
  passes.

### Direct contradictions previously labelled unanswerable

The following queries were `insufficient` even though the regulation directly
provides a contrary rule:

- `hard-uti-passport-alternative`
- `hard-uti-resident-id`
- `audit-a-uti-phone-alternative`
- `audit-a-english-uti-passport`
- `audit-b-english-uti-license`
- `hard-margin-consent-exception`

They are now `supported` with `claim_relation: contradicts`. UTI cases require
the Article 18 phrase that a UTI shall be included for each reportable
transaction. The margin case requires the Article 139 phrase prohibiting uses
outside the enumerated purposes.

### Time and notice evidence

- Intraday clearing-margin cases now require both `14시 이내` and the qualifier
  that the deadline is the time determined by the general clearing member.
- Dual-listing and short-sale notices now require the relevant notice-body
  phrases. Document identity alone is no longer evidence success.

## Result

After correction, all 210 cases and all labelled document/article/attachment
targets pass the corpus-grounding audit. Fixture SHA-256 is:

`5026836077883452ba19c64882fd8908e7463ed383ba318d7c877bf8afd07764`.

The corrected E5 evaluation has lower retrieval metrics because it now counts
six legitimate contradiction questions as answerable and requires actual HWP
formula/notice evidence. That decrease is an evaluation correction and must not
be hidden by restoring the old labels.

One English indexing defect was exposed rather than relabelled:
`english-holdout-jcf-default-loss` correctly targets JCF Article 10, but the PDF
places `CHAPTER 3` immediately after the Article 10 heading and the current
structured chunker clears the article owner before the substantive paragraph.
The raw article audit confirms that the fixture is correct; the missing search
anchor remains a retrieval defect.

## Limits

The executable audit proves consistency with the collected derivative corpus,
not current legal authority or every possible interpretation. The manual review
checked query-to-target meaning, but compliance-sensitive answers must still
verify the current Korean official source. Any corpus refresh that changes a
target, formula, or effective date must rerun this audit and deliberately
refresh the fixture checksum and English non-regression baseline.
