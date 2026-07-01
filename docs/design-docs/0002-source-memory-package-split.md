---
status: accepted
---

# Source 와 Memory 를 별도 패키지로 나누고 역할 타입으로 노출한다

CONTEXT.md 의 ubiquitous language 에서 **Source**(불변 원본)와 **Memory**(원본에서
파생된 큐레이션 지식)는 서로 다른 개념이다. 초기 구현은 둘을 `internal/memory`
한 패키지에 담았는데, 파일이 늘면서 "source 를 찾을지 memory 를 찾을지" 가
모호해지고, import(원본 적재)와 ingest/recall(지식 생성·조회)이 한 곳에 섞였다.

## 결정

두 개의 bounded context 로 패키지를 나눈다. 의존은 단방향이다 —
`memory` 가 `source` 를 import 하고, 역방향은 없다(ingest 가 source 를 읽어
memory 를 만들기 때문).

```text
internal/
  source/     # 불변 원본 context
    source.go     Source, SourceEvent, Scope, SourceID/Kind, ScopeKind
    importer.go   Importer(Import/Read) + 공용 세션 파싱 헬퍼
    codex.go / claude.go   포맷별 어댑터
    store.go / schema.go   sources, source_events 테이블
  memory/     # 큐레이션 지식 context
    memory.go     Memory, Link, MemoryID/Kind, LinkKind
    ingester.go   Ingester(Ingest) — source.Reader/MemoryWriter 소비
    recaller.go   Recaller(Recall) — MemoryReader 소비
    agents.go     기본 ingest 에이전트 구성 + spec 파싱(CLI 어댑터)
    command_agent.go   외부 프로세스 에이전트
    store.go / schema.go   memories, memory_links 테이블
  jsonutil/   # 두 스토어가 공유하는 JSON 헬퍼
  storage/    # sqlite 연결/드라이버
```

각 도메인 capability 를 **역할 타입**으로 노출한다: `source.Importer`,
`memory.Ingester`, `memory.Recaller`. 소비 측 인터페이스(`memory.SourceReader`,
`memory.MemoryWriter`, `memory.MemoryReader`)는 consumer 패키지에 두어
Go 관례(인터페이스는 사용하는 쪽에서 정의)를 따른다.

## Consequences

- **스키마 분리**: `source` 는 `sources`/`source_events`, `memory` 는
  `memories`/`memory_links` 를 소유한다. `memory` 스토어는 `sources` 테이블을
  직접 조회하지 않고 `source_id` 값만 쓰므로 스토어가 깨끗이 갈린다.
- **마이그레이션 순서**: `memory_links.source_id` 가 `sources(id)` 를 참조하므로
  호출자는 `source.Migrate` 를 먼저, `memory.Migrate` 를 나중에 실행한다.
  `cmd/gieok` 의 `withStores` 가 이 순서를 보장한다.
- **CLI 어댑터 이동**: 기본 에이전트 구성과 `name=cmd` spec 파싱은 도메인
  지식이므로 `main.go` 에서 `memory.DefaultAgents` / `memory.ParseAgentSpec`
  으로 옮겼다. `main` 은 플래그 파싱과 배선만 담당한다.
- **공유 JSON 헬퍼**는 `internal/jsonutil` 로 추출해 두 패키지의 중복을 없앤다.

## Considered options

- **두 패키지로 분리 (선택)** — 도메인 모델과 1:1. findability 가 가장 좋고,
  코드가 적은 지금이 이동 비용이 가장 싸다.
- **한 패키지 + 역할 인터페이스만 (거절)** — 파일만 정리하고 패키지 경계는
  두지 않는 안. 경계 비용은 없지만 source/memory 가 계속 한 import 그래프에
  묶여, 개념이 섞이는 근본 문제가 남는다.
