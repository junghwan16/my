---
status: accepted
---

# 스키마 마이그레이션은 goose(embedded SQL) + 파괴적 변경 전 자동 백업으로 관리한다

이 프로젝트는 완전한 로컬 데스크탑 앱이다: 사용자당 SQLite 파일 1개
(`~/.local/share/my/memory/my.db`)가 유일본이고, 마이그레이션 커맨드를 사용자가
직접 칠 일이 없으며, 단일 바이너리로 배포된다. 초기 구현은 `source.Migrate` /
`memory.Migrate` 가 각각 `CREATE TABLE IF NOT EXISTS` 를 실행할 뿐, 버전 개념이
없어 컬럼 진화·이력·순서 보장이 불가능했다.

## 결정

검증된 마이그레이션 라이브러리 **goose(`github.com/pressly/goose/v3`)** 를
도입한다. `internal/migrate` 가 마이그레이션을 소유한다:

- **DDL 은 goose SQL 파일**(`migrations/00001_*.sql`, `00002_*.sql`)로 두고
  `//go:embed` 로 바이너리에 심는다. 단일 바이너리가 자기 스키마를 들고 다닌다.
- `source.Migrate`/`memory.Migrate` 는 제거했고, 도메인 패키지는 row 모델과
  쿼리만 소유한다.
- **버전 추적**: goose 의 `goose_db_version` 테이블(라이브러리 기본).
- **자동 실행**: goose Provider API 로 `cmd/my` 의 `withStores` 가 DB 를 열자마자
  `migrate.Apply` 를 호출한다. 사용자에게 마이그레이션은 보이지 않는다.
- **순서**: 파일 번호가 곧 버전이라 `00001`(sources) → `00002`(memory_links)
  순으로 FK 의존(`memory_links → sources`)이 보장된다.
- **baseline 호환**: `00001`/`00002` 는 `CREATE TABLE IF NOT EXISTS` 라, 버전 이전에
  만들어진 기존 DB(테이블은 있고 `goose_db_version` 은 없음)에서 no-op 으로
  적용된 뒤 버전만 기록된다. 무중단 도입.

## 파괴적 마이그레이션 전 자동 백업

기억 저장소는 유일본이므로, goose 를 얇게 감싸 **이미 버전이 매겨진 DB**
(goose 현재 버전 ≥ 1)에 pending 마이그레이션을 적용하기 직전
`VACUUM INTO '<db>.bak-v<N>'` 로 스냅샷을 뜬다. Provider 의
`GetVersions(current, target)` 로 pending 여부를 판정한다.

- `VACUUM INTO` 는 WAL 상태에서도 일관된 스냅샷을 만든다(단순 파일 복사는 -wal
  내용을 놓칠 수 있다).
- 버전 0(신규 DB 또는 버전 이전 DB)의 baseline 은 no-op 이라 백업하지 않는다.

## SQLite 주의점 (구현 메모)

- 컬럼 타입 변경·제약 추가는 SQLite 에서 12-step 테이블 재생성이 필요하고, 그
  과정은 `foreign_keys=OFF`(트랜잭션 밖 토글) 를 요구한다. 그런 마이그레이션은
  goose 파일 상단에 `-- +goose NO TRANSACTION` 을 두고 PRAGMA 를 직접 다룬다.
  현재 파일(순수 CREATE)은 해당 없음.

## Considered options

- **goose (선택)** — `embed.FS` 1급 지원, 기존 `*sql.DB` 에 바로 붙는 Provider
  API(`HasPending`/`GetVersions`/`Up`)로 백업 래핑이 쉽다. 의존성이 가벼워 단일
  바이너리에 적합. `modernc.org/sqlite`(cgo-free) 와 문제없이 동작.
- **golang-migrate (거절)** — source/database 드라이버 조합과 up/down 파일 쌍이
  필요해 상대적으로 무겁다. 로컬 단일 바이너리엔 goose 가 더 단순하다.
- **직접 구현 러너 (거절)** — `PRAGMA user_version` 기반으로 ~90줄이면 되지만,
  검증된 라이브러리가 있는데 마이그레이션 실행·순서·트랜잭션·상태 조회를 재발명할
  이유가 없다.
- **`bun/migrate` (거절)** — 이미 bun 을 쓰지만, 마이그레이션 UX·생태계는 goose 가
  더 성숙하다.
