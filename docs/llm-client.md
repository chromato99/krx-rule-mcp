# 검색 결과로 규정 답변 작성하기

`search_rules`로 후보를 찾고 `get_context`로 조문을 읽습니다. 서버는 수집한 근거를 제공하고, 답변에 필요한 조건이 충족됐는지는 클라이언트의 모델이 판단합니다. 서버에서 답변 생성용 모델을 설정할 필요는 없습니다.

## 검색에서 인용까지

1. **범위를 정합니다.** 한국어 원문은 `language: "ko"`, 영문은 `"en"`으로 검색합니다. 규정과 개정예고는 `document_type`의 `rule`과 `notice`로 구분합니다. 시장·의무 주체는 질의에 넣고, 시행일 범위는 `effective_from`·`effective_to`에 `YYYY-MM-DD`로 지정합니다. 카테고리의 정확한 값은 `list_categories`에서 찾습니다.
2. **검색된 대목을 읽습니다.** `results[].evidence_matches[].chunk_id`를 `get_context`에 전달합니다. 첨부에 일치하면 `attachment_matches`에도 위치가 표시됩니다. 청크 없이 문서만 반환되면 `get_rule`로 본문을 엽니다.
3. **적용 조건을 확인합니다.** 같은 시장·주체·시행일의 근거인지 확인하고, 예외와 인용 조문은 이어 읽습니다. 예를 들어 추가 증거금의 납부 기한은 회원과 위탁자를 구분한 뒤 해당 조문에서 확인합니다. 여러 번 조회했다면 `release_generation`도 대조합니다.
4. **근거와 함께 답합니다.** 규정명·조문·`source_url`을 인용합니다. 시장이나 주체에 따라 결론이 달라지면 사용자에게 범위를 묻고, 원문을 찾지 못하면 검색한 범위와 확인할 수 없었던 조건을 밝힙니다. 영문 결과의 `source_id`로 한국어 규정을 찾을 수 있습니다.

## 응답 필드와 읽기 한도

| 응답 | 해석과 다음 호출 |
| --- | --- |
| `contract_version: "retrieval-v1"` | `retrieval.status`는 `candidates_found` 또는 `no_candidates`입니다. `retrieval.returned_results`는 이번 질의·필터에서 반환한 문서 수입니다. 0건이면 질의나 필터를 조정합니다. |
| `score`, `query_expansion`, `context_only` | 각각 순위 점수, 적용한 사전 용어, 같은 조문·문단에서 보충한 문맥입니다. 질문의 전제와 답변 근거는 실제 문장에서 확인합니다. |
| `get_context`의 `document`, `chunks`, `content` | 같은 본문 또는 같은 첨부 안에서 기본 앞뒤 1개 청크씩 가져옵니다. `before_chunks`·`after_chunks`를 0으로 주면 해당 방향을 제외합니다. |
| `truncated`, `total_chars`, `next_offset` | 본문·첨부·문맥은 기본 20,000자, 최대 50,000자입니다. 본문·첨부는 다음 호출에 `offset=next_offset`을 전달합니다. 문맥이 잘리면 `get_rule` 또는 `get_attachment`로 이어 읽습니다. |
| `list_rules`, `list_recent_changes` | 목록 크기는 `limit`으로 지정하고 `next_offset`을 다음 요청의 `offset`에 넣습니다. 기본 반환 수는 각각 50건과 20건이며 최대 200건입니다. |
| `krx-rule://rules/{id}`, `krx-rule://notices/{id}`, `krx-rule://attachments/{id}` | 리소스는 원문을 최대 50,000자까지 표시합니다. `contents[0]._meta.truncated`가 참이면 해당 도구로 나머지를 읽습니다. |
| `quality_notice`, `formula_notice` | 변환 경고는 표시된 출처에 적용됩니다. 산식의 기호와 부등호는 자동 생성 LaTeX와 인접한 `hwp-equation` 또는 원본 HWP를 대조합니다. [데이터 형식](data-format.md#hwp-formula-blocks) |

예를 들어 기본 인덱스에서 `의무호가 제시시간 의무발생시간 일중의무이행률`을 검색하면 「주식·주가지수 파생상품 시장조성 운영지침」의 첨부가 나옵니다. 아래는 그 첨부에서 일치한 청크의 앞뒤 문맥을 6,000자까지 읽는 `get_context` 입력입니다. 다른 인덱스에서는 해당 검색이 반환한 ID를 사용하세요.

```json
{"chunk_id": "210202173#att-210202173-210219676-hwp-9", "before_chunks": 1, "after_chunks": 1, "max_chars": 6000}
```

## 호스트 연동

- MCP 초기화 지침과 도구 설명을 모델에 전달합니다. 검색으로 읽은 문서는 답변의 출처로 취급합니다.
- 같은 도구 결과가 `structuredContent`와 JSON 텍스트에 들어갑니다. 모델 문맥에는 둘 중 하나만 전달합니다. [MCP 구조화 결과 명세](https://modelcontextprotocol.io/specification/2025-11-25/server/tools#structured-content)
- 잘못된 입력이나 필수 검색 서비스의 실패는 도구 오류로 처리합니다. 출력 크기 제한에 걸리면 `limit` 또는 `max_chars`를 줄여 재시도합니다.
- 실제 답변과 인용 품질은 사용하는 모델·프롬프트·도구 예산을 고정해 평가합니다. [호출 모델 평가](rag-quality-contract.md#caller-llm-evaluation)
