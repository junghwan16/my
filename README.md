# gieok

`gieok`은 코딩 에이전트가 예전 작업에서 배운 것을 다시 떠올리게 해 주는 로컬 CLI입니다.
세션 파일을 먼저 **원본(Source)** 으로 저장하고, 에이전트가 그 원본에서 다음 작업에 쓸 만한 **기억(Memory)** 을 만듭니다.
모든 데이터는 로컬 SQLite 파일에 저장됩니다.

## 한눈에 보는 흐름

1. **원본 가져오기**: `gieok memory import`가 Codex / Claude Code 세션 파일을 Source로 저장합니다.
2. **기억 만들기**: `gieok memory ingest`가 저장된 Source를 에이전트에게 읽혀 Memory를 만듭니다.
3. **기억 떠올리기**: `gieok memory recall`이 지금 작업에 맞는 Memory를 현재 작업공간 Scope 안에서 찾습니다.
4. **에이전트에 연결하기**: `gieok mcp`가 Claude Code 같은 MCP 클라이언트에 `recall`, `status`, `get` 툴을 노출합니다.

## 핵심 단어

- **Source (원본)**: 아직 해석하지 않은 세션 파일입니다. 예: Codex / Claude Code JSONL 대화 기록.
- **Memory (기억)**: Source에서 뽑아낸 재사용 가능한 지식입니다. 단순 복사나 긴 요약이 아니라, 다음 에이전트가 바로 참고할 수 있는 내용이어야 합니다.
- **Scope (범위)**: 기억이 적용되는 작업공간입니다. 기본값은 현재 디렉터리입니다.
- **Recall (떠올리기)**: Scope 안에서 지금 작업에 맞는 Memory를 찾는 동작입니다.

## 개발 검증

프로젝트 툴 버전은 `mise.toml`에 고정합니다.

```sh
mise install
mise run verify
```

`verify`는 CI와 같은 기준으로 `gofmt`, `go mod tidy`, `golangci-lint run`,
`go test ./...`, `git diff --check`를 실행합니다. Go 파일만 빠르게 정리하려면
`mise run fmt`를 씁니다.

## 현재 구현된 기능

- **`gieok memory import --from <path> --store <db>`**
  - Codex / Claude Code 세션 JSONL 파일(또는 디렉터리)을 파싱해 Source로 저장합니다.
  - 지원하지 않는 파일은 건너뜁니다.
- **`gieok memory ingest [--agent <name=cmd[,arg...]|claude|codex|pi>] [--store <db>]`**
  - 저장된 Source마다 설정된 에이전트(기본: claude, codex, pi)를 병렬 실행해 Memory를 만들고 Source와 연결합니다.
  - `--agent`는 `name=command[,arg...]`로 커스텀 커맨드를 지정하거나, `claude`/`codex`/`pi`처럼 이름만 줘서 해당 기본 에이전트를 인자까지 그대로 쓸 수 있습니다(콤마가 든 인자는 `name=command[,arg...]` 문법으로 표현할 수 없어 필요). 옵션을 생략하면 세 기본 에이전트를 모두 실행합니다.
  - 옵션: `--limit`, `--source-id`, `--concurrency`(동시 실행 상한), `--skip-existing`(이미 만든 (source, agent) 기억 건너뛰기, 재개용).
  - 동일 세션을 재수집해도 에이전트별로 이전 Memory를 원자적으로 교체해 중복이 쌓이지 않습니다.
  - 에이전트를 실행하기 전, 같은 Scope 안에서 그 Source와 관련된 기존 Memory를 먼저 떠올려 프롬프트에 함께 넣습니다(자기 자신의 Source에서 나온 Memory는 제외). 그래서 에이전트는 매 Source를 고립된 채로 요약하는 대신, 이미 아는 것과 연결·확장·갱신하는 방식으로 Memory를 만듭니다. Memory는 세션 텍스트를 그대로 옮긴 것이 아니라 에이전트가 1차로 정제한 결과여야 합니다.
