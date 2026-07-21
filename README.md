# KRX Rule MCP

한국거래소 법무포털 규정 corpus를 AI 클라이언트가 빠르게 검색하고 참조할 수 있게 하는 Go 기반 MCP 서버입니다.

이 저장소는 수집기를 포함하지 않습니다. [`krx-rule-markdown`](https://github.com/chromato99/krx-rule-markdown)이 만든 schema-v2 `data/` release를 검증한 뒤 BM25/vector index generation을 생성하고, stdio 또는 Streamable HTTP MCP 서버로 제공합니다. 소스 checkout의 `index/current`는 현재 관리 중인 corpus와 맞춘 immutable generation을 가리킵니다.

## 제공 기능

- **MCP tools/resources**: `search_rules`, `get_context`, `get_rule`, `list_rules`, `get_attachment`, `list_recent_changes`와 `krx-rule://...` resource URI를 제공합니다.
- **언어별 corpus 제공**: `ko`/`en` metadata를 읽고 MCP 검색, 목록, 최근 변경 조회에서 언어 필터를 제공합니다.
- **수식 친화적 RAG**: HWP EqEdit 원본 수식과 LaTeX(best-effort) 변환 블록을 함께 인덱싱하고 `get_attachment`로 제공합니다.
- **BM25 기본 검색**: producer의 `release_hash`와 `index_source_hash`가 맞는 `KRXIDX2` snapshot을 로드해 한국어 2-gram/3-gram 기반 검색을 수행합니다.
- **KRX 도메인 사전 검색 보강**: `config/domain-lexicon.yaml`을 로드해 `동적상하한가 -> 실시간가격제한제도/가격변동폭/별표25` 같은 보수적 query expansion을 적용합니다.
- **선택형 vector 검색**: OpenAI 호환 embeddings API로 만든 `KRXVEC2` snapshot을 로드하고 BM25 + vector 결과를 RRF로 병합합니다.
- **RAG 문맥 재조회**: 검색 결과의 `matched_chunk_id` 또는 `attachment_matches[].chunk_id`로 `get_context`를 호출해 해당 chunk 주변 문맥만 다시 가져올 수 있습니다.
- **원자적 index generation**: BM25와 선택적 vector/metadata를 `generations/<content-id>/`에 완성·검증한 뒤 `current` 포인터 하나만 원자 교체합니다. 중단된 build는 활성 generation을 바꾸지 않습니다.
- **구조 anchor**: 조문 소유 관계, 항·호·목, heading path를 chunk에 저장하고 인용된 조문을 owning article로 오인하지 않습니다. 수식 원본/LaTeX pair와 table row는 분리하지 않습니다.
- **TEI sidecar 운영**: Docker Compose 기본 구성이 Hugging Face Text Embeddings Inference sidecar를 함께 띄웁니다.
- **안전한 stateless HTTP 배포**: 서버측 MCP session을 보관하지 않으며 Bearer token, Origin allowlist, 요청·전체 응답·질의 크기 제한, 동시성 제한, deadline, rate limit, graceful shutdown을 제공합니다.
- **배포 generation 검증**: corpus/index/vector/도메인 사전/runtime mode/server image를 묶은 canonical release descriptor의 SHA-256을 응답·로그·metrics에 기록하고 `/readyz`에서 기대값과 비교합니다.

## Corpus 준비

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

| 항목 | 값 |
| --- | --- |
| model | `intfloat/multilingual-e5-small` |
| dimensions | `384` |
| document prefix | `passage: ` |
| query prefix | `query: ` |

같은 corpus와 같은 embedding 설정을 사용한다면 먼저 최신성만 확인하세요.

```bash
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir ./index \
  --vector-index ./index/vectors.krxvec \
  --check
```

새 checkout의 기본 generation이 현재 corpus와 맞지 않거나 corpus를 재생성했다면 서버를 올리기 전에 `krx-rule-index`를 실행하세요. Schema-v2 manifest, strict release profile, frontmatter parity, corpus hash가 먼저 검증됩니다. BM25가 없거나 맞지 않으면 서버는 기동을 실패합니다. Vector는 같은 generation 안에서 corpus/build hash, model, dimensions, prefix, chunk-id coverage와 metadata digest가 모두 맞을 때만 채택됩니다.

```bash
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR"
```

이미 최신이면 활성 generation id를 출력하고 종료합니다. 강제로 새 generation을 만들려면 `--force`, 쓰기 없이 활성 descriptor와 모든 artifact digest까지 확인하려면 `--check`를 사용합니다. 저장소 기본 index를 갱신할 때는 `--index-dir ./index`로 생성한 `current`와 해당 `generations/<id>/`를 함께 커밋합니다.

Vector index는 선택입니다. 기본 예시는 기본 제공 index와 같은 `intfloat/multilingual-e5-small`, 384차원, E5 prefix를 사용합니다.

```bash
docker compose up -d krx-rule-embeddings

OPENAI_API_KEY=local \
KRX_EMBEDDING_BASE_URL=http://127.0.0.1:18081/v1 \
KRX_EMBEDDING_MODEL=intfloat/multilingual-e5-small \
KRX_EMBEDDING_DIMENSIONS=384 \
go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR" \
  --vector-index "$KRX_RULE_INDEX_DIR/vectors.krxvec"
```

`--vector-index`는 기존 CLI 호환을 위한 “vector 포함” 선택자입니다. 실제 BM25, vector, metadata는 지정한 index 디렉터리의 같은 immutable generation 안에 기록되며 root의 개별 파일을 차례로 덮어쓰지 않습니다.

`KRX_EMBEDDING_QUERY_PREFIX` 기본값은 `query: `, `KRX_EMBEDDING_DOCUMENT_PREFIX` 기본값은 `passage: `입니다. Vector freshness는 corpus hash, model, dimensions, query/document prefix가 모두 같을 때만 최신으로 봅니다.

다른 embedding 모델을 쓰려면 index 생성과 서버 실행에 같은 embedding 설정을 사용해야 합니다. 예를 들어 OpenAI 호환 외부 API로 `text-embedding-3-small`을 쓰는 경우:

```bash
export KRX_EMBEDDING_BASE_URL=https://api.openai.com/v1
export OPENAI_API_KEY=...
export KRX_EMBEDDING_MODEL=text-embedding-3-small
export KRX_EMBEDDING_DIMENSIONS=1536
export KRX_EMBEDDING_QUERY_PREFIX=""
export KRX_EMBEDDING_DOCUMENT_PREFIX=""

go run ./cmd/krx-rule-index \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR" \
  --vector-index "$KRX_RULE_INDEX_DIR/vectors.krxvec" \
  --force
```

TEI sidecar 자체 모델을 바꾸려면 `KRX_TEI_MODEL_ID`와 `KRX_EMBEDDING_MODEL`을 같은 모델 id로 맞추고, 해당 모델의 출력 차원으로 `KRX_EMBEDDING_DIMENSIONS`를 설정한 뒤 sidecar를 재시작하고 vector index를 다시 생성하세요. E5 계열이 아닌 모델은 모델 권장 방식에 맞춰 query/document prefix를 바꾸거나 빈 문자열로 둘 수 있습니다. Prefix도 vector metadata에 기록되므로, index 생성 때와 서버 실행 때 값이 다르면 vector snapshot은 거부됩니다.

## 언어별 검색

`search_rules`, `list_rules`, `list_recent_changes`는 `language` 필터를 받습니다.

```json
{"query": "listing review", "language": "en", "limit": 5}
```

```json
{"query": "상장 심사", "language": "ko", "limit": 5}
```

영문 규정은 `id`가 `{한국어 규정 id}-en` 형태이고, `source_id`에 원 한국어 규정 id가 들어갑니다. 언어를 지정하지 않으면 한국어와 영문 corpus를 함께 검색합니다.

## RAG 문맥 조회

`search_rules`는 문서 metadata, snippet, `matched_chunk_id`, `matched_chunk_index`, owning `article_id`, `heading_path`를 반환합니다. 첨부가 매칭된 경우 `attachment_matches`에도 같은 anchor와 chunk id/index가 들어갑니다. 두 index 필드는 0부터 시작하며 값이 0이어도 응답에서 생략되지 않습니다. 조문 범위를 추측한 휴리스틱 값은 공개 API에 내보내지 않습니다. 도메인 사전 확장어는 원 질의보다 낮은 BM25 가중치로 반영됩니다. 점수는 결과 정렬용 신호이며 정답 확률이 아닙니다.

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
export KRX_MCP_BEARER_TOKEN="$(openssl rand -hex 32)"
KRX_VECTOR_SEARCH_ENABLED=true \
go run ./cmd/krx-rule-mcp \
  --mode http \
  --addr :8080 \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR" \
  --domain-lexicon config/domain-lexicon.yaml
```

`change-me`, `REPLACE_WITH_STRONG_RANDOM_TOKEN` 같은 알려진 placeholder token은 HTTP 기동 전에 거부됩니다. HTTP transport는 stateless이므로 어느 replica로 요청이 전달되어도 서버측 session affinity가 필요하지 않습니다.

Release generation을 계산하려면 운영 때와 같은 corpus/index/vector/embedding 환경과 `KRX_SERVER_IMAGE_DIGEST`를 사용해 다음을 실행합니다.

```bash
KRX_SERVER_IMAGE_DIGEST="sha256:<published-image-digest>" \
go run ./cmd/krx-rule-mcp \
  --data-dir "$KRX_RULE_DATA_DIR" \
  --index-dir "$KRX_RULE_INDEX_DIR" \
  --print-release-generation
```

출력된 `release_generation`을 `KRX_EXPECTED_RELEASE_GENERATION`에 설정하면 `/readyz`는 실제 canonical descriptor와 일치하는 Pod만 ready로 처리합니다. Descriptor에는 corpus/index source·build hash, BM25 artifact digest, 채택된 vector와 metadata digest, 도메인 사전 digest, runtime vector mode, server image digest가 포함됩니다.

Vector 검색을 쓰려면 index 생성 때 `--vector-index`로 vector 포함 generation을 publish하고, 서버에 `KRX_VECTOR_SEARCH_ENABLED=true`를 지정합니다. 서버 실행에는 별도 vector 경로를 주지 않습니다. 비활성화하면 generation에 vector가 있어도 파일을 읽지 않습니다. 기본 `KRX_VECTOR_SEARCH_POLICY=optional`은 잘못된 vector/embedding 설정에서 BM25로 fallback합니다. 운영에서 vector가 필수이면 `KRX_VECTOR_SEARCH_POLICY=required` 또는 `--require-vector`를 사용하며, 이때 full coverage와 올바른 embedding 설정이 없으면 기동을 실패합니다.

## Docker Compose

Compose는 서버와 TEI embeddings sidecar를 띄우는 운영 런타임입니다. Corpus sync는 compose 서비스가 아니라 [`krx-rule-markdown`](https://github.com/chromato99/krx-rule-markdown)의 단발성 작업으로 수행합니다.

```bash
cp .env.compose.example .env
vi .env  # KRX_RULE_DATA_DIR, KRX_RULE_INDEX_DIR, 강한 KRX_MCP_BEARER_TOKEN 설정
docker compose up -d --build
curl http://localhost:8080/healthz
```

`KRX_RULE_DATA_DIR` host path는 컨테이너의 `/app/data:ro`로, `KRX_RULE_INDEX_DIR` host path는 `/app/index:ro`로 mount됩니다. 로컬에서 저장소 기본 generation을 쓰려면 `KRX_RULE_INDEX_DIR`를 checkout의 `krx-rule-mcp/index`로 지정할 수 있습니다. Server image는 corpus나 index를 내장하지 않으므로 volume mount가 필요합니다. 기본 host bind는 `127.0.0.1`이며 외부 공개가 필요할 때만 `KRX_MCP_BIND_ADDRESS`를 변경합니다.
두 경로는 non-root 컨테이너 사용자가 읽을 수 있어야 합니다. 로컬 테스트용 임시 디렉터리를 쓸 때는 `chmod -R a+rX "$KRX_RULE_DATA_DIR" "$KRX_RULE_INDEX_DIR"`처럼 읽기 권한을 열어 주세요.

## Embeddings 설정

기본 제공 vector index와 Compose 기본값:

- `KRX_EMBEDDING_MODEL=intfloat/multilingual-e5-small`
- `KRX_EMBEDDING_MODEL_REVISION=` (로컬에서는 선택; required-vector 배포에서는 immutable model commit 필수)
- `KRX_EMBEDDING_DIMENSIONS=384`
- `KRX_EMBEDDING_BASE_URL=http://krx-rule-embeddings:80/v1`
- `OPENAI_API_KEY=local`
- `KRX_EMBEDDING_QUERY_PREFIX=query: `
- `KRX_EMBEDDING_DOCUMENT_PREFIX=passage: `

외부 OpenAI 호환 embeddings API나 다른 TEI 모델을 쓰려면 vector 포함 generation을 새 설정으로 재생성하고, 서버 실행 환경에도 같은 `KRX_EMBEDDING_*` 값을 지정하세요. Vector metadata에는 corpus/index hash, model/revision, dimensions, query/document prefix, scope와 chunk-id coverage가 기록되며 하나라도 다르면 vector 검색은 비활성화되거나 required 정책에서 기동을 실패합니다.

## 도메인 사전

기본 사전 파일은 [config/domain-lexicon.yaml](config/domain-lexicon.yaml)입니다. 서버는 시작할 때 이 YAML을 읽고, 파싱/검증에 실패하면 설정 오류로 종료합니다. 다른 파일을 쓰려면 `--domain-lexicon` 또는 `KRX_DOMAIN_LEXICON_PATH`를 지정하세요.

사전은 KRX 법무포털 corpus, KRX 제도 설명 페이지, KRX ETF 용어사전, KRX Global 영문 페이지를 근거로 관리합니다. 자세한 출처와 운영 원칙은 [docs/domain-lexicon.md](docs/domain-lexicon.md)를 참고하세요.

## 테스트

```bash
go test ./...
go test -race ./...
KRX_RULE_DATA_DIR=/opt/krx-rule-data KRX_RULE_INDEX_DIR=/opt/krx-rule-index docker compose config
```

실제 KRX 포털 수집 테스트는 이 저장소가 아니라 [`krx-rule-markdown`](https://github.com/chromato99/krx-rule-markdown)에서 수행합니다.
