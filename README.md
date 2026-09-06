# KRX Rule MCP

한국거래소 법무포털 규정 corpus를 AI 클라이언트가 빠르게 검색하고 참조할 수 있게 하는 Go 기반 MCP 서버입니다. 서버는 검색 후보와 원문을 제공하고, MCP를 호출하는 LLM이 근거를 읽어 답변·추가 검색·재질문 여부를 판단합니다. 사용 흐름과 응답 변경 사항은 [LLM 클라이언트 안내](docs/llm-client.md)를 참고하세요.

이 저장소는 수집기를 포함하지 않습니다. [`krx-rule-markdown`](https://github.com/chromato99/krx-rule-markdown)이 만든 schema-v2 `data/` release를 검증한 뒤 BM25/vector index generation을 생성하고, stdio 또는 Streamable HTTP MCP 서버로 제공합니다. 소스 checkout의 `index/current`는 현재 관리 중인 corpus와 맞춘 immutable generation을 가리킵니다.

## 제공 기능

- **MCP tools/resources**: `search_rules`, `get_context`, `get_rule`, `list_rules`, `get_attachment`, `list_recent_changes`와 `krx-rule://...` resource URI를 제공합니다.
- **언어별 corpus 제공**: `ko`/`en` metadata를 읽고 MCP 검색, 목록, 최근 변경 조회에서 언어 필터를 제공합니다.
- **수식 친화적 RAG**: HWP EqEdit 원본 수식과 LaTeX(best-effort) 변환 블록을 함께 인덱싱하고 `get_attachment`로 제공합니다.
- **BM25 기본 검색**: producer의 `release_hash`와 `index_source_hash`가 맞는 `KRXIDX2` snapshot을 로드해 한국어 2-gram/3-gram 기반 검색을 수행합니다.
- **KRX 도메인 사전 검색 보강**: `config/domain-lexicon.yaml`을 로드해 `동적상하한가 -> 실시간가격제한제도/가격변동폭/별표25` 같은 보수적 query expansion을 적용합니다.
- **선택형 vector 검색**: OpenAI 호환 embeddings API로 만든 `KRXVEC2` snapshot을 로드하고 BM25 + vector 결과를 RRF로 병합합니다.
- **선택형 한국어 evidence reranker**: 활성화한 경우 한국어 질의의 상위 20개 chunk를 재정렬합니다. 문서 RRF 순위는 유지하며, 재정렬 점수로 답변 가능 여부를 판정하지 않습니다.
- **RAG 문맥 재조회**: 검색 결과의 `evidence_matches[].chunk_id`로 `get_context`를 호출해 해당 chunk 주변 문맥만 다시 가져올 수 있습니다.
- **원자적 index generation**: BM25와 선택적 vector/metadata를 `generations/<content-id>/`에 완성·검증한 뒤 `current` 포인터 하나만 원자 교체합니다. 중단된 build는 활성 generation을 바꾸지 않습니다.
- **구조 anchor**: 조문 소유 관계, 항·호·목, heading path를 chunk에 저장하고 인용된 조문을 owning article로 오인하지 않습니다. 수식 원본/LaTeX pair와 table row는 분리하지 않습니다.
- **TEI sidecar 운영 예시**: Docker Compose 예시는 운영자가 선택한 호환 TEI 이미지를 embeddings sidecar로 함께 띄웁니다.
- **안전한 stateless HTTP 배포**: 서버측 MCP session을 보관하지 않으며 선택 가능한 Bearer 인증, 해시 기반 다중 토큰 registry, Origin allowlist, 요청·전체 응답·질의 크기 제한, 동시성 제한, deadline, rate limit, graceful shutdown을 제공합니다.
- **배포 generation 검증**: corpus/index/vector/도메인 사전/runtime mode/server image를 묶은 canonical release descriptor의 SHA-256을 응답·로그·metrics에 기록하고 `/readyz`에서 기대값과 비교합니다.

## Corpus 준비

이번 검색 평가와 저장소 기본 인덱스를 그대로 재현하려면 corpus의
[`8b8d951`](https://github.com/chromato99/krx-rule-markdown/commit/8b8d951f2d722c922fbbbd49d2ea4f532edbdbdc) 커밋을 사용하세요.
Corpus와 인덱스의 일치 조건은 [데이터 계약](docs/data-format.md)을 참고하세요.

먼저 별도 프로젝트인 [`krx-rule-markdown`](https://github.com/chromato99/krx-rule-markdown)에서 corpus를 생성합니다.

```bash
cd krx-rule-markdown
python3 -m pip install -e ".[convert]"
krx-rule-markdown sync --all --data-dir data
krx-rule-markdown clean --data-dir data --drop-past-rule-attachments --prune-unreferenced-attachments
krx-rule-markdown quality --data-dir data --output data/reports/data-quality.json --update-metadata
krx-rule-markdown validate --data-dir data --release --quality
```

`sync --all`은 기본적으로 한국어 규정/예고와 가능한 영문 규정 전문을 함께 생성합니다. 한국어만 필요하면 `--language ko`, 영문전문만 필요하면 `--language en`을 지정합니다.

생성된 `data/`는 운영 서버의 로컬 경로, Docker volume, CI artifact, release asset 등으로 전달합니다.
Corpus는 `ko/rules/<title>/index.md`, `ko/notices/<title>/index.md`, `en/rules/<title>/index.md`를 문서 단위로 읽습니다.
각 문서의 원본 첨부는 같은 디렉터리의 `raw/`, 변환 Markdown은 `attachments/`에 있어야 하며, MCP는 첨부를 부모 문서의 RAG chunk로 인덱싱합니다.
HWP 첨부의 수식은 `krx-rule-markdown`이 만든 `## HWP 수식` 섹션에 원본 `hwp-equation` 블록과 LaTeX `math` 블록으로 함께 들어갑니다. MCP는 두 블록을 그대로 읽고 검색 대상으로 삼습니다.

```bash
export KRX_RULE_DATA_DIR=/opt/krx-rule-data
export KRX_RULE_INDEX_DIR=/opt/krx-rule-index
mkdir -p "$KRX_RULE_DATA_DIR"
mkdir -p "$KRX_RULE_INDEX_DIR"
rsync -a krx-rule-markdown/data/ "$KRX_RULE_DATA_DIR"/
```

로컬 개발 중 두 저장소를 같은 부모 디렉터리에 clone했다면, `KRX_RULE_DATA_DIR`에 sibling repo의 `data` 경로를 지정해도 됩니다. 이 저장소가 제공하는 기본 generation을 쓰려면 `KRX_RULE_INDEX_DIR`를 `krx-rule-mcp/index`로 지정하세요.

## Index 생성

BM25 index는 필수입니다. Index 디렉터리는 다음 generation 구조를 사용합니다.

```text
index/
  current                         # 활성 generation id 한 줄
  generations/<generation-id>/
    generation.json               # release/build identity와 artifact digest/size
    bm25.krxidx                    # 필수
    vectors.krxvec                 # 선택
    vectors.krxvec.meta.json       # vector가 있을 때 필수
```

`generation-id`는 corpus release, index source/build hash, BM25와 선택적 vector artifact descriptor를 묶은 content hash입니다. Build는 filesystem advisory lock으로 직렬화되고 sibling staging에서 모든 digest·coverage 검증을 마친 뒤에만 `current`를 교체합니다.

Vector를 포함한 generation은 현재 관리 중인 `krx-rule-markdown/data` corpus와 다음 embedding 설정으로 생성합니다.

`text-v1` 재생성에서는 검증된 기존 generation과 model/revision/dimensions/prefix가 같을 때 정확히 동일한 청크 텍스트의 벡터를 재사용합니다. 이동한 chunk ID나 바뀐 문서 metadata를 근거로 재사용하지 않으며, 변경된 텍스트는 다시 embedding합니다. Revision이 없거나 profile이 다르면 전체를 생성하고, `--force`는 재사용하지 않습니다. 새 artifact의 전체 coverage와 hash 검증이 끝난 뒤에만 `current`를 교체합니다.

| 항목 | 값 |
| --- | --- |
| model | `intfloat/multilingual-e5-small` |
| revision | `614241f622f53c4eeff9890bdc4f31cfecc418b3` |
| dimensions | `384` |
| document prefix | `passage: ` |
| query prefix | `query: ` |
| document input | `text-v1` (chunk text) |

같은 corpus와 같은 embedding 설정을 사용한다면 먼저 최신성만 확인하세요.

```bash
KRX_EMBEDDING_MODEL_REVISION=614241f622f53c4eeff9890bdc4f31cfecc418b3 \
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir ./index \
  --vector \
  --check
```

새 checkout의 기본 generation이 현재 corpus와 맞지 않거나 corpus를 재생성했다면 서버를 올리기 전에 `krx-rule-index`를 실행하세요. Schema-v2 manifest, strict release profile, frontmatter parity, corpus hash가 먼저 검증됩니다. BM25가 없거나 맞지 않으면 서버는 기동을 실패합니다. Vector는 같은 generation 안에서 corpus/build hash, model, dimensions, prefix, embedding input format, chunk-id coverage와 metadata digest가 모두 맞을 때만 채택됩니다.

```bash
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR"
```

이미 최신이면 활성 generation id를 출력하고 종료합니다. 강제로 새 generation을 만들려면 `--force`, 쓰기 없이 활성 descriptor와 모든 artifact digest까지 확인하려면 `--check`를 사용합니다. 저장소 기본 index를 갱신할 때는 `--index-dir ./index`로 생성한 `current`와 해당 `generations/<id>/`를 함께 커밋합니다.

Vector index는 선택입니다. 아래 재현 예시는 기본 제공 generation과 같은 multilingual E5 small, 384차원, E5 prefix, `text-v1` 입력을 사용합니다.

```bash
docker compose up -d krx-rule-embeddings

OPENAI_API_KEY=local \
KRX_EMBEDDING_BASE_URL=http://127.0.0.1:18081/v1 \
KRX_EMBEDDING_MODEL=intfloat/multilingual-e5-small \
KRX_EMBEDDING_MODEL_REVISION=614241f622f53c4eeff9890bdc4f31cfecc418b3 \
KRX_EMBEDDING_DIMENSIONS=384 \
KRX_EMBEDDING_INPUT_FORMAT=text-v1 \
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR" \
  --vector
```

기본 E5 profile에서 `KRX_EMBEDDING_QUERY_PREFIX`는 `query: `, `KRX_EMBEDDING_DOCUMENT_PREFIX`는 `passage: `이며 `KRX_EMBEDDING_INPUT_FORMAT`은 `text-v1`입니다. 바이너리를 직접 실행할 때 다른 모델을 선택하면 E5 revision과 prefix를 상속하지 않으며 `KRX_EMBEDDING_DIMENSIONS`를 반드시 지정해야 합니다. Compose는 완전한 E5 profile을 기본 환경으로 주입하므로 다른 모델을 사용할 때는 model, revision, dimensions, query/document prefix와 input format을 한 묶음으로 모두 설정하세요. Prefix가 필요 없는 모델은 해당 환경변수를 명시적으로 빈 값으로 설정합니다. 외부 API를 직접 사용할 때 revision은 비울 수 있지만, Compose의 TEI sidecar에는 모델이 지원하는 revision을 지정해야 합니다. `structured-v1`은 청크 본문 앞에 문서 제목·카테고리·조문·heading path·source를 고정 순서의 field로 넣고, `text-v1`은 본문만 넣습니다.

각 generation은 한 embedding profile만 사용합니다. 모델 종류를 제한하지 않지만 corpus hash, model, revision, dimensions, query/document prefix, input format과 chunk coverage가 index 생성 시점과 runtime에서 모두 일치해야 합니다. Release evaluator는 특정 모델명을 승인 조건으로 사용하지 않고 full-vector 무결성과 동일한 한국어 중심 품질 gate를 검사합니다. 저장소의 `index/current`는 현재 검증된 기본 E5 profile의 reference artifact입니다.

## 언어별 검색

`search_rules`, `list_rules`, `list_recent_changes`는 `language` 필터를 받습니다.

```json
{"query": "listing review", "language": "en", "limit": 5}
```

```json
{"query": "상장 심사", "language": "ko", "limit": 5}
```

영문 규정은 `id`가 `{한국어 규정 id}-en` 형태이고, `source_id`에 원 한국어 규정 id가 들어갑니다. 언어를 지정하지 않으면 한국어와 영문 corpus를 함께 검색합니다. 이 프로젝트의 품질 기준과 선택형 reranker는 한국어가 우선이므로 한국어 RAG 클라이언트는 `language: "ko"`를 명시해야 합니다.

## RAG 문맥 조회

`search_rules`는 문서 metadata와 최대 세 개의 `evidence_matches`를 반환합니다. 각 evidence에는 chunk id/index, source, owning `article_id`, `heading_path`, snippet과 채널별 점수가 들어갑니다. 첨부가 매칭된 경우 `attachment_matches`에도 attachment metadata와 chunk anchor가 들어갑니다. index는 0부터 시작하며 값이 0이어도 응답에서 생략되지 않습니다. 도메인 사전 확장어는 원 질의보다 낮은 BM25 가중치로 반영됩니다. 점수는 결과 정렬용 신호이며 정답 확률이 아닙니다.

KRX 도메인 사전이 적용된 경우 `query_expansion`도 함께 반환됩니다. 이 필드에는 원 query, 확장 query, 적용된 사전 항목, confidence, review status, source URL이 들어가며, RAG 클라이언트는 어떤 공식 용어로 recall이 보강됐는지 확인할 수 있습니다. 사전은 검색 보강용이며 최종 답변의 법적 근거는 반드시 `get_context`, `get_rule`, `get_attachment`에서 가져온 규정 본문이어야 합니다.

검색 결과를 답변 근거로 사용할 때는 상위 결과의 chunk id로 `get_context`를 호출해 같은 본문 또는 같은 첨부 안의 주변 chunk를 함께 가져오세요.

```json
{
  "chunk_id": "210205830#att-210205830-210107342-hwp-3",
  "before_chunks": 1,
  "after_chunks": 1,
  "max_chars": 6000
}
```

`get_context` 응답은 `document`, `chunks`, `content`를 포함합니다. `content`에는 각 chunk의 `chunk_id`, `source`, `attachment_id`가 HTML comment로 표시되어 있어 RAG 답변에서 근거 위치를 추적하기 쉽습니다.

`before_chunks`와 `after_chunks`는 생략 시 각각 1이며, `0`을 지정하면 해당 방향의 주변 chunk를 포함하지 않습니다. `get_rule`과 `get_attachment`는 기본 20,000자, 호출당 최대 50,000자를 반환합니다. `total_chars`, `truncated`, `next_offset`을 보고 다음 호출에 `offset=next_offset`을 전달해 이어 읽습니다. `get_context`도 기본 20,000자, 최대 50,000자로 제한됩니다. Resource text 역시 50,000자로 제한되며 `_meta.truncated`가 true이면 해당 `get_*` tool로 이어 읽습니다. 구조화 tool payload는 기본 512KiB byte 상한을 추가로 적용하므로 큰 목록은 page를 줄여야 합니다. 목록 API는 `limit`, `offset`, `total`, `next_offset`을 사용합니다. 카테고리 필터에 사용할 정확한 값은 `list_categories` 도구로 조회할 수 있습니다.

공개 MCP 응답은 별도 DTO를 사용합니다. 로컬 `path`, `raw_path`, `text_path`, 변환기 오류 같은 서버 내부 정보는 노출하지 않습니다. 문서와 첨부는 명시적 `searchable` 값을 가지며, 품질 경고가 있으면 해당 문서·매치·문맥·첨부 metadata의 `quality_notice`에 source별로 표시됩니다. `searchable=false` source는 text index에 들어가지 않습니다. 수집 시점의 sanitized request descriptor가 있으면 `official_source`에 KRX source page URL, `POST` endpoint, whitelist 문서 식별 parameter와 source-content hash만 제공하며 cookie·CSRF·header는 포함하지 않습니다.

이 corpus는 KRX 법무포털을 수집·변환한 파생 snapshot이며 공식 원문 자체가 아닙니다. 현재성, 법적 효력, 규제 준수에 영향을 주는 답변은 응답의 `source_url`과 시행일을 근거로 한국어 KRX 공식 원문을 다시 확인해야 합니다. 영문 문서, 검색 snippet, 첨부 변환문, 자동 생성 LaTeX는 탐색 보조 자료로 취급합니다.

## HWP 수식 RAG 사용

`get_attachment`는 변환된 첨부 Markdown을 offset page로, `krx-rule://attachments/{id}` resource는 최대 50,000자로 반환합니다. Page 범위 안의 보존된 HWP 수식에는 다음 내용이 포함됩니다.

- `hwp-equation`: HWP EqEdit 원본 수식
- `math`: RAG와 Markdown math rendering을 위한 LaTeX(best-effort) 변환
- 수식을 인용하거나 검증할 때 원본 HWP 수식과 LaTeX 변환을 함께 보라는 안내문

검색 인덱스는 일반 본문과 같은 방식으로 이 수식 섹션을 chunk에 포함합니다. 따라서 `\frac`, `\sum`, 한국어 수식 라벨, 원본 EqEdit 표현이 모두 검색 단서가 될 수 있습니다. 수식이 포함된 첨부가 검색되거나 `get_attachment`로 제공될 때는 구조화된 `formula_notice`도 함께 반환합니다.

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

LaTeX 변환은 자동 생성 결과이므로, 정확한 산식이 중요한 답변에서는 MCP 응답에 포함된 원본 `hwp-equation`도 함께 확인하세요.

변환 품질 metadata에는 산식처럼 보이는 텍스트 힌트와 보존된 수식 블록 수가 분리되어 있습니다. 사용자-facing `formula_notice`는 보존된 EqEdit/LaTeX 블록이 있는 경우에만 반환되어 체크박스나 날짜 같은 서식 문자가 수식 공지로 오탐되지 않도록 합니다.

## 서버 실행

서버 실행은 `krx-rule-index --check`가 통과한 뒤에 수행합니다. `krx-rule-mcp`는 기동 시 `$KRX_RULE_INDEX_DIR/current`를 한 번 해석하고 그 immutable generation 안의 artifact만 로드합니다.

stdio:

```bash
go run ./cmd/krx-rule-mcp \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR"
```

HTTP:

```bash
AUTH_DIR=/opt/krx-rule-mcp-auth
mkdir -p "$AUTH_DIR"
chmod 700 "$AUTH_DIR"

TOKEN="$(openssl rand -hex 32)"
TOKEN_SHA256="$(printf '%s' "$TOKEN" | openssl dgst -sha256 -r | awk '{print $1}')"
cat > "$AUTH_DIR/bearer-tokens.yaml" <<EOF
version: 1
tokens:
  - id: first-client
    sha256: $TOKEN_SHA256
    enabled: true
EOF
chmod 600 "$AUTH_DIR/bearer-tokens.yaml"

export RULE_MCP_AUTH_MODE=required
export RULE_MCP_BEARER_TOKEN_FILE="$AUTH_DIR/bearer-tokens.yaml"
KRX_VECTOR_SEARCH_ENABLED=true \
go run ./cmd/krx-rule-mcp \
  --mode http \
  --addr :8080 \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR" \
  --domain-lexicon config/domain-lexicon.yaml
```

`TOKEN` 값은 클라이언트에 한 번 전달하고 registry나 로그에는 저장하지 않습니다. Registry에는 토큰 문자열의 SHA-256만 기록되며 `id`는 운영용 식별자입니다. 파일은 1 MiB, 1,024개 token으로 제한되고 unknown field, 중복 id/hash, 잘못된 digest, 누락된 `enabled`, 활성 token이 없는 구성을 거부합니다.

HTTP의 기본 인증 모드는 `required`입니다. 신뢰된 내부망에서만 인증을 끄려면 `RULE_MCP_AUTH_MODE=disabled` 또는 `--auth-mode disabled`를 명시합니다. 이 경우 token 파일은 읽지 않지만 Origin, 크기, 동시성, deadline, rate limit은 계속 적용됩니다. HTTP transport는 stateless이므로 어느 replica로 요청이 전달되어도 서버측 session affinity가 필요하지 않습니다.

Token 추가·비활성화는 registry 파일을 수정한 뒤 서버를 재시작해야 반영됩니다. 안전한 rotation 순서는 다음과 같습니다.

1. 새 token의 hash를 `enabled: true`로 추가하고 모든 instance를 재시작합니다.
2. 전체 instance의 `/readyz`에서 같은 `auth_registry_generation`을 확인한 뒤 클라이언트를 새 token으로 전환합니다.
3. 기존 token을 `enabled: false`로 바꾸고 다시 전체 instance를 재시작합니다.

Kubernetes RollingUpdate 중에는 이전 Pod가 기존 token을 계속 허용할 수 있으므로 비활성화는 rollout 완료 시점부터 완전히 적용된 것으로 봅니다. Token registry digest는 RAG artifact의 `release_generation`에는 포함되지 않습니다.

Release generation을 계산하려면 운영 때와 같은 corpus/index/vector/embedding 환경, `RULE_MCP_SERVER_IMAGE_DIGEST`, `RULE_MCP_TEI_IMAGE_DIGEST`를 사용해 다음을 실행합니다.

```bash
RULE_MCP_SERVER_IMAGE_DIGEST="sha256:<published-image-digest>" \
RULE_MCP_TEI_IMAGE_DIGEST="sha256:<tei-runtime-image-digest>" \
go run ./cmd/krx-rule-mcp \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR" \
  --print-release-generation
```

출력된 `release_generation`을 `RULE_MCP_EXPECTED_RELEASE_GENERATION`에 설정하면 `/readyz`는 실제 canonical descriptor와 일치하는 Pod만 ready로 처리합니다. Descriptor에는 corpus/index source·build hash, BM25 artifact digest, 채택된 vector와 metadata digest, 도메인 사전 digest, runtime vector mode, server image digest와 TEI runtime image digest가 포함됩니다. 따라서 server 또는 TEI image만 교체해도 별도의 release generation이 생성됩니다.

Vector 검색을 쓰려면 index 생성 때 `--vector`로 vector 포함 generation을 publish하고, 서버에 `KRX_VECTOR_SEARCH_ENABLED=true`를 지정합니다. 서버 실행에는 별도 vector 경로를 주지 않습니다. 비활성화하면 generation에 vector가 있어도 파일을 읽지 않습니다. `KRX_VECTOR_SEARCH_POLICY=optional`은 잘못된 vector/embedding 설정에서 BM25로 fallback합니다. 운영에서 vector가 필수이면 `KRX_VECTOR_SEARCH_POLICY=required` 또는 `--require-vector`를 사용합니다. 이 정책은 full coverage와 embedding 설정이 없으면 기동을 실패시키고, runtime embedding 오류·timeout·잘못된 vector 응답은 BM25 결과가 아닌 MCP tool error로 반환합니다. HTTP `/readyz`도 실제 canary embedding이 유효해야 200을 반환합니다.

한국어 reranker는 기본적으로 꺼져 있습니다. 활성화하면 한국어 질의의 제한된 상위 후보를 재정렬하며, 이전의 `supported` 판정에 따른 실행·채택 조건은 사용하지 않습니다. 문서 순위는 유지하고 문서 내부 evidence 순서를 조정합니다. `KRX_RERANKER_POLICY=required`의 모델 검증·기동·readiness 조건은 유지합니다. 서버에 답변 생성용 LLM을 추가할 필요는 없습니다.

## Docker Compose

Compose는 서버와 사용자가 선택한 TEI embeddings sidecar를
required-vector 정책으로 띄우는 운영 런타임입니다. TEI `/health`가
성공해야 서버가 시작됩니다. `krx-rule-mcp`는 특정 TEI 이미지를
기본값으로 선택하지 않으며, 사용자는 대상 아키텍처와 아래 embedding
계약에 맞는 이미지를 `RULE_MCP_TEI_IMAGE`와 digest로 지정해야 합니다.
Corpus sync는 compose 서비스가 아니라
[`krx-rule-markdown`](https://github.com/chromato99/krx-rule-markdown)의
단발성 작업으로 수행합니다.

선택형 reranker는 별도의 `reranker` profile입니다. CPU 기준으로 고정된 `dragonkue/bge-reranker-v2-m3-ko` revision, F16, 4-passage batch, 2,048-token batch 한도를 예시로 제공하지만 자동 활성화하지 않습니다.

```bash
docker compose --profile reranker up -d krx-rule-reranker
KRX_RERANKER_ENABLED=true \
KRX_RERANKER_POLICY=required \
KRX_REQUIRE_RERANKER=true \
docker compose up -d krx-rule-mcp
```

```bash
cp .env.compose.example .env
vi .env  # data/index, auth registry directory, user-selected TEI image/digest 설정
docker compose up -d --build
curl http://localhost:8080/healthz
```

`KRX_RULE_DATA_DIR` host path는 컨테이너의 `/app/data:ro`로, `KRX_RULE_INDEX_DIR` host path는 `/app/index:ro`로, `RULE_MCP_AUTH_CONFIG_DIR`은 `/run/secrets/krx-rule-mcp:ro`로 mount됩니다. 인증 directory에는 위 형식의 `bearer-tokens.yaml`을 둡니다. 변경 후 `docker compose restart krx-rule-mcp`로 registry를 다시 로드합니다. 로컬에서 저장소 기본 generation을 쓰려면 `KRX_RULE_INDEX_DIR`를 checkout의 `krx-rule-mcp/index`로 지정할 수 있습니다. Server image는 corpus나 index를 내장하지 않으므로 volume mount가 필요합니다. 기본 host bind는 `127.0.0.1`이며 외부 공개가 필요할 때만 `RULE_MCP_BIND_ADDRESS`를 변경합니다.

세 경로는 non-root 컨테이너 사용자가 읽을 수 있어야 합니다. 로컬 테스트용 임시 디렉터리를 쓸 때는 `chmod -R a+rX "$KRX_RULE_DATA_DIR" "$KRX_RULE_INDEX_DIR" "$RULE_MCP_AUTH_CONFIG_DIR"`처럼 읽기 권한을 열어 주세요.

## Embeddings 설정

저장소 제공 vector index의 embedding 계약과 Compose 예시 설정:

- `KRX_EMBEDDING_MODEL=intfloat/multilingual-e5-small`
- `KRX_EMBEDDING_MODEL_REVISION=614241f622f53c4eeff9890bdc4f31cfecc418b3`
- `KRX_EMBEDDING_DIMENSIONS=384`
- `KRX_EMBEDDING_BASE_URL=http://krx-rule-embeddings:80/v1`
- `OPENAI_API_KEY=local`
- `KRX_EMBEDDING_QUERY_PREFIX=query: `
- `KRX_EMBEDDING_DOCUMENT_PREFIX=passage: `
- `KRX_EMBEDDING_INPUT_FORMAT=text-v1`

위 값은 저장소 제공 reference artifact의 기본 profile이며 프로젝트의 유일한 허용 모델이 아닙니다. 다른 TEI 모델이나 외부 OpenAI 호환 embeddings API도 사용할 수 있지만 해당 모델의 dimensions, revision, prefix와 input format으로 vector generation을 다시 만들어야 합니다. 비기본 모델은 E5 revision/prefix를 자동 상속하지 않습니다. Vector metadata에는 corpus/index hash, model/revision, dimensions, query/document prefix, embedding input format, scope와 chunk-id coverage가 기록되며 하나라도 runtime 설정과 다르면 vector 검색은 비활성화되거나 required 정책에서 기동을 실패합니다.

TEI 이미지는 운영자가 선택합니다. `RULE_MCP_TEI_IMAGE`에는 대상
아키텍처에서 동작하고 이 Compose의 CLI·HTTP 계약과 호환되는 이미지를
지정하세요. 재현 가능한 배포에서는 tag가 아니라 manifest digest를
포함한 immutable reference와 동일한 `RULE_MCP_TEI_IMAGE_DIGEST`를
사용해야 합니다. 문서의 이미지 문자열은 형식 예시일 뿐 기본값이나
권장 이미지가 아닙니다.

## 도메인 사전

기본 사전 파일은 [config/domain-lexicon.yaml](config/domain-lexicon.yaml)입니다. 서버는 시작할 때 이 YAML을 읽고, 파싱/검증에 실패하면 설정 오류로 종료합니다. 다른 파일을 쓰려면 `--domain-lexicon` 또는 `KRX_DOMAIN_LEXICON_PATH`를 지정하세요.

사전은 KRX 법무포털 corpus, KRX 제도 설명 페이지, KRX ETF 용어사전, KRX Global 영문 페이지를 근거로 관리합니다. 자세한 출처와 운영 원칙은 [docs/domain-lexicon.md](docs/domain-lexicon.md)를 참고하세요.

## 검색 품질 평가

`krx-rule-eval`은 실제 `search_rules → get_context` 경로에서 검색 결과와 근거를 검사합니다. 유지하는 평가 자료는 다음 세 파일입니다.

- `eval/fixtures/retrieval.json`: 질의·필터·문서/조문/첨부 target을 포함한 210개 회귀 사례.
- `eval/baselines/retrieval.json`: 동일 corpus/index/질의 계약의 언어별 검색 회귀 기준선.
- `eval/schema/retrieval.schema.json`: 현재 평가 fixture의 schema v2.

문서 Hit@5와 상위 5개 문서의 근거 묶음 포함률, 모든 반환 청크의 소유 관계·잘림·필터 누출을 검사합니다. 범위 밖/모호한 질문에 검색 후보가 있다는 사실은 답변 승인으로 세지 않습니다. 최종 답변 정확성·거절·재질문은 호출 LLM의 별도 평가 대상입니다.

```bash
KRX_EMBEDDING_BASE_URL=http://127.0.0.1:18081/v1 \
go run ./cmd/krx-rule-eval \
  --data-dir ../krx-rule-markdown/data --index-dir index \
  --vector --require-vector --fail-on-gate \
  --output /tmp/krx-retrieval-eval.json
```

`--fail-on-gate`는 full-vector 무결성, 반환 계약, 필터, 문맥 일치와 고정 기준선 대비 검색 회귀를 검사합니다. `--split`/`--case-prefix`는 진단 전용이며 gate와 함께 사용하지 않습니다. BM25 진단은 vector 플래그를 생략합니다.

별도 fixture는 `--fixture`로 지정합니다. 봉인된 holdout은 외부에서 준비한 canonical source 예약 파일을 `--holdout-reservation`으로 함께 지정해야 합니다. 이미 검토한 자료를 독립 평가로 재사용하지 않습니다. 평가 지표와 절차는 [품질 계약](docs/rag-quality-contract.md)에 정리합니다.

`eval/compare.py <변경 전 보고서> <변경 후 보고서>`로 같은 질의/target과 인덱스를 사용한 결과를 비교합니다. 실행 결과는 `eval/results/`에 생성되며 Git에서 제외됩니다. 일회성 실험·샘플·아카이브는 프로젝트 소스에 포함하지 않습니다.

`.github/workflows/retrieval-eval.yml`은 정확한 corpus commit을 받아 고정 인덱스를 검증하는 수동 검색 회귀 검사입니다. 자동 배포를 수행하지 않습니다.

## 테스트

```bash
go test ./...
go test -race ./...
KRX_RULE_DATA_DIR=/opt/krx-rule-data \
KRX_RULE_INDEX_DIR=/opt/krx-rule-index \
RULE_MCP_AUTH_CONFIG_DIR=/opt/krx-rule-auth \
RULE_MCP_TEI_IMAGE=registry.example/tei@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc \
RULE_MCP_TEI_IMAGE_DIGEST=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc \
docker compose config
```

실제 KRX 포털 수집 테스트는 이 저장소가 아니라 [`krx-rule-markdown`](https://github.com/chromato99/krx-rule-markdown)에서 수행합니다.
