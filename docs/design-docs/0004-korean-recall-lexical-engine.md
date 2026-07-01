---
status: accepted
---

# 한국어 recall 어휘 검색은 앱단 형태소 토큰화 + FTS5 로 한다

Recall(= Scope 안에서 관련 Memory 찾기)의 어휘 검색을 어떻게 구현할지 결정한다.
Memory 텍스트는 대부분 한국어(한/영 혼합)이고, 검색 정확도(재현율)가 최우선이다.

## 결정

**Go 애플리케이션 계층에서 한국어 형태소로 토큰화한 뒤, 공백으로 이어 붙여 FTS5
(`unicode61`) 인덱스에 저장하고 BM25 로 랭킹한다.** 색인과 질의에 동일한
`Tokenizer` 를 쓴다.

- 형태소 분석기는 `memory.Tokenizer` 인터페이스 뒤에 둔다(기존 `Agent`/`runner`
  주입형 어댑터 패턴과 동형). 기본 구현은 순수 Go 인 **Kagome v2 + kagome-dict-ko**
  (`internal/tokenize`), cgo 없음.
- 새 goose 마이그레이션 `00003` 이 `memories_fts` FTS5 가상 테이블을 만든다.
  토큰화는 Go 에서 하므로 SQL 마이그레이션은 빈 테이블만 만들고, 앱단 reindex 가
  기존 Memory 를 백필한다.
- 쓰기 경로(`ReplaceSourceMemories`)가 Memory 저장과 같은 트랜잭션에서 FTS 를
  동기화한다. Scope 필터는 `memories_fts → memory_links → sources.scope_value`
  조인으로 구현한다.
- 재사용 가능한 검색 seam 은 `memory.Recaller.Search(query, scope, limit)` 이며,
  CLI recall 명령(#1)과 이후 MCP 가 이를 공유한다.

## 근거

- FTS5 `trigram` 은 **3글자 미만 질의를 구조적으로 매칭하지 못한다**(SQLite 공식).
  한국어는 2음절 명사가 흔해(`종목`, `순매`) 재현율 손해가 크다.
- FTS5 커스텀 토크나이저는 **C 레벨 전용**이라 pure-Go 인 `modernc.org/sqlite`
  로는 등록할 수 없다. 앱단 토큰화는 SQLite 토크나이저 API 를 건드리지 않으므로
  **modernc 유지**가 가능하다(mattn/go-sqlite3 cgo 전환 불필요).
- 형태소 분석은 조사·어미를 분리해(`종목을` → `종목`+`을`) bare noun 질의가
  굴절된 본문을 매칭하게 한다. 회귀 테스트가 실제 Kagome 로 이를 고정한다.
- 딥리서치(25 소스, 23 검증 주장)로 위 세 가지를 확인했고, FTS5 가 modernc 에서
  동작함을 실측했다.

## Consequences

- 신규 의존성: 순수 Go `github.com/ikawaha/kagome/v2`, `kagome-dict-ko`
  (Apache-2.0). 단일 바이너리·cgo-free 유지.
- **유지보수 리스크**: kagome-dict-ko 는 Experimental 등급. 사전 버전을 고정하고,
  더 높은 정확도가 필요하면 Tokenizer 를 교체(cgo Kiwi 등)한다 — 계약은 불변.
- 의미 검색(임베딩)·하이브리드(BM25+dense RRF)·sqlite-vec 는 같은 `Search` 계약
  뒤에서 additive 하게 얹는다(로드맵). 이 ADR 은 어휘 층만 확정한다.

## Considered options

- **앱단 형태소 + FTS5 (선택)** — modernc 유지, cgo 0, 한국어 재현율 확보.
- **FTS5 trigram (거절)** — 2음절 한국어 질의 구조적 미스.
- **mattn/go-sqlite3 + C 커스텀 토크나이저 (거절)** — cgo 배포 복잡도, 지금 불필요.
- **벡터/임베딩 우선 (연기)** — 품질은 높지만 임베더가 병목. 어휘 층 이후 단계.