- **`gieok memory recall [task] [--scope <value>] [--all-scopes] [--limit <n>] [--json] [--store <db>]`**
  - 현재 Scope 안에서 task에 관련된 Memory를 다시 떠올립니다. Recall은 원본 세션 파일을 뒤지는 검색이 아니라 만들어 둔 Memory를 재사용하는 동작입니다.
  - task 텍스트는 positional 또는 `--task`로 전달합니다. 관련 랭킹은 하이브리드 recall입니다: 어휘(한국어 형태소 + FTS5/BM25, ADR 0004)와 의미(로컬 임베딩 코사인, ADR 0005) 두 랭킹을 Reciprocal Rank Fusion(RRF)으로 융합합니다(ADR 0006). 로컬 임베더(Ollama)가 없으면 의미 랭킹이 비어 어휘 전용으로 강등됩니다. task를 생략하면 Scope 안의 최근 Memory를 반환합니다.
  - Scope는 기본적으로 현재 작업 디렉터리에서 파생됩니다. `--scope`로 다른 워크스페이스를, `--all-scopes`로 전체 Scope를 조회합니다.
  - 기본 출력은 사람이 읽는 형식(Memory ID·agent·kind·시각·본문·Source 컨텍스트)입니다. `--json`은 MCP `recall` 툴과 공유하는 구조화 결과 모델을 냅니다.
- **`gieok mcp [serve] [--store <db>]`**
  - 표준 입출력(stdio) 위에서 도는 MCP 서버를 실행합니다. Claude Code 같은 MCP 클라이언트가 붙어 Memory를 떠올릴 수 있습니다. 노출 툴: `recall`, `status`, `get`.
  - `recall` 툴: `query`(필수), `scope`(선택), `limit`(선택)을 받아 CLI recall과 동일한 경로(`memory.Recaller.Recall`, 어휘+의미 RRF 하이브리드)를 통과시켜 랭킹된 Memory를 구조화 결과로 반환합니다.
  - 각 결과는 `memory_id`, `agent`, `kind`, `text`, `created`와, Memory가 파생된 여러 Source를 담는 `sources` 배열(각 항목 `id` / `uri` / `scope{kind,value}`)을 담습니다. CLI recall의 `--json` 모델과 같은 구조입니다.
  - `status` 툴: 입력 없이 recall 인덱스 건강도를 반환합니다 — `memories`(저장된 Memory 수), `vectors`(임베딩 벡터 수), `fts_rows`(전문 인덱스 행 수). 벡터/FTS 수가 Memory 수와 크게 벌어지면 백필이 필요하다는 신호입니다.
  - `get` 툴: `memory_id`(필수)로 Memory 하나를 조회해 recall과 동일한 Memory 구조(`memory_id`, `agent`, `kind`, `text`, `created`, `sources[]`)로 반환합니다. 해당 id가 없으면 `found=false`와 설명 `message`를 냅니다.
기본 저장 위치: `~/.local/share/gieok/memory/gieok.db`

## 세션 자동 캡처

Claude Code / Codex 세션은 이미 `~/.claude/projects/*.jsonl`, `~/.codex/sessions`
등에 쌓입니다. 두 단계로 나눠 자동 캡처합니다:

- **Import (즉시, 세션 종료 시)**: Claude Code `SessionEnd` 훅이 방금 끝난 세션 하나만
  `gieok memory import --from <session-jsonl>`로 Source로 저장합니다. 실패해도
  조용히 넘어가므로 세션 종료를 막지 않습니다.
- **Ingest (예약 실행)**: 매일 정오에 예약 작업(cron)이 Codex 세션 디렉터리를 통째로 import한 뒤,
  `--skip-existing`으로 아직 Memory가 없는 Source만 `claude` 에이전트로 ingest합니다.

```sh
gieok memory import --from ~/.codex/sessions
gieok memory ingest --skip-existing --agent claude
```

배치 방식이라 대화 중 매 턴마다 추가 비용이 들지 않고 세션당 중복 Source도 생기지 않습니다.
(과거의 `gieok hook` Stop 훅은 매 턴마다 커지는 대화 기록의 부분 스냅샷을
Source로 중복 생성해 제거했습니다. 세션은 이미 디스크에 있으므로 예약 작업이
한 번에 훑는 방식으로 충분합니다.)

## 로드맵 (미구현)

- **기억 그래프**: 현재는 Source→Memory 연결만 저장합니다. Memory 간 연결은 스키마 설계가 필요한 후속 작업입니다.
- **에이전트 실행 루프**: 기억을 만들고 평가하는 반복 실행을 보이게 하는 도구 및 내장 루프 에이전트.

세션을 대화 중 직접 기록하는 MCP `record` 툴은 도입하지 않습니다. 세션 자체가 Source이고, `import`와 `ingest`가 각각 원본 저장과 기억 만들기를 맡습니다.
