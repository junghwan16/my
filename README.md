# my

`my`는 코딩 에이전트 세션을 장기 기억으로 축적하기 위한 로컬 CLI입니다.

## 현재 구현된 기능

- **`my memory import --from <path> --store <db>`**
  - Codex / Claude Code 세션 JSONL 파일(또는 디렉터리)을 파싱해 SQLite에 정규화 저장합니다.
  - 지원하지 않는 파일은 건너뜁니다.
- **`my memory ingest [--agent name=cmd[,arg...]] [--store <db>]`**
  - 저장된 세션마다 설정된 에이전트(기본: claude, codex, pi)를 병렬 실행해 Memory를 생성·링크합니다.
  - 옵션: `--limit`, `--source-id`, `--concurrency`(동시 실행 상한), `--skip-existing`(이미 수집한 (source, agent) 건너뛰기, 재개용).
  - 동일 세션을 재수집해도 에이전트별로 이전 Memory를 원자적으로 교체해 중복이 쌓이지 않습니다.
- **`my memory recall [task] [--scope <value>] [--all-scopes] [--limit <n>] [--json] [--store <db>]`**
  - 현재 Scope 안에서 task에 관련된 Memory를 다시 떠올립니다. Recall은 raw 세션 검색이 아니라 ingest된 Memory를 재사용하는 도메인 액션입니다.
  - task 텍스트는 positional 또는 `--task`로 전달합니다. 관련 랭킹은 한국어 형태소 토큰화 + FTS5(BM25)입니다(ADR 0004). task를 생략하면 Scope 안의 최근 Memory를 반환합니다.
  - Scope는 기본적으로 현재 작업 디렉터리에서 파생됩니다. `--scope`로 다른 워크스페이스를, `--all-scopes`로 전체 Scope를 조회합니다.
  - 기본 출력은 사람이 읽는 형식(Memory ID·agent·kind·시각·본문·Source 컨텍스트)입니다. `--json`은 future MCP `memory.recall`과 공유하는 구조화 결과 모델을 냅니다.

기본 저장 위치: `~/.local/share/my/memory/my.db`

## 로드맵 (미구현)

- **Graph 메모리**: 현재는 source→Memory 링크만 저장합니다. Memory 간 그래프 링크는 스키마 설계가 필요한 후속 작업입니다.
- **MCP 서버**(`internal/mcp`): LLM 에이전트가 메모리를 조회·기록하는 인터페이스. 현재 placeholder.
- **Claude Code hooks**를 통한 세션 자동 ingest.
- **Agent Loop**: 루프 엔지니어링 가시성 도구 및 내장 루프 에이전트.
