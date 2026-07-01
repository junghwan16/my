---
status: accepted
---

# 에이전트가 저자하는 Memory↔Memory Relation 은 Link 와 별개 개념이다

ingest 중 에이전트에게는 이미 "관련 기존 Memory"(같은 Scope, 소스 샘플로 recall)가
프롬프트로 주어진다(ADR 없음, 코드로만 존재). 에이전트가 새 Memory 를 그 기존 Memory
위에 "이어 붙였다"는 사실을 저장할 곳이 없었다. CONTEXT.md 의 `Link` 는 이미
Source→Memory 증거 연결로 확정돼 있어 재사용할 수 없다 — 그것을 Memory↔Memory 로
확장하면 provenance recall 의 의미가 오염된다.

## 결정

**Memory 사이 관계는 `Relation` 이라는 별도 개념으로 만들고, 새 `memory_relations`
테이블에 저장한다. 에이전트가 `relates_to` 로 지목한 id 중, 그 소스에 대해 프롬프트에
실제로 보여 준 기존 Memory 집합(allowlist)에 든 것만 관계로 남긴다.**

- **용어 분리**: `Link` = Source→Memory(기존 `memory_links`, 불변), `Relation` =
  Memory↔Memory(신규 `memory_relations`). 순수 추가로, 기존 provenance recall 은
  건드리지 않는다.
- **스키마**: `memory_relations(from_memory_id, to_memory_id, kind, created_at,
  metadata_json)`, PK `(from_memory_id, to_memory_id, kind)`. 양 끝 모두
  `memories(id) ON DELETE CASCADE` — 어느 쪽 Memory 를 지워도 dangling relation 이
  남지 않는다. 방향은 항상 **새 Memory → 기존 Memory**.
- **단일 kind**: `kind = "relates"` 고정. 컬럼(과 PK 참여)은 후일 타입화용으로
  유지하되, 오늘은 값 하나만 쓴다 — 미래 관계 타입을 스키마 변경 없이 같은 쌍에
  공존시킬 수 있다.
- **출력 계약 확장**: 에이전트 JSON Memory 에 `relates_to []MemoryID` 를 파싱한다.
  평문 출력 경로는 관계 없음 그대로다.
- **allowlist 검증**: `relates_to` 는 그 소스에 대해 프롬프트에 보인 관련 Memory 의
  id 로만 허용한다. 그 외(에이전트가 지어낸 id, 자기 자신, 중복)는 조용히 drop —
  에이전트가 임의의 Memory 로 관계를 날조하지 못한다. FK 위반 대신 저장 이전 단계에서
  걸러 낸다.
- **원자적 교체**: (source, agent) 재수집 시 그 run 의 이전 Memory 삭제가
  `from_memory_id` CASCADE 로 그 run 이 저자한 관계까지 함께 지운다. 새 관계는 새
  Memory 가 존재한 뒤(같은 트랜잭션 안)에 삽입돼 양 끝 FK 를 만족한다 — 재수집해도
  관계가 누적되지 않는다.
- **프롬프트 갱신**: 관련 Memory 안내문에 "이어지는 기존 Memory 의 대괄호 id 를
  `relates_to` 로 지목하라, 위에 나열된 id 만 허용된다"를 추가한다. recall 랭킹·seam 은
  그대로 재사용한다.

## Considered options

- **ingest 중 agent 저자 (선택)** — 기존 recall-in-prompt 를 재사용, 추가 recall 비용
  0, agent 가 실제로 이어진다고 판단한 지식 관계만 저장.
- **벡터 유사도 kNN (거절)** — agent 변경 0 으로 즉시 그래프를 채우지만, 계산된
  근접도이지 저장된 지식이 아니고 threshold 튜닝·노이즈를 동반한다. Relation 은
  근접도가 아니라 지식 관계다.
- **스키마만 두고 생성은 나중 (거절)** — Relation 테이블과 렌더러만 두면 그래프의
  memory↔memory 가 당분간 빈 화면으로 남아, provenance/Relation 그래프뷰어(ADR-0008)의
  절반이 죽는다.

## Note

초안(b19bada 기준)은 Source→Memory 를 `Derivation` 으로 재명명하고 `Link` 를
Memory↔Memory 로 되돌리는 안이었다. 그러나 `main` 이 그 사이 CONTEXT.md 에서 `Link` 를
Source→Memory 로 확정했으므로, 코드 리네임 없이 Memory↔Memory 에 새 이름 `Relation` 을
쓰는 쪽으로 정렬했다. 결정의 실체(agent 저자·allowlist·단일 kind·원자적 교체)는 동일하다.
