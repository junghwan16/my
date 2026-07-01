---
status: accepted
---

# 스키마 마이그레이션은 GORM + 필요한 SQLite SQL + 자동 백업으로 관리한다

gieok은 사용자당 SQLite 파일 1개
(`~/.local/share/gieok/memory/gieok.db`)가 유일한 저장소다. 사용자가 별도
마이그레이션 명령을 실행하지 않고, `cmd/gieok` 이 DB를 열 때 `migrate.Apply` 를
자동 실행한다.

초기 구현은 Source/Memory 패키지가 각자 `CREATE TABLE IF NOT EXISTS` 를 직접
실행해서 버전, 순서, 기존 DB 진화를 추적하기 어려웠다. 이후 goose SQL 파일을
도입했지만, 실제 테이블 모델은 Go 코드에 이미 있고 SQL 파일은 같은 스키마를 한 번 더
표현했다.

## 결정

`internal/migrate` 가 **GORM 기반 schema apply** 를 소유한다.

- 일반 테이블(`sources`, `source_events`, `memories`, `memory_links`,
  `memory_vectors`, `schema_versions`)은 GORM 모델과 `AutoMigrate` 로 만든다.
- FTS5 가상 테이블(`memories_fts`)은 GORM 모델로 표현할 수 없으므로
  `CREATE VIRTUAL TABLE IF NOT EXISTS` SQL을 명시적으로 실행한다.
- schema 버전은 `schema_versions(name, version, updated_at)` 에 기록한다.
- 기존 goose DB는 `goose_db_version` 을 읽어 현재 버전을 추정한 뒤 GORM ledger로
  흡수한다. 이전 `recorded_at` 컬럼은 apply 중 `imported_at` 으로 rename 한다.
- 도메인 패키지(`internal/source`, `internal/memory`)는 row 모델과 쿼리만 가진다.

## 자동 백업

기존 저장소를 변경해야 하면 적용 직전 `VACUUM INTO '<db>.bak-v<N>'` 로 스냅샷을 만든다.
신규 DB는 백업하지 않는다.

- `VACUUM INTO` 는 WAL 상태에서도 일관된 스냅샷을 만든다.
- `<N>` 은 GORM ledger가 있으면 그 버전, 없으면 legacy goose 버전, 둘 다 없으면 0이다.
- 같은 스키마에 대해 `Apply` 를 반복 실행하면 아무 변경도 하지 않는다.

## SQLite 주의점

- GORM은 일반 테이블 생성과 additive migration에 적합하지만, FTS5 같은 SQLite 전용
  객체는 raw SQL이 더 정확하다.
- 컬럼 rename처럼 GORM이 의도를 알기 어려운 변경은 작은 명시 SQL로 둔다.
- 연결은 기존 `*sql.DB` 를 재사용한다. storage와 GORM migrator 모두 pure-Go SQLite
  driver를 공유하므로 cgo-free 배포를 유지한다.

## Considered options

- **GORM + 필요한 raw SQL (선택)** — schema 모델이 Go 코드에 모이고, FTS5 같은
  SQLite 전용 객체만 명시 SQL로 남겨 중복을 줄인다.
- **goose SQL 파일 유지 (거절)** — 실행기는 안정적이지만, 같은 스키마가 Go row 모델과
  SQL 파일에 중복되어 이름 변경·컬럼 진화 때 어긋나기 쉽다.
- **직접 SQL runner (거절)** — 단순해 보이지만 버전 기록, 기존 DB 흡수, 백업 타이밍을
  직접 계속 관리해야 한다.
- **`bun/migrate` (거절)** — 현재 query layer는 bun을 쓰지만, 이번 결정의 목표는
  schema source를 Go 모델로 모으는 것이므로 GORM migrator가 더 직접적이다.
