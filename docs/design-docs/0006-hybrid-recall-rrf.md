---
status: accepted
---

# 하이브리드 recall 은 어휘·의미 랭킹을 Reciprocal Rank Fusion(RRF)으로 융합한다

Recall(= Scope 안에서 관련 Memory 찾기)에는 어휘 검색(ADR-0004, 형태소+FTS5/BM25)과
의미 검색(ADR-0005, bge-m3 dense 코사인)이라는 서로 보완적인 두 랭커가 있다. 실제
218개 세션 dogfooding 에서 두 랭커의 top-5 겹침이 0~2/5 에 불과했다 — 같은 질의에
대해 서로 다른 Memory 를 떠올린다. 이 둘을 하나의 하이브리드 랭킹으로 합쳐 recall
품질을 끌어올리되, 호출자(CLI/MCP)의 계약은 바꾸지 않아야 한다.

## 결정

**어휘 랭킹과 의미 랭킹을 Reciprocal Rank Fusion(RRF, k=60)으로 융합해, 공유 recall
seam `Recaller.Recollect` 이 단일 하이브리드 랭킹을 반환하게 한다. 의미 엔진이 있으면
자동으로 하이브리드, 없으면 어휘 전용으로 강등한다.**

- **RRF 융합**: Memory ID 기준으로 `score(m) = Σ 1/(k + rank_i(m))` 를 각 랭킹
  리스트(어휘 `SearchMemories`, 의미 `SearchSemantic`)에서 m 이 등장하는 위치의
  1-based rank 로 합산한다. 한 랭커에서 강하고 다른 랭커에서 약한 Memory 도 위로
  떠올라, 융합이 단독 랭커보다 낫다. k=60 은 Cormack et al.(2009)의 표준값으로,
  단일 랭커의 최상위 hit 가 융합을 지배하지 못하게 눌러 두 엔진의 보완 효과를
  살린다.
- **하이브리드 seam**: 새 `Store.HybridRecollections` 가 `SearchMemories` 와
  `SearchSemantic` 을 각각 넉넉히(overfetch=50) 호출해 융합 풀을 만들고, RRF 로
  랭킹한 뒤 limit 로 자른다. Source 컨텍스트는 기존 `attachSources` 를 그대로 재사용해
  결과를 `[]Recollection` 으로 맞춘다. `Recollect` 는 task 가 있을 때 이 메서드를
  호출한다 — 시그니처·반환 shape 불변이라 CLI(`my memory recall`)와 MCP `recall`
  툴이 호출부 수정 없이 개선된 랭킹만 본다.
- **graceful 강등**: 임베더가 없으면(Ollama 미가용) `SearchSemantic` 이 빈 리스트를
  반환하므로(ADR-0005 계약), 융합은 어휘 랭킹 하나만 남아 사실상 어휘 전용 순서로
  동작한다 — 에러도, 계약 변경도 없다. 완전 오프라인 기본 빌드가 그대로 유지된다.
- **결정론적 tie-break**: 융합 점수가 같으면 최신 `CreatedAt`, 그다음 `MemoryID`
  오름차순으로 깨뜨린다(의미 랭커의 tie-break 와 동일). 같은 입력에 항상 같은 순서.
- **빈 task 경로 불변**: task 가 비면 여전히 `RecentRecollections`(Scope 안 최신
  Memory)를 반환한다 — 하이브리드는 task recall 에만 얹힌다.
- **파생 규칙 미변경**: `SearchMemories`·`SearchSemantic` 은 호출만 하고 내부는
  건드리지 않는다(#9 병렬 작업이 `SearchSemantic` 내부를 수정하므로 호출 지점만
  안정적으로 유지).

## 근거

- **왜 RRF**: 점수 정규화 없이 rank 만으로 서로 다른 스케일(BM25 점수 vs 코사인
  유사도)의 두 랭킹을 합칠 수 있다. 튜닝 파라미터가 k 하나뿐이고, dense+sparse
  하이브리드 검색의 사실상 표준이다. dogfooding 의 낮은 겹침(0~2/5)이 딱 RRF 가
  강한 상황 — 보완적 랭킹의 융합.
- **왜 seam 뒤에서**: `Recollect` 가 CLI·MCP 의 단일 진입점이므로, 융합을 이 안에
  넣으면 두 surface 가 자동으로 개선된다. 계약(시그니처·`[]Recollection` shape)이
  그대로라 호출부 수정이 0.
- **왜 의미 있을 때만 하이브리드**: 임베더는 옵션(ADR-0005)이다. 있으면 자동으로
  하이브리드, 없으면 빈 의미 리스트로 어휘 전용 강등 — 별도 플래그·분기 없이 같은
  코드 경로가 두 경우를 모두 처리한다.

## Consequences

- 의미 엔진이 붙은 환경에서는 recall 결과에 어휘상 매칭되지 않지만 의미상 가까운
  Memory 도 포함될 수 있다. 이는 하이브리드의 의도된 동작이다. Scope 안에 Memory 가
  전혀 없으면 두 랭커 모두 비어 "no memory recalled" 가 그대로 유지된다.
- 융합 풀을 위해 각 랭커를 limit 보다 넓게(overfetch) 호출하므로, 의미 검색이 있는
  경우 후보 벡터 로드가 limit 이 아니라 overfetch 크기만큼 일어난다. 로컬 규모
  (수천~수만 Memory)에서 수용 가능하다.
- 새 의존성 없음. 순수 Go, cgo-free 유지.

## Considered options

- **RRF 융합 (선택)** — 점수 정규화 불필요, 파라미터 1개, dense+sparse 표준,
  낮은 겹침 상황에 최적.
- **점수 가중 합(weighted score sum) (거절)** — BM25 와 코사인의 스케일이 달라
  정규화가 필요하고, 가중치·정규화 방식이 데이터셋마다 튜닝 대상이 된다.
- **어휘 우선 + 의미 fallback (거절)** — 어휘가 결과를 내면 의미를 안 쓰므로,
  dogfooding 이 보여준 보완 효과(의미만 떠올리는 Memory)를 놓친다.
- **의미 전용 전환 (거절)** — 어휘의 정확한 형태소 매칭(ADR-0004)을 버리게 되고,
  임베더 없는 오프라인 기본 빌드에서 recall 이 통째로 죽는다.
