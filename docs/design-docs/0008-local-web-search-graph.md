---
status: accepted
---

# 검색과 provenance 그래프를 로컬 web 서버로 노출한다

gieok 는 CLI(`import`/`ingest`/`recall`)와 MCP(`recall`/`status`/`get`)는 있지만
사람이 직접 훑어보는 검색·탐색 표면이 없다. 기본적인 검색과, "얼마나 많은 Source 가
Memory 로 녹아있는지" 를 보여주는 그래프뷰어가 필요하다. 그래프 밀도 시각화는
브라우저급 렌더링이 유리한데, 프로젝트는 오프라인 우선의 순수 Go CLI 다 — 어느 표면에
올릴지는 되돌리기 어려운 선택이다.

## 결정

**`gieok web [--store <db>] [--addr <host:port>]` 서브커맨드를 추가한다. 127.0.0.1 에
바인딩된 Go HTTP 서버가 go:embed 로 묶은 정적 HTML/JS 와 JSON API 를 서빙하고, 검색과
그래프를 모두 브라우저에서 렌더링한다.**

- **검색**: `/api/recall` 은 CLI·MCP 와 동일한 공유 seam `Recaller.Recall`
  (하이브리드 RRF, ADR-0006)를 그대로 통과시킨다. UI 는 텍스트 박스 + 결과 리스트,
  빈 질의면 최근 Memory. scope 선택 드롭다운(DB 의 scope 목록)으로 워크스페이스 전환.
- **그래프**: `/api/graph` 는 선택 scope 로 제한된 nodes/edges 를 낸다 — Source·Memory
  노드, Link(Source→Memory)·Relation(Memory→Memory) edge(ADR-0007). 노드는 상한(예
  500)으로 자르고, Memory 노드를 클릭하면 그 이웃(파생 Source + 연결 Memory)을 온디맨드로
  확장한다. 프론트는 vendoring 한 그래프 라이브러리(cytoscape/d3)로 렌더링한다.
- **"녹아있는 정도" 지표**: Source 노드 크기 = fan-out(그 Source 가 몇 개 Memory 로
  녹았는지), Memory 노드 크기 = Relation degree(그 Memory 가 몇 개 다른 Memory 와
  연결됐는지, ADR-0007). 집계 패널 = 총 Source·Memory 수와 평균 Source/Memory.

  주의: per-Memory Link fan-in 은 지표로 쓰지 않는다 — Memory ID 가 (source, agent,
  kind, text) 해시라 한 Memory 는 정확히 한 Source 에만 연결되어 fan-in 이 구조적으로
  항상 1 이기 때문이다. "여러 Source 가 녹아 연결되는" 그림은 per-Memory fan-in 이
  아니라 Source fan-out + Memory 간 Relation 그래프 + 집계 수치가 담당한다.

## 근거

- **왜 web, TUI 가 아니라**: 어려운 절반은 그래프 밀도 시각화이고 브라우저가 그 자연스러운
  집이다. 검색은 같은 서버에 사소하게 얹힌다. TUI 그래프는 ASCII/braille 로 제한돼
  "소스가 녹아있는" 밀도를 표현하지 못한다.
- **왜 Recaller seam 재사용**: recall 계약을 CLI·MCP·web 한 지점(`Recall`)에
  모으면 세 표면이 같은 랭킹을 자동으로 공유한다 — 검색 로직 중복 0.
- **왜 scope 제한 + 노드 상한**: store 는 여러 scope 에 걸쳐 수만 Memory 를 담을 수
  있고, 브라우저 그래프는 노드 ~1-2천을 넘으면 못 쓴다. scope 개관 + 클릭 드릴다운이
  전체감과 사용성을 함께 준다.
- **왜 embed + vendoring**: 오프라인 단일 바이너리(프로젝트 ethos, 세만틱 recall 도
  Ollama 없으면 우아하게 강등)를 지키려면 정적 자산·그래프 라이브러리를 바이너리에
  묶어 CDN 의존을 없앤다.

## Consequences

- HTTP 서버와 정적 자산이 추가된다. 외부 런타임 서비스는 없고 localhost 바인딩이라
  네트워크 노출이 없다.
- `memory.Store` 에 그래프용 read-model 질의가 추가된다: scope 내 노드/edge, Memory 별
  Link fan-in, 집계 통계, scope 목록. recall 경로는 건드리지 않는다.
- 상한을 넘는 그래프는 전체 렌더가 아니라 드릴다운으로 다룬다 — 전역 조망은 집계
  패널의 수치가 담당한다.

## Considered options

- **로컬 web 서버 단일 (선택)** — 그래프 네이티브 렌더링, 검색은 같은 서버에 추가,
  표면 1개.
- **TUI 단일 (거절)** — 검색은 강하지만 그래프가 ASCII 로 제한돼 핵심 요구(밀도
  시각화)를 못 낸다.
- **분리: TUI 검색 + web 그래프 (거절)** — 각자 최적이지만 표면 2개로 유지보수 2배,
  공유 이득 대비 비용이 크다.
