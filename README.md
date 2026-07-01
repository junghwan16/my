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
  - task 텍스트는 positional 또는 `--task`로 전달합니다. 관련 랭킹은 하이브리드 recall입니다: 어휘(한국어 형태소 + FTS5/BM25, ADR 0004)와 의미(로컬 임베딩 코사인, ADR 0005) 두 랭킹을 Reciprocal Rank Fusion(RRF)으로 융합합니다(ADR 0006). 로컬 임베더(Ollama)가 없으면 의미 랭킹이 비어 어휘 전용으로 매끄럽게 강등됩니다. task를 생략하면 Scope 안의 최근 Memory를 반환합니다.
  - Scope는 기본적으로 현재 작업 디렉터리에서 파생됩니다. `--scope`로 다른 워크스페이스를, `--all-scopes`로 전체 Scope를 조회합니다.
  - 기본 출력은 사람이 읽는 형식(Memory ID·agent·kind·시각·본문·Source 컨텍스트)입니다. `--json`은 MCP `recall` 툴과 공유하는 구조화 결과 모델을 냅니다.
- **`my mcp [serve] [--store <db>]`**
  - stdio 위에서 도는 MCP 서버를 실행합니다. Claude Code 같은 MCP 클라이언트가 붙어 메모리를 Recall 할 수 있습니다.
  - `recall` 툴: `query`(필수), `scope`(선택), `limit`(선택)을 받아 CLI recall과 동일한 seam(`memory.Recaller.Recollect`, 어휘+의미 RRF 하이브리드)을 통과시켜 랭킹된 Memory를 구조화 결과로 반환합니다.
  - 각 결과는 `memory_id`, `agent`, `kind`, `text`, `created`와, Memory가 파생된 여러 Source를 담는 `sources` 배열(각 항목 `id` / `uri` / `scope{kind,value}`)을 담습니다. CLI recall의 `--json` 모델과 동일한 shape입니다.
- **`my hook [--store <db>]`**
  - Claude Code hook 페이로드를 stdin(JSON)으로 받아 `transcript_path` 세션을 자동으로 import 합니다. 끝난 세션이 곧 recall 가능한 Source가 됩니다.
  - fail-soft: 페이로드·세션에 문제가 있어도 세션을 깨뜨리지 않도록 stderr에 알리고 종료 코드 0으로 끝납니다. LLM 단계인 ingest는 hook에서 하지 않고 `my memory ingest` 배치에 맡겨 hook을 빠르게 유지합니다.
  - `~/.claude/settings.json`의 Stop hook으로 연결합니다:
    ```json
    { "hooks": { "Stop": [ { "hooks": [ { "type": "command", "command": "my hook" } ] } ] } }
    ```

기본 저장 위치: `~/.local/share/my/memory/my.db`

## 로드맵 (미구현)

- **Graph 메모리**: 현재는 source→Memory 링크만 저장합니다. Memory 간 그래프 링크는 스키마 설계가 필요한 후속 작업입니다.
- **Agent Loop**: 루프 엔지니어링 가시성 도구 및 내장 루프 에이전트.

세션을 대화 중 직접 기록하는 MCP `record` 툴은 도입하지 않습니다 — 세션 자체가 Source이고, hook/ingest로 인제스트되는 것이 곧 기록입니다(참조 시스템 seCall과 동일).
