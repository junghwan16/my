# Issue tracker: GitHub

이 리포의 이슈·PRD 는 GitHub issue 로 산다. 모든 작업은 `gh` CLI.

## 컨벤션

- **이슈 생성**: `gh issue create --title "..." --body "..."`. 멀티라인 본문은 heredoc.
- **이슈 읽기**: `gh issue view <number> --comments`, 라벨도 함께.
- **이슈 목록**: `gh issue list --state open --json number,title,body,labels,comments --jq '[.[] | {number, title, body, labels: [.labels[].name], comments: [.comments[].body]}]'` + 적절한 `--label`/`--state` 필터.
- **코멘트**: `gh issue comment <number> --body "..."`
- **라벨 추가/제거**: `gh issue edit <number> --add-label "..."` / `--remove-label "..."`
- **닫기**: `gh issue close <number> --comment "..."`

리포는 `git remote -v` 로 추론 — clone 안에서 실행하면 `gh` 가 자동 인식.

## 스킬이 "publish to the issue tracker" 라고 하면

GitHub issue 를 만든다.

## 스킬이 "fetch the relevant ticket" 이라고 하면

`gh issue view <number> --comments` 실행.
