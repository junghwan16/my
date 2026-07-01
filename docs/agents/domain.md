# Domain Docs (ADR)

엔지니어링 스킬이 코드를 탐색할 때 이 리포의 도메인 언어와 아키텍처 결정 기록(ADR)을 어떻게 소비하는지.

## 탐색 전에 읽을 것

- **`CONTEXT.md`** — Source, Memory, Scope, Recall 같은 제품 언어의 짧은 지도.
- **`docs/design-docs/`** — 건드릴 영역에 닿는 ADR.

이 디렉토리가 없으면 **조용히 진행**한다. 부재를 플래그하거나 미리 만들자고 제안하지 않는다. producer 스킬(`spec-grill-docs`)이 되돌리기 어려운 결정이 실제로 굳을 때 lazy 하게 만든다.

## 파일 구조

single-context:

```text
/
├── docs/design-docs/
│   ├── 0001-memory-naming.md
│   └── 0002-source-memory-package-split.md
└── src/
```

## ADR 충돌 플래그

출력이 기존 ADR 과 모순되면 조용히 덮지 말고 명시한다:

> _ADR-0002(source-memory package split)와 모순 — 그래도 재론할 가치가 있는 이유는…_

일관된 도메인 용어를 쓰되(이슈 제목·리팩터 제안·가설·테스트 이름), `CONTEXT.md`의 짧은 언어 지도를 먼저 따르고 세부 설계 판단은 기존 ADR에서 실제 쓰이는 용어를 따른다.
