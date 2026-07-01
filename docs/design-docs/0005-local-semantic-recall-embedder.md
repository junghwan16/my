---
status: accepted
---

# 의미 recall 은 옵션형 로컬 임베더(bge-m3 via Ollama) + BLOB 브루트포스 코사인으로 한다

Recall(= Scope 안에서 관련 Memory 찾기)에 어휘 검색(ADR-0004)과 나란히 의미
검색을 얹는다. 어휘층은 형태소가 겹쳐야 매칭되므로, 표현이 다른 동의어·의역
질의를 놓친다. 이를 dense embedding 으로 보완하되, cgo-free·단일 바이너리·기본
100% 오프라인이라는 프로젝트 제약을 깨지 않아야 한다.

## 결정

**의미 recall 은 옵션형 로컬 임베더를 통해 제공한다.**

- **임베더 계약**: `memory.Embedder` (`Embed(ctx, text) ([]float32, error)`,
  `Model() string`) 를 도입한다. `memory.Tokenizer` 와 동형으로, 구체 모델을
  인터페이스 뒤에 두어 recall 계약을 바꾸지 않고 교체 가능하게 한다.
- **기본 어댑터**: `internal/embed` 의 순수 Go `Ollama` 가 로컬 Ollama HTTP
  API(`POST /api/embed`)를 호출한다. 모델은 **bge-m3**(dense, 1024-dim),
  다국어(한국어 포함) 성능이 좋고 M1 급에서 Metal 로 가볍게 돈다(~1.2GB).
  cgo 없음, SDK 없음, `net/http` 만 사용. base URL·모델은 옵션으로 설정 가능.
- **선택 기능 + 자동 강등**: `withStores` 가 임베더를 만들고
  `Available(ctx)`(실제 1회 embed)로 health-check 한다. Ollama 미가용/모델
  부재면 임베더를 붙이지 않고, 의미 recall 은 비활성 + 어휘(형태소+FTS5) recall
  이 그대로 동작한다. 임베더 없는 기본 빌드는 완전 오프라인이다.
- **벡터 저장**: GORM schema apply 가 `memory_vectors`
  (`memory_id` PK/FK, `model`, `dim`, `vector` BLOB) 를 만든다. 벡터는 Go 코드에서
  **little-endian float32 BLOB** 로 직렬화한다. `model`·`dim` 을 함께 기록해
  모델/차원 교체를 감지하고 오래된 벡터를 재임베드한다.
- **쓰기 동기화 + 백필**: 임베더가 있으면 `ReplaceSourceMemories` 쓰기 경로에서
  Memory 텍스트를 임베드해 벡터를 저장한다(FTS 동기화와 동형). 현재 모델의
  벡터가 없는 Memory 는 `EnsureVectorsIndexed` 가 반복 실행해도 안전하게 백필한다
  (`EnsureFTSIndexed` 와 동형). 개별 embed 실패는 건너뛰어 쓰기·recall 을 막지
  않는다(자동 강등이 계약).
- **의미 검색**: `Store.SearchSemantic` 이 질의를 임베드 → scope 내 후보 벡터
  로드(현재 모델만) → Go **브루트포스 코사인 유사도** 로 랭킹 → top-limit 반환.
  scope·limit 는 `SearchMemories` 와 동일하게 지킨다. `Recaller.SearchSemantic`
  으로 노출한다. 기존 어휘 `Recall`/`Search` 가 여전히 기본이며,
  하이브리드 융합(BM25+dense)은 별도 이슈(#6)에서 이 계약 위에 얹는다.

## 근거

- **왜 옵션형인가**: 임베더는 프로젝트의 "로컬 우선·오프라인·cgo-free" 제약과
  충돌하기 쉬운 무거운 의존성이다. 별도 로컬 서비스(Ollama)로 분리하고 자동 강등을 두면
  기본 빌드의 단순성·이식성을 유지하면서 원하는 사용자만 의미층을 켤 수 있다.
- **왜 Ollama HTTP**: pure-Go `net/http` 만으로 로컬 추론을 붙일 수 있어 cgo 나
  바인딩이 불필요하다. bge-m3 는 다국어·한국어 recall 에 적합하고 M1 부담이 작다.
- **왜 BLOB + 브루트포스 코사인 (지금 sqlite-vec/cgo 아님)**: `modernc.org/sqlite`
  는 pure-Go 라 확장(sqlite-vec 등)을 로드할 수 없고, 확장 도입은 cgo/배포
  복잡도를 부른다. 이 스토어는 개인 로컬 규모(수천~수만 Memory)이므로 float32
  BLOB 을 읽어 Go 에서 코사인을 전수 계산해도 충분히 빠르다. 스케일이 커지면
  같은 인터페이스 뒤에서 ANN 인덱스로 교체한다 — 계약은 불변.
- **왜 model+dim 기록**: 임베딩은 모델별로 비교 불가능하다. 모델 id 를 저장하고
  검색·백필을 현재 모델로 한정하면, 모델 교체 시 오래된 벡터가 조용히 섞이지 않고
  재임베드로 자연 교체된다.

## Consequences

- 신규 의존성 없음(표준 라이브러리 `net/http` 만). 단일 바이너리·cgo-free 유지.
- 의미 recall 을 쓰려면 로컬 Ollama + `ollama pull bge-m3` 가 필요하다(문서화).
  없으면 자동으로 어휘 전용으로 동작한다.
- 쓰기 시 embed 는 단일 SQLite 커넥션 트랜잭션 안에서 일어나므로 Ollama 지연이
  쓰기 지연으로 이어진다. 로컬 CLI·개인 규모에서는 수용 가능하며, 실패는
  건너뛰어 쓰기를 막지 않는다.
- `SearchSemantic` 은 `Recaller` 에 노출만 하고 기본 recall 경로는 어휘 그대로다.
  하이브리드 융합(#6)이 이 위에 덧붙는다.

## Considered options

- **옵션형 Ollama 임베더 + BLOB 브루트포스 (선택)** — cgo 0, 오프라인 기본,
  로컬 규모에 충분, 인터페이스 교체 가능.
- **sqlite-vec 확장 (거절/연기)** — pure-Go modernc 에서 로드 불가, cgo·배포
  복잡도. 스케일이 요구할 때 재검토.
- **원격 임베딩 API (거절)** — 로컬 우선·오프라인 원칙 위배, 네트워크·비용 의존.
- **임베더 필수화 (거절)** — 기본 빌드의 오프라인·단순성을 깨고, Ollama 없는
  사용자의 recall 을 통째로 막는다.
