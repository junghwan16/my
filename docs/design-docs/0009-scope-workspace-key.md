---
status: proposed
---

# Scope 매칭은 raw cwd 가 아니라 파생된 workspace key 로 한다

## 맥락

지금 Scope.Value 는 세션이 기록될 때의 **파일시스템 cwd 문자열 그대로**다
(`internal/source/claude.go`의 `Scope{Value: meta.CWD}`, codex 동일). Recall 은 질의
scope 를 `os.Getwd()` 로 잡고(`cmd/gieok` recallScope), store 는
`WHERE sr.scope_value = ?` 로 **완전 일치** 필터한다.

이 설계는 "같은 워크스페이스 = 같은 경로 문자열"을 가정하는데, 실제로는 한 프로젝트가
여러 경로로 나타난다:

- **rename**: `/Users/jeff.cho/personal/my` → `/Users/jeff.cho/personal/gieok`
  (프로젝트 이름이 바뀜). 같은 프로젝트인데 두 Scope 로 갈린다.
- **worktree**: `.../gieok/.claude/worktrees/<name>` 는 `.../gieok` 와 다른 Scope 다.
- **conductor / 다중 워크스페이스**: `.../conductor/workspaces/adserver/<city>`,
  `.../worktrees/adserver/<x>` 등 한 프로젝트가 수십 경로로 흩어진다.

실측: 현재 store 에 **distinct scope 181개** — 대부분 소수 실제 프로젝트의 경로 변종이다.

### 결과 (Useful Recall 을 직접 깎는다)

정본 Scope `/Users/jeff.cho/personal/gieok` 에서 recall 하면 gieok-dev 지식이 하나도 안
나온다 — 그 지식이 `/personal/my`(구 이름)와 worktree 경로에 갇혀 있기 때문이다. eval
smoke 가 전 시나리오 0 결과를 낸 1차 원인이 바로 이 Scope 분절이었다(코퍼스 품질 문제와
별개). 다음 에이전트가 "같은 프로젝트, 다른 경로"에서 아무것도 recall 하지 못한다.

## 결정 (제안)

**세션이 기록한 raw 위치는 그대로 보존하되(감사·출처용), Recall 이 그룹핑·필터에 쓰는
키를 raw cwd 에서 분리해 파생된 안정적 `workspace key` 로 만든다.**

- **원본 보존**: `sources.scope_value`(raw cwd)는 건드리지 않는다. 출처·디버깅 정보다.
- **파생 workspace key**: 정규화 규칙으로 raw cwd 에서 안정 키를 계산한다.
  - worktree 접미사 제거: `<root>/.claude/worktrees/<name>` → `<root>`.
  - conductor/멀티워크스페이스 접미사 축약: `.../conductor/workspaces/<proj>/<x>` →
    `<proj>` 기준 키, `.../worktrees/<proj>/<x>` → `<proj>`.
  - 명시적 alias 표로 rename 흡수: `personal/my` ↔ `personal/gieok`.
  - 가능하면 git 신원(repo root basename 또는 remote)을 키로 승격 — 세션은 이미
    `gitBranch`/`cwd` 를 메타에 담으므로 확장 여지가 있다.
- **Recall 경로도 같은 정규화**: 질의 scope 도 동일 규칙으로 정규화한 뒤 workspace key
  로 매칭한다. 완전 일치가 아니라 "같은 workspace key" 로 그룹핑.
- **가법적·되돌릴 수 있음**: raw scope 를 지우지 않고 키를 얹으므로, 규칙이 틀리면 키만
  재계산한다. 기존 데이터는 백필로 키를 채운다.

## 근거

- **원본과 매칭 키 분리**가 핵심이다. cwd 는 훌륭한 출처 정보지만 나쁜 그룹핑 키다 —
  경로는 자주 바뀌고(rename·worktree·이동) 지식의 소속은 안 바뀐다. 둘을 한 필드가
  겸하니 recall 이 깨진다.
- **정규화 규칙은 관찰된 패턴 기반**이다(worktree/conductor 접미사, 181개 scope 의
  실제 형태). 추측이 아니라 데이터에서 왔다.
- **가법적 설계**라 위험이 낮다. 원본 scope 를 파괴하는 마이그레이션이 아니라 파생 키를
  추가하는 것이라, 규칙을 반복 개선할 수 있다.

## Considered options

- **파생 workspace key (선택)** — raw 보존 + 안정 키. 되돌릴 수 있고 backfill 가능.
- **raw cwd 를 git repo 신원으로 교체 (거절 후보)** — 가장 깨끗하지만, 세션 파일에
  remote URL 이 없고 import 호스트에서 cwd→repo 해석이 불확실하다. 원본을 파괴한다.
- **recall 시 관련 scope 집합으로 확장 (부분 채택 가능)** — prefix/substring 으로
  질의 scope 를 여러 개로 넓히는 것. 정규화 키의 런타임 근사이지만, 규칙이 매칭 시점에
  흩어져 일관성이 떨어진다. workspace key 파생의 하위 수단으로만.

## 상태

제안 단계. 측정 근거: recall eval(`gieok memory eval`)에서 정본 scope 대 `/my` scope
hit rate 차이로 효과를 정량화할 수 있다. GitHub issue #21(scope 연속성)과 연결.
구현 전, 진행 중인 코퍼스 재-ingest 가 끝난 뒤 baseline 을 고정하고 이 변경의 hit@k
델타를 측정한다.
