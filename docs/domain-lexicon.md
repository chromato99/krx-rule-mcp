# Domain Lexicon

`search_rules` applies a KRX domain lexicon before BM25/vector search. The lexicon is intentionally conservative: it expands user wording into official or corpus-derived KRX terms, but it does not replace the original query.

The default file is `config/domain-lexicon.yaml`. The server loads it on startup. Override the path with `--domain-lexicon` or `KRX_DOMAIN_LEXICON_PATH`.

The response includes `query_expansion` when any entry was applied, so RAG clients can see which official terms and source URLs affected recall.

## Source Priority

Entries should be added in this order of trust:

1. KRX Legal Portal corpus: rule titles, article headings, attachment titles, annex names.
2. KRX official regulation/market guide pages.
3. KRX official English pages for bilingual aliases.
4. Curated user aliases, only when they are mapped to official KRX terms and marked as `curated`.

Avoid broad blog/news terminology unless a maintainer manually reviews it and records the official KRX term it maps to.

## Current Sources

- KRX Legal Portal: <https://rule.krx.co.kr/out/index.do>
- KRX market stabilization guide for derivatives, including `가격제한폭`, `실시간가격제한제도`, and `실시간 상·하한가`: <https://regulation.krx.co.kr/contents/RGL/03/03050600/RGL03050600.jsp>
- KRX price-limit guide: <https://regulation.krx.co.kr/contents/RGL/03/03020201/RGL03020201.jsp>
- KRX KOSPI 200 Futures guide: <https://open.krx.co.kr/contents/OPN/01/01040201/OPN01040201.jsp>
- KRX ETF glossary: <https://open.krx.co.kr/contents/OPN/01/01030500/OPN01030500.jsp>
- KRX ETF glossary list endpoint: <https://open.krx.co.kr/contents/OPN/01/01030500/OPN01030500T1.jsp>
- KRX Global KOSPI 200 Futures page, including `Daily Price Limit`: <https://global.krx.co.kr/contents/GLB/02/0201/0201040201/GLB0201040201.jsp>

## YAML Format

```yaml
entries:
  - id: derivatives_realtime_price_limit
    canonical: 실시간가격제한제도
    aliases:
      - 동적상하한가
      - dynamic price limit
    match_groups:
      - [파생상품, 선물, futures, derivatives]
      - [실시간, realtime, real-time]
      - [상하한가, 가격제한, price limit]
    expansions:
      - 실시간 가격제한의 가격변동폭
      - 가격변동폭
      - 별표25
    evidence_terms:
      - 실시간 가격제한
      - 가격변동폭
      - real-time price limit
    source_urls:
      - https://regulation.krx.co.kr/contents/RGL/03/03050600/RGL03050600.jsp
    confidence: high
    review_status: curated
    note: Optional reviewer note.
```

Required fields are `id` and `canonical`. The loader trims duplicate aliases,
match-group alternatives, expansions, evidence terms, and source URLs, and
rejects duplicate `id` values or empty match groups.

The fields have deliberately different jobs:

- `aliases` is an OR-list. A direct alias match may activate the entry.
- `match_groups` is compositional: every group must match, while alternatives
  inside one group are OR-ed. This represents an intent such as
  `(derivatives) AND (real-time) AND (price limit)` without copying complete
  evaluation questions into the lexicon.
- `expansions` improves candidate and document recall. It may contain broader
  official names, document names, or related terms.
- `evidence_terms` contains reviewed phrases used with `canonical` to rank
  candidate passages. These terms and broader recall expansions do not prove
  that a question is answerable; the caller must inspect the source text.

Do not add full evaluation sentences or expected numeric answers as aliases or
expansions. Prefer reusable concept groups, and keep answer values in the
corpus. When one colloquial term can mean different legal concepts, define
separate entries. For example, intraday member margin (Article 82) and
intraday client margin (Article 88) are not interchangeable.

## Example

A user query such as `동적상하한가` is not a term found in the collected rule corpus. The lexicon treats it as a curated alias for the official derivatives real-time price limit terminology:

```json
{
  "query_expansion": {
    "original_query": "동적상하한가",
    "expanded_query": "동적상하한가 실시간가격제한제도 실시간 가격제한 실시간 상한가 실시간 하한가 실시간 가격제한의 가격변동폭 가격변동폭 가격제한폭 파생상품시장 업무규정 시행세칙 별표25 ...",
    "applied_terms": [
      {
        "id": "derivatives_realtime_price_limit",
        "canonical": "실시간가격제한제도",
        "matched_terms": ["동적상하한가"],
        "confidence": "high",
        "review_status": "curated",
        "source_urls": [
          "https://regulation.krx.co.kr/contents/RGL/03/03050600/RGL03050600.jsp",
          "https://rule.krx.co.kr/out/index.do"
        ]
      }
    ]
  }
}
```

RAG clients should still cite the returned rule/attachment context, not the
lexicon entry itself. The lexicon improves recall and helps select evidence; it
is not legal authority and never supplies an answer value by itself.
