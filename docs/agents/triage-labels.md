# Triage Labels

스킬들은 5개 canonical triage role 로 말한다. 이 파일은 그 role 을 이 리포 이슈 트래커의 실제 라벨 문자열로 매핑한다.

| Canonical role    | 우리 트래커 라벨   | 의미                              |
| ----------------- | ----------------- | --------------------------------- |
| `needs-triage`    | `needs-triage`    | 메인테이너 평가 필요              |
| `needs-info`      | `needs-info`      | 리포터 답변 대기                  |
| `ready-for-agent` | `ready-for-agent` | 완전 명세, 사람이 자리를 비워도 에이전트가 바로 집음 |
| `ready-for-human` | `ready-for-human` | 사람 구현 필요                    |
| `wontfix`         | `wontfix`         | 처리 안 함                        |

스킬이 role 을 언급하면(예: "`ready-for-agent` 라벨 적용") 이 표의 해당 라벨 문자열을 쓴다.

GitHub 에서는 이 문자열을 같은 이름의 라벨로 사용한다(`gh issue edit --add-label`).
