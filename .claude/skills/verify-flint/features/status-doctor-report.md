# status·doctor·report·audit — 운영 진단

운영 상태를 보는 4개 커맨드. status는 서버 생존·스키마·대기 수와
계측을 한 줄 JSON으로 보고하고, doctor는 건강검사 체인을 돌아 고침
명령을 안내하며, report는 기간 지표 리포트를 JSON·text·HTML로
내고, audit은 dolt_log 커밋 목록을 감사한다. 전부 읽기 경로다
(doctor의 warn 카운터 기록이 유일한 쓰기 — Gotchas).

## Sub-features

- `status-json` — ping·schema_version·pending + hot/read 계측 한 줄
  JSON. exit 0 / 2(서버 다운) / 3(스키마 비호환).
- `status-as-of` — `--as-of <spec>` 과거 스냅샷의 pending 수(출력 말미
  as_of 해시).
- `doctor-full` — 검사 16종 체인. 최악 상태가 overall 판정, fail는
  exit 1·warn는 exit 0.
- `doctor-subset` — `--check NAME` 단일 검사·`--list-checks` 이름
  목록·`--json` 구조 고정·`--fix`(hook-file만 자가수복).
- `report-json` — `--days`(기본 7) 창의 일별 유입·counts·stale·effect·
  curation·diagnosis 한 줄 JSON.
- `report-views` — `--text` 사람 읽기 뷰·`--html [--out FILE]` 단일
  파일 대시보드.
- `audit-log` — dolt_log 최근 N(기본 20·상한 100). 컬럼은
  commit_hash·committer·date·message 4종 고정.
- `audit-filter` — `--session ID`는 message 라벨 필터, telemetry
  커밋(`telemetry: ` 접두)은 기본 제외·`--telemetry`로 포함, `--stat`는
  테이블 변경 부록.

## How to get to it (user POV)

- `flint status [--as-of <spec>]`
- `flint doctor [--list-checks] [--check NAME] [--fix] [--json]`
- `flint report [--days N] [--html] [--out FILE] [--text]`
- `flint audit [--limit N] [--session ID] [--stat] [--telemetry]`

## Driving it with Claude Code

Preconditions:

- 검증 DSN `postgresql://flint:flint-dev@localhost:5434/flint_verify`에
  빌드·시드가 끝난 상태(스킬 Launch 참조).
- doctor 전체 실행은 compose 프로젝트 디렉터리(메인 체크아웃)에서 —
  이유는 Gotchas 첫 항.
- 증거는 `.superlite/verify/<version>/captures/`, 판정은 manifest에.

- **status 기본.** `FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint status`. Exit 0. 키 순서는 status, schema_version, pending, hot_calls, hot_p95, read_calls, read_zero_pct, last_write_at, last_hot_at, last_read_at, read_fallback_calls, read_calls_14d, read_zero_pct_14d, read_zero_near_pct_14d, read_window_days, hot_bytes_14d, read_bytes_14d, read_bytes_p95, hot_bytes_p95. 시드 직후 last_*·read_fallback_calls가 null인 것은 정상(미기록 계측).
- **status 서버 다운 계약.** 같은 커맨드를 `FLINT_DSN='postgresql://flint:flint-dev@localhost:5435/flint_verify'`(빈 포트)로. Exit 2, `{"error":...}` 1행 — **stdout**이다(stderr 아님).
- **doctor 전체.** 메인 체크아웃에서 `FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint doctor`. `{"status":...,"checks":[{"name","status","detail"?,"fix"?}...]}` 1행. 시드 직후 DB는 overall `warn`(`last-seen`·`backup-age` 미기록)에 exit 0 — warn는 exit를 깨지 않는다. fail가 있으면 exit 1.
- **doctor 서브셋.** `flint doctor --check db` → `{"status":"ok","checks":[{"name":"db",...}]}` exit 0(워크트리 어디서나 안전). `flint doctor --list-checks` → `{"checks":[검사 이름 16개]}` — 이름은 --check 허용 목록과 같다.
- **report JSON.** `FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint report`. Exit 0. `days` 배열 7행(오래된 날→오늘, 빈 일자 0행), `counts.pending`이 status의 pending과 일치, `diagnosis`는 정상일 때 `[]`(정상 침묵). `--days 3`은 days 3행.
- **report text 뷰.** `... ./bin/flint report --text`. 첫 행 `flint report · 7d window (...)`, 둘째 행 `writes N · reads N · dup prevented N (dup N%) · zero hits N% · fallback N%`. 진단 0건이면 diagnosis 섹션 자체가 없다.
- **report HTML.** `... ./bin/flint report --html --out /tmp/flint-verify/report`. `/tmp/flint-verify/report.html` 생성(`.html` 접미가 없으면 붙는다), exit 0. 수집 오류의 exit 코드는 JSON 뷰와 동일 계약.
- **audit 기본·--stat.** `FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint audit --stat`. Exit 0, `{"log":[...]}`. 항목 키는 commit_hash·committer·date·message(+`stat` 테이블별 data_change·schema_change, `date_rel` 상대 시각). 기본 출력의 message에 `telemetry: ` 접두가 없는 것으로 제외 계약을 확인한다.
- **audit 세션 필터.** 합성 행 1건을 라벨해 기록: `echo '{"topic":"검증 데모","summary":"audit --session 실증용 합성 행이다.","tags":["demo"],"entity":"flint","domain":"verify","confidence":0.5}' | FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify' ./bin/flint write knowledge --session verify-demo`(exit 0, hash 포함). 이어 `flint audit --session verify-demo` → message에 `[session:verify-demo]`가 붙은 커밋만 반환. `flint audit --limit 1`이 1행만 내는 것으로 limit도 확인.

## Gotchas

- doctor의 `port` 검사는 cwd에서 `docker compose ps`를 실행한다
  (lib/ops/doctor.go ComposePsFn). compose 프로젝트명이 디렉터리
  이름이라 워크트리에서 돌리면 컨테이너를 못 찾아 fail → exit 1. 전체
  doctor는 메인 체크아웃에서, 워크트리에서는 `--check db`로 DB 도달성만.
- doctor는 warn 판정 검사마다 meta `fired_doctor_<check>` 카운터를
  증분 기록한다 — "read-only"의 유일한 쓰기(횟수만, 개인정보 무관).
- 오류 한 줄 `{"error":...}`는 stdout이다. 스킬의 오류 캡처는 stdout을
  본다.
- status의 hot_calls·hot_p95는 직전 실행 누적을 meta에서 불러오는
  baseline이다(프로세스가 무상태) — hot 드라이브 직후 값이 곧바로
  반영되지 않는다.
- report `--days`는 보존 창(daily_retention_days, 기본 14)으로
  clamp된다 — 그 너머의 날짜는 빈 행으로만 존재한다.
- audit `--limit`은 비수 값 exit 3, 0·음수는 조용히 기본 20, 100 초과는
  100으로 clamp — 큰 값을 줘도 컬럼·동작은 늘지 않는다.
- fresh 검증 DB의 dolt_log는 스키마 마이그레이션·시드 커밋뿐이라 로그가
  짧다 — 결함 아님. 세션 라벨 커밋은 write `--session`으로 만든다.
