---
status: accepted
---

# 영속 메모리 단위의 코드 타입명은 도메인 언어에 맞춰 `Memory` 로 한다

CONTEXT.md 의 ubiquitous language 는 curated unit 을 **Memory** 라고 부른다. 이 타입은
`memory` 패키지 안에 살기 때문에 외부에서 `memory.Memory` / `memory.MemoryID` 로 보이며
Google Go style guide 가 지적하는 package stutter 가 생긴다. 그럼에도 코드 심볼(`Memory`,
`MemoryID`, `MemoryKind`)을 glossary 와 1:1 로 맞추는 쪽을 택한다. 문서·이슈·코드가 같은
단어를 쓰는 편이, stutter 를 피하려고 `Item` 같은 별도 용어를 두는 것보다 탐색·이해 비용이 낮다.

## Consequences

- revive 의 `exported` stutter 검사는 이 컨벤션과 근본적으로 충돌하므로
  `.golangci.yml` 에서 `disableStutteringCheck` 로 끈다. (`Memory` 계열만이 아니라
  프로젝트 전반이 도메인 정렬 명명을 따르기로 한 결정의 일부다.)
- 타입명은 `SourceID`/`SourceKind`/`ScopeKind`/`LinkKind` 와 동일한 `<Domain><Suffix>`
  패턴을 유지한다. 그래서 `MemoryID`/`MemoryKind` 이며, revive 가 제안하는 `ID`/`Kind` 로
  줄이지 않는다 — 그 축약은 오히려 기존 타입들과의 대칭을 깬다.

## Considered options

- **`Memory` (선택)** — glossary 와 정렬. stutter 는 lint 설정으로 흡수.
- **`Item` 유지 (거절)** — stutter 는 없지만 문서의 "Memory" 와 코드의 "Item" 이 어긋나
  독자·에이전트가 매번 번역해야 한다.
