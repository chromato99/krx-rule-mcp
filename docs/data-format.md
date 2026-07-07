# Data Format

Documents are stored as Markdown with YAML frontmatter.

```yaml
---
id: "210207961"
title: "코스닥시장 상장규정"
category: "업무규정 / 코스닥시장규정"
source_url: "https://rule.krx.co.kr/out/regulation/regulationViewPop.do"
effective_date: "2026-07-01"
published_date: "2026-05-13"
collected_at: "2026-06-16T13:00:00Z"
content_hash: "sha256..."
document_type: "rule"
language: "ko"
attachments:
  - id: "210203562-210032775-hwp"
    title: "[별표 1] 시가기준가종목의 최초의 가격을 결정하기 위한 최저호가가격 및 최고호가가격 산정기준"
    file_name: "유가증권시장 업무규정 시행세칙_172차_시가기준가종목의최초의가격을결정하기위한최저호가가격및최고호가가격산정기준.hwp"
    source_url: "/Download.do"
    raw_path: "ko/rules/코스닥시장-상장규정/raw/별표-1-시가기준가종목의-최초의-가격을-결정하기-위한-최저호가가격-및-최고호가가격-산정기준.hwp"
    text_path: "ko/rules/코스닥시장-상장규정/attachments/별표-1-시가기준가종목의-최초의-가격을-결정하기-위한-최저호가가격-및-최고호가가격-산정기준.md"
    content_hash: "sha256..."
    status: "converted"
    quality_status: "ok"
    quality_score: 100
    converted_text_chars: 18354
    table_row_count: 12
    formula_hint_count: 1
---
```

Required document fields:

- `id`
- `title`
- `source_url`
- `collected_at`
- `content_hash`
- `document_type`
- `language`: `ko` or `en`

The `id` field is the stable KRX document id used by MCP resource URIs and search metadata. Korean documents use the KRX id. English full-text documents use `{source_id}-en` and keep the Korean document id in `source_id`.

Language-aware corpus directories:

- `ko/rules/<title>/index.md`, `ko/notices/<title>/index.md`: Korean source corpus.
- `en/rules/<title>/index.md`: English full-text rule corpus when available.
- `<document>/raw`: downloaded original files for that rule or notice.
- `<document>/attachments`: converted Markdown attachments for that rule or notice.

Legacy `rules`, `notices`, and `attachments` directories are still read as Korean corpus for compatibility.

Attachment statuses are `pending`, `converted`, or `failed`.

Current-rule history attachments such as `전문(JUN)`, `개정이유`, `개정문`, and `신구조문` are intentionally skipped. They either duplicate the main rule body or describe past revisions. Direct `별표 및 서식` downloads are collected as normal attachments because they frequently carry tables, formulas, and templates needed for RAG answers. Future amendment notice attachments are kept with the notice document.

Attachment path fields are relative to the data root:

- `raw_path`: downloaded original file, when available. Raw paths point into the parent document bundle's `raw/` directory and preserve the original extension.
- `text_path`: converted Markdown text, only present for successfully converted attachments. Converted Markdown paths point into the parent document bundle's `attachments/` directory, so generated server ids do not leak into filenames.
- `content_hash`: hash of the original attachment bytes when downloaded
- `error`: failure reason for failed downloads or conversions

If conversion fails, the manifest keeps the original file path and failure reason but omits `text_path`.

## HWP Formula Blocks

`krx-rule-markdown` preserves HWP EqEdit formulas in converted attachment Markdown. When a converted attachment contains formulas, the text normally ends with a `## HWP 수식` section.

````markdown
수식 1 원본(HWP EqEdit):
```hwp-equation
{의무호가`제시시간`} over {의무발생시간} & GEQ 일중의무이행률
```

수식 1 LaTeX(best-effort):
```math
\begin{aligned}\frac{\text{의무호가 제시시간}}{\text{의무발생시간}} & \ge \text{일중의무이행률}\end{aligned}
```
````

MCP serving behavior:

- `get_attachment` returns the converted Markdown unchanged, including the formula notice, original `hwp-equation`, and generated `math` block.
- `get_attachment` also returns a structured `formula_notice` JSON field when the attachment contains HWP formulas.
- If search hits an attachment chunk, `search_rules` returns `attachment_matches[].chunk_id`; clients can pass it to `get_context` to read the exact formula neighborhood.
- `krx-rule://attachments/{id}` resources expose the same converted Markdown.
- The indexer chunks this content with the rest of the attachment text, so formula text can match both BM25 and vector search.
- The LaTeX block is best-effort. Clients should keep the adjacent HWP EqEdit source available when exact formulas matter.

`formula_notice` shape:

```json
{
  "severity": "info",
  "code": "hwp_formula_latex_best_effort",
  "message": "This result contains HWP EqEdit formulas. LaTeX math blocks are best-effort conversions; verify exact formulas against the adjacent hwp-equation source or original HWP attachment.",
  "source_equation_available": true,
  "generated_latex_available": true,
  "formula_count": 1
}
```

If only formula-like converted text is detected and no `hwp-equation`/`math` block is available, the server uses `code: "formula_text_detected"` with both availability flags set to `false`. That weaker notice means the text can help retrieval, but clients should verify precise formulas against the original attachment.

Converted attachment quality fields are optional but recommended:

- `quality_status`: `ok`, `warn`, or `fail`
- `quality_score`: simple 0-100 conversion quality score
- `quality_flags`: comma-separated warning flags such as `very_short_text`, `very_long_lines`, `replacement_characters`, `raw_table_hints_without_table_text`
- `converted_text_chars`, `converted_non_space_chars`: converted text size indicators
- `table_row_count`: table-like rows detected in the converted Markdown text
- `formula_hint_count`: formula-like expressions detected in converted text
- `replacement_char_count`: Unicode replacement characters found in converted text

`data/reports/data-quality.json` stores the full data-quality audit, including issue severity, document id, attachment id, filename, and message. It is produced by `krx-rule-markdown`; `krx-rule-mcp` only reads the corpus fields needed for indexing and serving.

Search snapshots are generated by `krx-rule-index` in `krx-rule-mcp` and should be stored outside the `krx-rule-markdown` corpus. The recommended local directory is `KRX_RULE_INDEX_DIR`. This repository also tracks a default root-level `index/` directory for the currently maintained corpus, so a source checkout can use `KRX_RULE_INDEX_DIR=./index` when the corpus hash matches.

- `bm25.krxidx`: `KRXIDX2\n` header plus gzip-compressed binary BM25 snapshot
- `vectors.krxvec`: optional `KRXVEC2\n` header plus gzip-compressed binary vector snapshot
- `vectors.krxvec.meta.json`: optional vector metadata sidecar

The `data/` directory remains the corpus handoff from `krx-rule-markdown`; search snapshots are MCP-serving artifacts. Snapshots include corpus hashes and are rejected or ignored when they no longer match the current Markdown/attachment corpus. The repository-provided `index/` is therefore a convenience snapshot, not a substitute for checking freshness after regenerating or replacing the corpus. Vector metadata also records embedding model, dimensions, query prefix, and document prefix so vector indexes are regenerated when E5 prefix settings change.
The default repository vector metadata uses `intfloat/multilingual-e5-small`, 384 dimensions, `query: `, and `passage: `. Alternative embedding models require regenerating `vectors.krxvec` and `vectors.krxvec.meta.json` with the new model, dimension, and prefix settings.
