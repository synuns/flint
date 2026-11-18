# TUI 화면 — 폼·검토 뷰

flint의 대화형 화면 5종. export 폼(형식·상태·경로 선택 → 미리보기
확인), pending 검토(목록→상세→키 액션), maintain 검토(3소스 탭),
report 화면(진단 카드↔지표 요약), setup 위저드(exposure 선택). 전부
터미널(stdout TTY)에서만 뜨고, 비터미널에서는 exit 3 한 줄로 거부된다.
화면 식별은 라벨·프롬프트 문자열로 한다 — 좌표는 쓰지 않는다.

## Sub-features

- `export-form` — `flint export --tui` huh 폼(Format·Status·Output
  file) → `Export N rows (~M bytes)?` 확인 → 기존 export 실행.
- `pending-review` — `flint pending --tui` 목록("Pending review")→
  상세, 키 a approve·s supersede·d skip·e expire·q quit.
- `maintain-review` — `flint maintain --tui` 3탭(dup-suspects·
  conflict-suspects·stale), m merge·k keep·i invalidate·q quit.
- `report-screen` — `flint report --tui` 진단 카드·지표 요약 두 뷰, tab
  전환·enter 상세 확장·q 종료.
- `setup-wizard` — `flint setup` 무인자 + 터미널이면 위저드(Exposure
  profile 선택 → Backup key 안내). C-c 취소는 부작용 없음.
- `tui-contract` — 비터미널·FLINT_NO_TUI 차단 계약(exit 3). export·
  pending·report는 무인자 + 터미널 적응형 진입.

## How to get to it (user POV)

- `flint export --tui`(또는 무인자 `flint export` + 터미널).
- `flint pending --tui`(또는 무인자 `flint pending` + 터미널).
- `flint maintain --tui` — 단독만(다른 인자와 조합 불가).
- `flint report --tui`(`--days` 조합은 가능, `--html`·`--text`·`--out`는
  불가; 무인자 + 터미널 적응형).
- `flint setup` — 무인자 + 터미널이면 위저드.

## Driving it with Claude Code

Preconditions:

- 검증 DSN `postgresql://flint:flint-dev@localhost:5434/flint_verify`에
  빌드·시드가 끝난 상태(스킬 Launch 참조).
- 모든 화면 구동은 `scripts/verify/tui_capture.sh`로 — tmux pty에서
  띄우고 키를 주입하고 텍스트 스냅샷을 남긴다.
- 증거는 `.superlite/verify/<version>/captures/`, 판정은 manifest에.

- **export 폼 진입.** `scripts/verify/tui_capture.sh --cmd "FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint export --tui" --out .superlite/verify/<version>/captures/export-form.txt`. 캡처에 `Format`·`Status`·`Output file (optional — empty writes to stdout)` 라벨과 하단 안내 `pipe output or pass flags for non-interactive mode`가 보인다.
- **export 기본값 완주.** 같은 --cmd에 `--keys 'enter,enter,enter,enter'`. 3 enter로 폼 기본값(jsonl·active·stdout)을 확정하고 마지막 enter가 `Export N rows (~M bytes)?` 확인에서 Export를 선택 — 화면이 실제 export 결과로 바뀐다. 캡처가 실행 결과까지 담는다.
- **export 취소.** 같은 --cmd에 `--keys 'C-c'`. 폼 중단이 `export cancelled` 1행으로 끝난다(실행 없음, 깨끗한 취소).
- **pending 검토 뷰.** `scripts/verify/tui_capture.sh --cmd "FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint pending --tui" --keys 'enter' --out .superlite/verify/<version>/captures/pending-detail.txt`. 진입 캡처에 목록 제목 `Pending review`와 키 안내 `enter detail · a approve · s supersede · d skip · e expire · q quit`. enter 후 캡처가 선택 항목 상세(본문·출처)를 보인다. `--keys 'enter,esc,q'`로 복귀·종료까지 돈다.
- **pending 적응형 진입.** `--cmd "... ./bin/flint pending"`(무인자)도 tmux pty라 같은 화면이 뜬다 — 무인자 + 터미널 적응형 계약의 실증.
- **maintain 검토 뷰.** `scripts/verify/tui_capture.sh --cmd "FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint maintain --tui" --keys 'tab' --out .superlite/verify/<version>/captures/maintain-tabs.txt`. 목록 제목 `Maintain review`, tab으로 dup-suspects·conflict-suspects·stale 탭이 전환된다. 시드 직후엔 후보가 없어 빈 목록일 수 있다 — 빈 목록은 결함이 아니다.
- **report 화면.** `scripts/verify/tui_capture.sh --cmd "FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint report --tui" --keys 'tab' --out .superlite/verify/<version>/captures/report-tui.txt`. 진단 카드 뷰에서 tab으로 지표 요약 뷰로 전환, `q`로 종료. 데이터는 JSON 뷰와 동일 원천이라 진단 0건이면 카드가 비어 있다.
- **setup 위저드 캡처.** `scripts/verify/tui_capture.sh --cmd "FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint setup" --keys 'C-c' --out .superlite/verify/<version>/captures/setup-wizard.txt`. `Exposure profile` 선택 항목(`Normal — full detail`·`Minimal — compact output`)과 `Backup key` 안내가 캡처에 보인다. C-c는 위저드 취소 — 부작용 없음.
- **비터미널·차단 계약.** `FLINT_DSN='...' ./bin/flint export --tui | cat` → exit 3, `{"error":...}`에 `TUI requires a terminal — use flag-mode instead`. `FLINT_NO_TUI=1 FLINT_DSN='...' ./bin/flint maintain --tui` → exit 3, `--tui is blocked by FLINT_NO_TUI`. 파이프(비 pty) 실행만 exit 3이 나오면 계약 성립.

## Gotchas

- TUI 판정은 stdout TTY다. tmux pty에서만 화면이 뜨고, 셸 파이프·리다이렉션에서는 전부 exit 3(lib/tui/tui.go — /dev/null 리다이렉션도 비터미널 취급).
- FLINT_NO_TUI=1은 3층 방어 최상위 — 적응형 진입을 차단하고, maintain는 명시적 `--tui`까지 차단한다. CI·에이전트 환경 안전장치.
- 무인자 `flint export`·`flint pending`·`flint report`는 터미널이면 TUI로 흐른다(적응형). 검증 스크립트에서 JSON이 필요하면 플래그를 붙이거나 FLINT_NO_TUI=1로 강제한다.
- export 완주 여정의 마지막 enter는 실제 export를 실행한다(stdout 기본). 부작용 없는 증거만 필요하면 폼·확인 캡처에서 끊는다.
- export 미리보기 상한: 선택 필터의 행 수가 export_import_max_rows를 넘으면 `export exceeds max rows`로 exit 3 — 시드 규모에서는 닿지 않는다.
- maintain `--tui`는 다른 인자와 조합하면 exit 3(`--tui cannot be combined with ...`), report `--tui`는 `--html`·`--text`·`--out`와 조합 불가(exit 3) — `--days`는 조합된다.
- pending 화면 폭 40컬 이하에서는 키 안내가 축약 표기로 바뀐다(lib/tui/pending.go keyHint). tui_capture.sh 기본 폭 200이라 전체 안내가 잡힌다.
- 화면 확인은 캡처 텍스트의 라벨·프롬프트 문자열로 판정한다 — 행·열 좌표는 폭·테마에 의존해 재현되지 않는다.
