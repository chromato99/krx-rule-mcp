# 검색 개선 평가 원본

`rag-improvements-20260906.tar.gz`는 검색 개선 실험과 호출 LLM 구조 전환에서
생성한 원시 보고서, 변경 전 검색 응답·문맥, 전환 직전 소스를 보관합니다.
검토할 요약·fixture·baseline은 저장소에 일반 파일로 포함하며, 반복되는 대형
원시 JSON은 이 압축 파일로 제공합니다. `eval/results/`의 풀린 실행 결과는
커밋하지 않습니다.

`manifest.json`에 압축 파일 및 각 파일의 SHA-256과 크기를 기록합니다. 압축은
경로·mtime·소유자 정보를 고정하여 생성했습니다. 측정 지표와 명시된 실패를
다시 확인할 수 있지만 이미 소비된 검증 자료를 독립 holdout으로 만들지는 않습니다.

원본 파일을 별도 디렉터리에 풀어 확인할 수 있습니다.

```bash
mkdir -p /tmp/krx-rag-audit
tar -xzf eval/archives/rag-improvements-20260906.tar.gz -C /tmp/krx-rag-audit
```

- `eval/results/caller-llm/`: 같은 v3 지표로 비교한 변경 전후 210/38문항 보고서.
- `eval/experiments/20260906-scope/`: 이전 검색·답변 판정 단계의 실패와 성능 측정.
- `eval/experiments/20260906/`, `eval/holdout/`, 그 외 `eval/results/`: 앞선 실험 기록.
- `captures/`: 변경 전 실제 서비스 응답/문맥과 수집·재평가 스크립트 원본.
- `historical-runtime/`: 호출 LLM 구조로 전환하기 직전 Go 소스와 설정. 현재 실행 코드와 별도입니다.

현재 비교 도구로 압축 안의 v3 보고서를 읽는 예시입니다.

```bash
python3 eval/compare.py \
  /tmp/krx-rag-audit/eval/results/caller-llm/rag-v1-before.json \
  /tmp/krx-rag-audit/eval/results/caller-llm/rag-v1-after.json
```

원본 스크립트의 로컬 경로는 당시 실행 환경의 기록입니다. 재실행 시 공개된
corpus 커밋과 index generation 경로를 지정해야 합니다. 이 평가의 corpus는
[krx-rule-markdown `8b8d951`](https://github.com/chromato99/krx-rule-markdown/commit/8b8d951f2d722c922fbbbd49d2ea4f532edbdbdc)입니다.
