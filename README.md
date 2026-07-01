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

기본 저장 위치: `~/.local/share/my/memory/my.db`

## 로드맵 (미구현)

- **Graph 메모리**: 현재는 source→Memory 링크만 저장합니다. Memory 간 그래프 링크는 스키마 설계가 필요한 후속 작업입니다.
- **MCP 서버**(`internal/mcp`): LLM 에이전트가 메모리를 조회·기록하는 인터페이스. 현재 placeholder.
- **Claude Code hooks**를 통한 세션 자동 ingest.
- **Agent Loop**: 루프 엔지니어링 가시성 도구 및 내장 루프 에이전트.
