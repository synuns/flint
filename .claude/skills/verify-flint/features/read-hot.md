# Read · hot · status (flint read / hot / status)

read는 활성 지식만 노출하는 active_knowledge 뷰에서 질의에 맞는 행을
돌려주고, hot은 세션 주입용 요약(헤드라인 1줄 또는 예산 절단된 전체
목록)을 만들고, status는 인스턴스 진단을 한 줄 JSON으로 보고한다.
제로히트는 오류가 아니라 exit 0이다.

## Sub-features

- `read-query` 자유 텍스트 질의 + `--tag --entity --domain --limit --status` 필터. 무효 사실(invalid)은 뷰가 차단한다.
- `read-hit` 기본 텍스트 `id|topic|summary`(summary 80 rune 절단), `--json`은 행별 한 줄 JSON.
- `read-limit` 기본 10·상한 50 clamp — `--json`에 `{"limit_requested":N,"limit_applied":50}` 부가 줄.
- `read-zero` 제로히트 exit 0 — 텍스트 모드 완전 침묵, `--json`은 `{"hits":0}` 한 줄.
- `hot-headline` 무인자 실행 1줄 헤드라인 `[flint] active N · pending M · recent decisions: ...`.
- `hot-full` `--full` 전체 주입 `kind|topic|summary|ts` 줄, hot_budget(기본 2,000 rune) 초과 시 `…[truncated]` 절단, pending 행은 `·+N일` 대기 접미.
- `status-readhot` ping·schema_version·pending 카운트·read/hot 지표의 한 줄 JSON.

## How to get to it (user POV)

- 세션 시작 훅이 헤드라인을 프롬프트에 주입한다(SessionStart → `flint hot`).
- 작업 전 회상: 에이전트가 `flint read <query>`로 검색한다.
- 상태 점검: `flint status`로 pending 수·최근 활동을 본다.

## Driving it with Claude Code

Preconditions:

- 검증 DSN: `FLINT_DSN='postgresql://flint:flint-dev@localhost:5434/flint_verify'`.
- `make seed FLINT_SEED_DSN=$DSN` 직후(합성 시드: active 지식 8·pending 1·프로젝트 3).

- **read 히트(JSON).** 시드 topic을 정확히 찌른다. `FLINT_DSN=$DSN ./bin/flint read "flint exit code" --json`. Exit 0이고 행별 한 줄 JSON에 `"topic":"flint exit code"`가 있다.
- **read 히트(텍스트).** 같은 질의에서 `--json`을 뺀다. `N|flint exit code|<summary 80 rune 이내>` 꼴의 `|` 조인 줄이 나온다.
- **제로히트.** 시드에 없는 무의미 단일 토큰을 찌른다. `FLINT_DSN=$DSN ./bin/flint read "지브릴스키월드"`. stdout 완전 무출력·exit 0. `--json`을 붙이면 `{"hits":0}` 한 줄·exit 0.
- **limit clamp.** `FLINT_DSN=$DSN ./bin/flint read "flint" --limit 100 --json`. 결과 뒤에 `{"limit_requested":100,"limit_applied":50}` 부가 줄이 붙는다.
- **status 필터.** `FLINT_DSN=$DSN ./bin/flint read "검증용 대기 지식" --status pending --json`. 기본 뷰(active)에 없던 pending 행이 나온다 — 판정 여정(write-pipeline)으로 pending 행을 먼저 만들었으면 히트, 아니면 `{"hits":0}`.
- **hot 헤드라인.** `FLINT_DSN=$DSN ./bin/flint hot`. `[flint] active 3 · pending 1 · recent decisions: ... — recall: flint read <query>` 1줄·exit 0.
- **hot 전체.** `FLINT_DSN=$DSN ./bin/flint hot --full`. `project|데모 게임 프로젝트|active|<ts>` 꼴 줄들이 나오고 전체 길이는 2,000 rune 이하다. pending 행 끝에 `·+N일` 접미가 붙는다.
- **status.** `FLINT_DSN=$DSN ./bin/flint status`. `{"status":"ok","schema_version":"...","pending":N,...}` 한 줄·exit 0. write 여정 직후면 pending이 그 여정의 증가분만큼 올라 있다.

## Gotchas

- `--json` 행의 값은 전부 문자열이다(FormatRows는 map[string]string으로 마샬, lib/query/read.go). `"confidence":"0.9"`처럼 숫자도 문자열로 온다.
- 제로히트 판정 전에 단어 OR 폴백 재쿼리가 한 번 돈다 — 오타가 섞인 질의도 히트할 수 있다. 제로히트 여정은 폴백 단어까지 매칭 안 되는 무관 단일 토큰으로만 검증한다.
- `--session` 미지정이면 harness 설정 디렉터리의 `session-*.label` 최신 파일을 조용히 채택하고 계측 source가 `hook`로 기록된다(lib/wire/session.go). 검증 환경에 라벨 파일이 있으면 계측이 달라진다 — 재현성이 필요하면 `--source cli`를 명시한다.
- `--full`의 행 상한은 10행이다(HotSQL이 ClampLimit(0)=10). 예산 2,000 rune는 그 10행의 표시 문자열에 적용된다(ClampHot). 반면 헤드라인 카운트는 상위 50행(hotHeadlineLimit)에서 센다.
- decision 팔은 `ingested_at >= now() - interval '7 days'` AND `tags LIKE '%:adr:%'`(lib/sqlfs/views.sql). 검증 DB 재사용 시 재시드는 duplicate라 ingested_at이 갱신되지 않는다 — 오래된 시드면 recent decisions가 0으로 보인다(버그 아님).
- read 1회도 dolt 커밋을 남긴다(`read: recall bump (read audit attribution)`, 회상 강화 계측). audit 커밋 수 비교에서 이 커밋을 제외하지 않으면 오탐한다.
