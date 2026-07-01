---
status: accepted
---

# 재사용할 기억의 코드 타입명은 `Memory`, 패키지명은 `memories` 로 한다

CONTEXT.md 는 Source에서 뽑아낸 재사용 가능한 지식을 **Memory(기억)** 라고 부른다.
그래서 코드에서도 같은 단어를 쓴다. 타입명은 `Memory`, `MemoryID`, `MemoryKind` 를
유지한다.

다만 Go lint의 stuttering 규칙도 유지한다. 패키지명은 `memory` 가 아니라 `memories` 로
두어 바깥에서는 `memories.Memory`, `memories.MemoryID` 처럼 읽히게 한다. 같은 이유로
Source 패키지는 `sources` 로 둔다. 디렉터리 경로는 기존 import path를 유지하되, Go
패키지 이름만 plural로 둔다.

## Consequences

- `.golangci.yml` 에서 revive `exported`의 stuttering check를 끄지 않는다.
- 타입명은 `SourceID`/`SourceKind`/`ScopeKind`/`LinkKind` 와 동일한 `<Domain><Suffix>`
  패턴을 유지한다. 그래서 `MemoryID`/`MemoryKind` 이며, revive 가 제안하는 `ID`/`Kind` 로
  줄이지 않는다 — 그 축약은 오히려 기존 타입들과의 대칭을 깬다.
- import alias가 필요한 호출부는 `memoriespkg`/`sourcespkg` 처럼 명시해 로컬 store 변수와
  패키지 qualifier가 충돌하지 않게 한다.

## Considered options

- **`memories.Memory` (선택)** — 제품 언어를 유지하면서 revive stuttering check도 통과.
- **`memory.Memory` + lint disable (거절)** — 이름은 자연스럽지만 golangci 설정이 실제
  중복 이름을 숨긴다.
- **`memory.Item` (거절)** — 중복은 없지만 문서의 "Memory" 와 코드의 "Item" 이 어긋나
  독자·에이전트가 매번 번역해야 한다.
