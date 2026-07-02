---
status: accepted
---

# web UI를 React + Vite + TypeScript + Tailwind/shadcn로 재작성하고 산출물을 커밋해 embed한다

ADR-0008 은 web 표면을 손으로 쓴 정적 HTML/JS + go:embed 로 세웠다. UI 가
검색·그래프 두 페이지로 커지며 상태(scope 선택, recall, 그래프 온디맨드 확장, 편집)와
컴포넌트가 늘어, vanilla HTML/JS 로는 유지보수와 재디자인이 어렵다. 다만 프로젝트는
오프라인 순수 Go CLI(`go install` 로 끝, CDN 의존 없음)이고, JS 빌드 툴체인 도입은
되돌리기 어려운 선택이다.

## 결정

**web UI 를 React + TypeScript 로 작성하고 Vite 로 빌드한다. 스타일은 Tailwind v4 +
shadcn/ui(Radix 프리미티브 + cva)로 Linear 풍의 다크 테마를 입히고, scope 선택은
네이티브 `<select>`. 빌드 산출물을 `internal/web/static/` 에 커밋하고 go:embed 로
묶는다. Go 서버·JSON API 는 편집 엔드포인트(ADR-0010) 외에는 무변경.**

- **오프라인 불변식 유지**: Tailwind 는 빌드 타임에 정적 CSS 로 컴파일되고, shadcn 컴포넌트는
  소스로 레포에 들어와 소유된다(런타임 CDN 없음). Inter(가변 폰트)는 `@fontsource` 로
  번들해 woff2 가 빌드 자산이 되고, 한글은 시스템 폰트 스택으로 둔다. cytoscape 는 CDN
  vendoring 대신 번들에 포함해 자체 완결이다 → 런타임·CDN 의존 0.
- **`go install` 유지**: 커밋된 산출물 덕에 Go 전용 소비자는 Node 없이 빌드·설치한다.
  Node 툴체인은 UI 를 *수정*할 때만 필요하다.
- **drift 방지**: presubmit/CI 에서 UI 를 빌드한 뒤
  `git diff --exit-code internal/web/static` freshness 검사를 돌려 소스(`ui/src`)와
  커밋된 산출물의 불일치를 잡는다 — 지금 `go mod tidy` diff 검사와 동형이다.

디자인 방향은 Linear(near-black + 인디고 액센트, Inter, 고밀도, ⌘K 커맨드 팔레트)를
앵커로, Roam 의 Linked-References(원본 + 연결 기억)와 Obsidian 의 다크 그래프 뷰
패턴을 결합한다.

## 근거

- **왜 빌드 프레임워크**: 두 페이지 + 상태 + 편집 + 재디자인을 vanilla 로 이어가면
  유지보수 비용이 크다. React + TS 가 컴포넌트·타입·상태를 정리하고, cytoscape 그래프도
  React 컴포넌트로 감싸 재사용한다.
- **왜 Tailwind + shadcn**: 접근성 있는 Radix 프리미티브(Dialog, Command, ScrollArea
  등)를 소스로 소유하면서 Linear 풍 커스텀 테마를 토큰(CSS 변수)으로 입힐 수 있다.
  Dialog/cmdk 는 편집·⌘K 팔레트에 바로 쓰인다.
- **왜 산출물 커밋**: `go install <mod>@latest` 는 npm 을 돌리지 않는다. 산출물 커밋만이
  오프라인·무툴체인 설치를 지킨다.

## Consequences

- 레포 첫 JS 툴체인이 들어온다: `internal/web/ui/`(React+TS+Vite+Tailwind+shadcn 소스),
  package.json, dev 전용 node_modules. `mise.toml` 에 node 핀과 `verify` 에 UI
  빌드 + freshness 검사를 추가한다.
- 커밋 diff 에 생성된 `static/` 이 포함된다 — 리뷰 시 소스(`ui/src`)와 산출물이 함께
  바뀐다. Vite 의 content-hash 파일명은 결정적이라 같은 소스는 같은 산출물을 낸다.

## Considered options

- **React + Vite + TS + Tailwind/shadcn, 산출물 커밋 (선택)** — 접근성 프리미티브를
  소유하며 Linear 아이덴티티를 토큰으로 표현, 오프라인 불변식 유지.
- **CSS Modules (거절)** — 무설정·무의존으로 먼저 채택해 bespoke 테마를 구현했으나,
  디자인 방향을 Linear/shadcn 기반으로 다시 잡으며 접근성 프리미티브(Dialog·Command)를
  손으로 재구현하지 않도록 shadcn 으로 전환했다.
- **StyleX (거절)** — atomic CSS·타입세이프 토큰은 매력적이나 Vite 통합에 postcss+babel
  배선이 필요하고 2페이지 규모엔 과했다.
- **vanilla HTML/JS 유지 (거절)** — 두 페이지 + 편집으로 커지며 유지보수 한계.
- **dist 비커밋 + CI 에서만 빌드 (거절)** — `go install @latest` 가 깨진다.
