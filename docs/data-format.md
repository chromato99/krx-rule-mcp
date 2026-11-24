# Data format

The indexer consumes a validated schema-v2 release from [krx-rule-markdown](https://github.com/chromato99/krx-rule-markdown). Keep the corpus and the generated index in separate directories.

## Corpus layout

| Path under `data/` | Contents |
| --- | --- |
| `manifest.json` | Strict release profile, document records, `release_hash` and `index_source_hash` |
| `ko/rules/<title>/index.md`, `ko/notices/<title>/index.md` | Korean rules and amendment notices with YAML frontmatter |
| `en/rules/<title>/index.md` | Available English full texts. Their ID is `{source_id}-en`, where `source_id` is the Korean rule ID. |
| `<document>/raw/`, `<document>/attachments/` | Preserved originals and converted attachment Markdown owned by the document |
| `reports/data-quality.json` | Producer audit with document/attachment IDs, severity and conversion issues |

The release includes annexes, forms and notice attachments. The producer's cleanup step drops past-revision attachments such as `개정이유`, `개정문` and `신구조문` from current rules. [Build a corpus and index](deployment.md#manual-index-jobs)

## What the loader verifies

- **Identity and content.** Manifest entries must match frontmatter. Document records carry `schema_version`, `id`, `title`, `document_type`, `language`, `source_url`, `collected_at`, `body_hash` and `searchable`. IDs must be unique and bundle paths must resolve within the expected directory. The loader rejects symlinks and cross-bundle references.
- **Hashes.** `body_hash` and `converted_text_hash` cover canonical UTF-8 text with LF line endings, NFC and outer whitespace trimmed. `raw_file_hash` covers original bytes. A converted attachment's `raw_path` and `text_path` are relative to the data root.
- **Conversion and quality.** An attachment records conversion (`pending`, `converted`, `failed`), preservation, quality and searchability separately. A failed conversion or failed quality check requires `searchable: false`. To include a failed attachment in a strict release, preserve the original, mark it `preservation_status: preserved` and list its ID in the manifest's `release_profile.allowed_failure_ids`. Public responses attach any `quality_notice` to the affected source.
- **Provenance.** When present, `source_content_path`, `source_content_hash` and `source_request_path` form a set. The loader validates the saved content and request descriptor. The public `official_source` exposes the source page, POST endpoint, permitted document identifiers and content hash. [Public source policy](legal-notice.md)

The [Go data types](../internal/model/model.go) list the fields, and the [producer/consumer test fixture](../internal/corpus/testdata/corpus_contract_v2.json) contains example records and their canonical hashes.

## HWP formula blocks

Converted HWP attachments can include paired `hwp-equation` (original EqEdit) and `math` (generated LaTeX) blocks. Search can match either. Use `get_context` on the attachment match, or read the attachment in pages with `get_attachment`. For an exact formula, compare the generated LaTeX with the adjacent original or the HWP file.

`formula_notice.code` is `hwp_formula_latex_best_effort` when the attachment text contains either block. `source_equation_available`, `generated_latex_available` and `formula_count` describe what was preserved. A positive `formula_hint_count` alone produces no notice.

## Index layout

`index/current` contains the lowercase SHA-256 ID of the active generation. Files below `index/generations/<id>/` belong to that generation.

| File | Purpose |
| --- | --- |
| `generation.json` | Descriptor v1 with corpus/index identity, sizes and SHA-256 digests |
| `bm25.krxidx` | Required BM25 snapshot (`KRXIDX2`, format v6), including chunk IDs, owning articles and heading paths |
| `vectors.krxvec` | Optional vector snapshot (`KRXVEC2`, format v3) |
| `vectors.krxvec.meta.json` | Required companion for vectors, format v4. Records model, revision, dimensions, prefixes, input format, full/sample scope and expected/stored chunk coverage. |

The indexer verifies the staged set before publishing it. To check a release, use the [index verification commands](deployment.md#manual-index-jobs).
