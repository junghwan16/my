---
status: accepted
---

# Source와 Memory를 별도 패키지로 나눈다

CONTEXT.md에서 **Source(원본)** 는 아직 해석하지 않은 세션 파일이고,
**Memory(기억)** 는 그 원본에서 뽑아낸 재사용 가능한 지식이다. 둘은 역할이 다르다.
초기 구현은 둘을 `internal/memory` 한 패키지에 담았는데, 파일이 늘면서
"원본을 다루는 코드인지, 기억을 다루는 코드인지"가 흐려졌다. `import`(원본 저장)와
`ingest`/`recall`(기억 만들기와 떠올리기)도 한 곳에 섞였다.

## 결정

두 패키지로 나눈다. 의존은 한 방향만 허용한다:
`memories` 패키지는 `sources` 패키지를 읽을 수 있지만, `sources` 패키지는 `memories`를 모른다.
기억은 원본에서 나오지만, 원본은 기억 없이도 존재하기 때문이다.

```text
internal/
  source/     # package sources: 세션 파일을 Source로 저장하고 읽는다
    source.go     Source, SourceEvent, Scope, SourceID/Kind, ScopeKind
    importer.go   Importer(Import/Read) + 공용 세션 파싱 헬퍼
    codex.go / claude.go   포맷별 어댑터
    store.go / schema.go   sources, source_events 테이블
  memory/     # package memories: Source에서 Memory를 만들고 떠올린다
    memory.go     Memory, Link, MemoryID/Kind, LinkKind
    ingester.go   Ingester(Ingest) — Source를 읽어 Memory를 만든다
    recaller.go   Recaller(Recall) — Memory를 찾고 Source 증거를 붙인다
    agents.go     기본 ingest 에이전트 구성 + spec 파싱(CLI 어댑터)
    command_agent.go   외부 프로세스 에이전트
    store.go / schema.go   memories, memory_links 테이블
  jsonutil/   # 두 스토어가 공유하는 JSON 헬퍼
  storage/    # sqlite 연결/드라이버
```

각 역할은 이름이 드러나는 타입으로 노출한다: `sources.Importer`,
`memories.Ingester`, `memories.Recaller`. 인터페이스(`memories.SourceReader`,
`memories.MemoryWriter`, `memories.MemoryReader`)는 실제로 쓰는 패키지에 둔다.
그래야 "이 코드가 무엇을 필요로 하는지"를 호출부 근처에서 바로 볼 수 있다.

## Consequences

- **스키마 분리**: `sources` 는 `sources`/`source_events`, `memories` 는
  `memories`/`memory_links` 를 소유한다. Memory 스토어는 `sources` 테이블을
  직접 조회하지 않고 `source_id` 값만 쓰므로 스토어가 깨끗이 갈린다.
- **마이그레이션 소유권**: FK 순서와 schema 진화는 `internal/migrate` 의 GORM schema
  apply가 소유한다. 도메인 패키지는 row 모델과 쿼리만 둔다.
- **CLI 어댑터 이동**: 기본 에이전트 구성과 `name=cmd` 형식 파싱은 도메인
  지식이므로 `main.go` 에서 `memories.DefaultAgents` / `memories.ParseAgentSpec`
  으로 옮겼다. `main` 은 플래그 파싱과 배선만 담당한다.
- **공유 JSON 헬퍼**는 `internal/jsonutil` 로 추출해 두 패키지의 중복을 없앤다.

## Considered options

- **두 패키지로 분리 (선택)** — Source와 Memory의 역할이 코드 위치와 1:1로 맞는다.
  찾기 쉽고, 코드가 적은 지금이 이동 비용이 가장 싸다.
- **한 패키지 + 역할 인터페이스만 (거절)** — 파일만 정리하고 패키지 경계는
  두지 않는 안. 경계 비용은 없지만 source/memory 가 계속 한 의존 그래프에
  묶여, 개념이 섞이는 근본 문제가 남는다.
