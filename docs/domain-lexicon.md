# Domain lexicon

The lexicon connects terms used in questions with terms used in KRX documents. For example, `동적상하한가` can retrieve `실시간가격제한제도`, `가격변동폭` and `별표25`. When an entry matches, `search_rules` returns the original query, expanded query, matched terms and sources in `query_expansion`. Cite the regulation or attachment found through that search.

The server loads the [default YAML file](../config/domain-lexicon.yaml) at startup. Use `--domain-lexicon` or `KRX_DOMAIN_LEXICON_PATH` for another file. Invalid entries fail startup.

## Add an entry

Use rule titles, headings and annex names from the KRX corpus first, then official market guides and English terminology. Record a source URL for a colloquial alias and mark the mapping `curated`. The configuration lists the sources for each entry. [KRX Legal Portal](https://rule.krx.co.kr/out/index.do) · [Market stabilization guide](https://regulation.krx.co.kr/contents/RGL/03/03050600/RGL03050600.jsp) · [ETF glossary](https://open.krx.co.kr/contents/OPN/01/01030500/OPN01030500.jsp)

```yaml
entries:
  - id: derivatives_realtime_price_limit
    canonical: 실시간가격제한제도
    aliases: [동적상하한가, dynamic price limit]
    match_groups:
      - [파생상품, 선물, derivatives]
      - [실시간, real-time]
      - [상하한가, 가격제한, price limit]
    expansions: [가격변동폭, 별표25]
    evidence_terms: [실시간 가격제한, real-time price limit]
    source_urls:
      - https://regulation.krx.co.kr/contents/RGL/03/03050600/RGL03050600.jsp
    confidence: high
    review_status: curated
```

| Field | How it is used |
| --- | --- |
| `id`, `canonical` | Required unique entry ID and official term |
| `aliases`, `match_groups` | Any direct alias can activate the entry. For a group match, at least one alternative from **every** group must match. Empty groups are invalid. |
| `expansions` | Broader terms for finding candidates, including official names and annexes |
| `evidence_terms` | Reviewed passage-level phrases used with the canonical term to rank evidence |
| `source_urls`, `confidence`, `review_status`, `note` | Record the basis and review of the terminology mapping. |

Keep entries reusable. Put member margin and client margin in separate entries when the obligation differs. Leave evaluation questions and expected numeric answers out of the dictionary.
