---
status: accepted
---

# 사람의 Memory 편집은 원본을 덮지 않고 Override로 얹는다

web 표면에서 사람이 Memory 를 고칠 수 있으면 좋다. 그런데 Memory ID 는
`sha256(source, agent, kind, text)` 해시라 텍스트를 제자리에서 바꾸면 ID 가 바뀌고,
그 ID 를 참조하는 Link·Relation·벡터·FTS(전부 ON DELETE CASCADE)가 끊긴다. 게다가
도메인 원칙(CONTEXT.md)은 "원본은 고치지 않고 새 것으로 바로잡는다" 이다. 편집을
어떻게 저장할지는 되돌리기 어려운 선택이다.

## 결정

**사람의 편집은 원본 Memory 를 덮지 않고 nullable `memories.text_override` 컬럼에
Override 로 얹는다. Memory 의 해시 ID·Link·Relation·벡터는 그대로 두고, 읽기 경로는
effective text(`COALESCE(text_override, text)`)를 돌려준다. 빈 Override 는 컬럼을
NULL 로 지워 원본 텍스트로 복귀시킨다.**

- **쓰기**: web 전용 엔드포인트 `POST /api/memory/edit {memory_id, text}` 하나가
  `Store.SetMemoryOverride` 를 호출한다. CLI·MCP 는 편집을 노출하지 않는다.
- **읽기**: 모든 Memory 조회가 지나는 공유 컬럼 리스트(`memoryColumnsSQL`)와 그래프
  라벨 쿼리에 override 를 더해, RecallResult 는 effective text 와 `edited`·
  `original_text` 를, 그래프 노드는 effective 라벨을 낸다.
- **재수집 안전**: ingest 의 memory upsert 는 `text_override` 를 건드리지 않으므로,
  같은 Memory 를 다시 수집해도 사람의 Override 는 보존된다.
- **검색 인덱스**: Override 는 FTS/벡터를 재색인하지 않는다 — 랭킹은 에이전트의 원본
  텍스트 기준, 표시는 effective 텍스트다.

## 근거

- **왜 in-place 편집이 아닌가**: 텍스트가 ID 해시의 일부라 제자리 편집은 ID 를 바꿔
  provenance(Link·Relation·벡터)를 통째로 끊는다. Override 는 정체성과 증거를 보존한다.
- **왜 "새 교정 Memory" 가 아닌가**: 도메인 원칙과는 맞지만, 사람이 UI 에서 한 글자
  고칠 때마다 새 Memory + Relation 을 만드는 것은 그래프를 오염시키고 UX 가 무겁다.
  Override 는 원본을 보존하면서도 가볍게 되돌릴 수 있다.
- **왜 web 전용 쓰기**: 편집은 사람의 열람·교정 행위다. CLI·MCP 는 에이전트 파이프라인
  이라 편집 진입점을 두지 않아 읽기 계약을 단순하게 유지한다.

## Consequences

- `memories` 에 nullable `text_override` 컬럼이 생기고 schemaVersion 이 오른다(6→7).
  기존 스토어는 AutoMigrate 가 컬럼만 추가한다.
- web 표면이 더 이상 순수 읽기 전용이 아니다(ADR-0008 의 "네트워크 노출 없음"은 유지 —
  여전히 loopback 바인딩). 쓰기는 이 엔드포인트 하나로 국한된다.
- 랭킹과 표시가 갈린다: 편집된 Memory 는 원본 텍스트로 검색되지만 편집본으로 보인다.
  후속으로 재색인이 필요하면 별도 결정으로 다룬다.

## Considered options

- **비파괴 Override 컬럼 (선택)** — 정체성·provenance 보존, 가벼운 되돌리기, 재수집 안전.
- **in-place 텍스트 편집 (거절)** — ID 가 바뀌어 Link·Relation·벡터가 끊긴다.
- **편집을 새 교정 Memory + Relation 으로 (거절)** — 도메인상 자연스럽지만 그래프 오염과
  무거운 UX.
- **metadata_json 에 override 저장 (거절)** — 스키마 변경은 피하지만 모든 읽기 경로에서
  JSON 파싱이 필요하고 메타데이터를 과적재한다.
