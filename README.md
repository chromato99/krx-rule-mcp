# KRX Rule MCP

AI 클라이언트에서 한국거래소 규정·개정예고·첨부를 검색하고, 찾은 조문의 앞뒤 문맥을 읽을 수 있는 MCP 서버입니다. 한국어·영어를 지원하며, 규정 수집과 첨부 변환은 [krx-rule-markdown](https://github.com/chromato99/krx-rule-markdown)이 담당합니다.

## 원격 테스트 서버에 연결하기

Streamable HTTP 주소는 `https://krx-rule-mcp.chromato99.com/mcp`입니다. 이 테스트 서버는 토큰 없이 연결할 수 있습니다. 사용하는 클라이언트에 한 번 등록하세요.

**Codex**

```bash
codex mcp add krx-rule-mcp --url https://krx-rule-mcp.chromato99.com/mcp
codex mcp list
```

**Claude Code**

```bash
claude mcp add --transport http --scope user krx-rule-mcp https://krx-rule-mcp.chromato99.com/mcp
claude mcp list
```

**Google Antigravity**에서는 IDE의 **MCP Servers → Manage MCP Servers → View raw config** 또는 CLI의 `~/.gemini/config/mcp_config.json`을 엽니다. 기존 설정이 있다면 `mcpServers`에 서버 항목만 추가합니다. [Antigravity 안내](https://antigravity.google/docs/mcp)

```json
{"mcpServers": {"krx-rule-mcp": {"serverUrl": "https://krx-rule-mcp.chromato99.com/mcp"}}}
```

**Cursor**에서는 `~/.cursor/mcp.json`의 `mcpServers`에 등록합니다. [Cursor 안내](https://cursor.com/docs/mcp)

```json
{"mcpServers": {"krx-rule-mcp": {"url": "https://krx-rule-mcp.chromato99.com/mcp"}}}
```

**VS Code의 GitHub Copilot**에서는 명령 팔레트의 **MCP: Add Server → HTTP**를 선택하고, 위 주소와 이름 `krx-rule-mcp`를 입력한 뒤 **Global**로 저장합니다. [VS Code 안내](https://code.visualstudio.com/docs/agent-customization/mcp-servers)

새 대화에서 다음과 같이 요청하면 검색과 원문 조회를 함께 시험할 수 있습니다.

> KRX MCP로 「저PBR기업 선정 등에 관한 지침」의 선정 기준을 찾아 주세요. `search_rules`가 반환한 `chunk_id`를 `get_context`로 읽고, 적용 조건과 예외를 규정명·조문·출처 링크와 함께 알려 주세요.

서버는 수집 시점의 자료를 제공합니다. 호출 모델은 검색된 조문을 읽고 적용 시장·주체·시행일을 확인합니다. 현행 규정이나 법적 효력이 중요한 답변은 `source_url`에서 시행 중인 한국어 원문을 대조하세요. [클라이언트 안내](docs/llm-client.md)

## 사용할 수 있는 도구

| 목적 | MCP 도구 |
| --- | --- |
| 규정·첨부 검색, 일치한 대목과 앞뒤 문맥 읽기 | `search_rules` → `get_context` |
| 규정·개정예고 본문 또는 변환된 첨부 이어 읽기 | `get_rule`, `get_attachment` |
| 문서 목록, 카테고리, 최근 변경 확인 | `list_rules`, `list_categories`, `list_recent_changes` |

검색과 목록 조회에서 `language`를 `ko` 또는 `en`으로 지정하면 해당 언어로 범위를 좁힙니다. 생략하면 두 언어를 함께 조회합니다. 문서와 첨부는 응답의 `krx-rule://` URI로도 읽을 수 있습니다.

## 직접 실행하기

**Docker Compose**로 실행하면 서버 이미지에 포함된 규정 데이터와 BM25·벡터 인덱스를 사용합니다. 별도 수집이나 인덱스 생성은 생략할 수 있습니다. 질의를 벡터로 변환하는 TEI(Text Embeddings Inference) 이미지와 자체 HTTP 서버의 Bearer 인증 파일을 준비하세요. [Compose 설정과 실행](docs/deployment.md#docker-compose)

**로컬 stdio**는 두 저장소를 나란히 두고, 기본 인덱스와 일치하는 규정 데이터로 바로 실행할 수 있습니다. 아래 명령은 `krx-rule-mcp/`에서 실행합니다. 벡터 서비스 없이 BM25로 검색합니다.

```bash
export KRX_RULE_DATA_DIR=../krx-rule-markdown/data
go run ./cmd/krx-rule-index --index-dir ./index --check
go run ./cmd/krx-rule-mcp --index-dir ./index
```

기본 인덱스에 맞는 규정 데이터는 [수집 저장소의 고정 커밋](https://github.com/chromato99/krx-rule-markdown/tree/4f3a111f7184737f6e959b50af62f9cc87db412f)에 있습니다. 다른 데이터를 쓰거나 배포 방식을 선택할 때는 아래 안내를 참고하세요.

| 하려는 일 | 안내 |
| --- | --- |
| 자체 HTTP·Compose·Kubernetes 배포, 외부 데이터로 교체 | [배포](docs/deployment.md) · [인증과 토큰 교체](docs/security.md) |
| 규정 수집물 검증, 인덱스 생성, 임베딩 모델 변경 | [데이터 형식](docs/data-format.md) · [인덱스 생성](docs/deployment.md#manual-index-jobs) · [임베딩 설정](docs/search.md#embedding-profile) |
| 검색 순위 이해, 용어 추가, 코드 위치 확인 | [검색 방식](docs/search.md) · [도메인 사전](docs/domain-lexicon.md) · [아키텍처](docs/architecture.md) |
| 검색 품질 측정, 호출 모델의 답변 평가 | [평가 기준과 실행 명령](docs/rag-quality-contract.md) |
| 출처 표기와 재배포 조건 확인 | [원문 이용 안내](docs/legal-notice.md) · [코드 라이선스](LICENSE) |

Go 테스트는 저장소 루트에서 `go test ./...`로 실행합니다. 동시성 검사에는 `go test -race ./...`를 사용합니다.
