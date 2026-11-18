# flint feature map — 검증 여정 인덱스

검증 원천. 파일 1개 = 기능 1개 = 드라이브 1단위. 맵에 있는 여정을
돌지 않는 증명은 불완전하다. MCP 하니스는 미포함(다음 이터레이션).

| 파일 | 기능 | 표면 |
|---|---|---|
| [write-pipeline.md](write-pipeline.md) | write 파이프라인(검증·dedup·판정·정규화·이스케이프·exit code) | CLI |
| [read-hot.md](read-hot.md) | read·hot(status·주입 예산·제로히트 exit 0) | CLI |
| [export.md](export.md) | export 5포맷 + `--out -` stdout·사이드카 | CLI |
| [backup-drill.md](backup-drill.md) | backup·drill(덤프·사이드카·복구 훈련) | CLI |
| [status-doctor-report.md](status-doctor-report.md) | status·doctor·report·audit(운영 진단) | CLI |
| [tui-screens.md](tui-screens.md) | TUI 폼(export·maintain·pending) | TUI(tmux) |

각 파일의 H2는 정확히 4개: `Sub-features` / `How to get to it (user POV)` /
`Driving it with Claude Code` / `Gotchas`.
