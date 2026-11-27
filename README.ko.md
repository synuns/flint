<p align="center">
  <br/>
  ◯ ─────────── ◯
  <br/><br/>
  <img src="assets/flint-logo.svg" width="120" alt="flint 로고">
  <br/><br/>
  <strong>F L I N T</strong>
  <br/><br/>
  ◯ ─────────── ◯
  <br/>
</p>

<p align="center">
  <strong>AI 코딩 에이전트를 위한 장기기억</strong>
  <br/>
  <sub>세션이 쌓일수록 에이전트가 똑똑해집니다 — Claude Code, Codex, Gemini, Copilot, Cursor 등 8종 지원</sub>
</p>

<p align="center">
  <a href="README.md">English</a> | <a href="README.ko.md">한국어</a>
</p>

<p align="center">
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26"></a>
  <img src="https://img.shields.io/badge/DoltgreSQL-versioned_data-2f7d6f" alt="DoltgreSQL">
  <img src="https://img.shields.io/badge/MCP-native-8b5cf6" alt="MCP">
  <img src="https://img.shields.io/badge/harnesses-8-10b981" alt="8 하네스">
  <a href="https://github.com/synuns/flint/actions/workflows/ci.yml"><img src="https://github.com/synuns/flint/actions/workflows/ci.yml/badge.svg" alt="CI 상태"></a>
  <img src="https://img.shields.io/badge/release-v1.39.1-blue" alt="v1.39.1">
</p>
<p align="center">
  <sub>8종은 자동 등록(A급) 하니스 기준 — 전체 지원 목록은 아래 <a href="#에이전트--어댑터">어댑터 표</a>에 있습니다.</sub>
</p>

<p align="center">
  <a href="#빠른-시작">빠른 시작</a> ·
  <a href="#동작-원리">동작 원리</a> ·
  <a href="#왜-안전한가">안전성</a> ·
  <a href="#커맨드">커맨드</a> ·
  <a href="#에이전트--어댑터">에이전트</a>
</p>

AI 코딩 에이전트는 세션이 끝나면 모든 걸 잊습니다. 지난주에 정한 아키텍처, 한 달 전에 문서화한 함정, 어제 겪은 배포 이슈 — 전부 백지 상태로 다시 시작합니다. 실력은 좋은데 건망증이 심한 동료와 같습니다.

flint는 이걸 바꿉니다. 에이전트가 무언가 배우면 — 결정, 관례, 주의사항 — flint가 Doltgres에 기록합니다. Doltgres는 모든 변경이 커밋으로 남는 데이터베이스라서, 기억이 쌓이는 과정 자체가 이력으로 보존됩니다. 다음 세션이 시작되면 flint가 그동안 쌓은 맥락을 다시 주입합니다. 에이전트는 처음부터 다시 배우는 게 아니라 지난번에 이어서 작업합니다.

효과는 복리로 쌓입니다. 첫 주에는 기술 스택을 익히고, 한 달쯤 지나면 gRPC를 REST 대신 선택한 이유를 기억하고, 석 달쯤 되면 마이그레이션 순서와 불안정한 테스트 회피법, 배포 체크리스트까지 알고 있습니다. 직접 기록해뒀으니까요.

사람은 Obsidian에 노트를 남깁니다. 에이전트는 flint에 기억을 남깁니다 — 불과 도구를 얻기 위해 두드리는 부싯돌처럼.

> v1.39.1 · r143-angle2-sessionlabel 패치 — 보안: secrets 스캔이 session_label 커버·work_state 원문 저장 시 session_label 살균·재보고 시 라벨 백필·라벨 파일 폴백 검증(제어문자·200자 상한) · [전체 변경 이력](docs/HISTORY.md)
> v1.39.0 · 지식 성장 모델 2단계 — recall 이력이 read 순위에 합류(5번째 RRF 신호)·recall-audit 권장 처분(supersede/expire/hold·decay-audit 패리티)·세션 사슬: state open이 세션 라벨 기록·hot이 프로젝트 우선 스냅샷 부활·release-gate가 CI 검증 로컬 선실행 · [전체 변경 이력](docs/HISTORY.md)
> v1.38.4 · r143-angle4 패치 — 문서: read projection 서술 교정(10열·status 포함)·work_state 보존 계약(90일 정리·TUNING 행 추가)·QUICKSTART recall 뷰+flint state·README doctor 수 30 · [전체 변경 이력](docs/HISTORY.md)
> v1.38.3 · r143-angle3 패치 — 성능: resolved work_state 보존 정리(기본 90일)·배치 resolve(단일 UPDATE·resolved_at 기록)·dedup 질의 부분 유니크 인덱스 경로 복원·hot meta 질의 5→2·testengine scratch DROP 엔진다운 즉시 반환 · [전체 변경 이력](docs/HISTORY.md)
> v1.38.2 · r143-angle2 패치 — 보안: secrets 스캔이 resolved work_state 행까지 커버·work_state 원문 저장 제거(살균 전용 진입 등록)·stale-state 출력 render 경유·빈 --task exit 3(CLI=MCP) · [전체 변경 이력](docs/HISTORY.md)
> v1.38.1 · r143 패치 — claim 무결성: approve --claim 영속화(승격 행이 recall에서 소멸하지 않음)·write 시 claim 정규화·무효 어휘 거부·hot 스냅샷 예산 불변식(총합 ≤ 예산)·fixture 스키마 게이트 · [전체 변경 이력](docs/HISTORY.md)
> v1.38.0 · 지식 성장 모델 1단계 — claim 축(decision/procedure/lesson/event·event는 회상 제외)·work_state 테이블(의도는 open→resolve·무조건 기록)·수집 3단(유한 보류→Δ≠0 승격 심사→사후 증명)·approve claim 게이트·flint state CLI+MCP flint_work·hot 상태 스냅샷·testengine scratch 근본 수리 · [전체 변경 이력](docs/HISTORY.md)
> v1.37.1 · r137 패치 — G5 감사 경화: smoke 훅 실패 시퀀스 정지·demo-DSN·ops-DSN 가드 강화(host·port·db)·회귀 지반 판별력 케이스 추가·restore 살균 예외 문서화·smoke --check 옵트인 전환 · [전체 변경 이력](docs/HISTORY.md)
> v1.37.0 · 신뢰 기반 — 합성 회귀 지반(정확 일치 도달 판정·엔진 격리)·포이즈닝 회귀 3종 고정·쓰기 경계 계약(드리프트 게이트)·7단계 doctor smoke · [전체 변경 이력](docs/HISTORY.md)
> v1.36.1 · r136 패치 — G5 감사 경화: 파생 태깅 해시 도메인 정렬(실물 노트에서 불발이던 기능 수리)·계약 게이트 경화(경계값 완전 일치·영역 마커)·태그 충돌 수리·문장 단위 근거 검출 · [전체 변경 이력](docs/HISTORY.md)
> v1.36.0 · 추출 계약 — echo 처분 위계 계약 고정(드리프트 게이트)·엄격 원본 해시 일치 파생 태깅·규칙형 근거 동봉 강제(origin/status)·신뢰 tier 등록표(하향만·K2)·교정 입구 B안 결정 · [전체 변경 이력](docs/HISTORY.md)
> v1.35.1 · r135 패치 — G5 감사 경화: LLM import 게이트에 flint-waiver 주석 관례 구현(CI_REVIEW §6 정합)·F3~F5 감사 발견 수리(sync exit 계약·엔진 상태 키·훅 4종 문서)·import 블록 주석 파싱·FLINT_SLOW_LOOPS 상한 · [전체 변경 이력](docs/HISTORY.md)
> v1.35.0 · 작업 시스템+AX 감사 — 에이전트 실수 결정론 검사 5종(테스트 삭제·스킵·LLM SDK 린트·ci에 vet·lint·표면 계약·구조화 실패)·규칙↔집행 쌍 표·slow 원인 규명(native 엔진 커밋 시퀀싱·재시도 래퍼)·5축×44표면 AX 매트릭스 · [전체 변경 이력](docs/HISTORY.md)
> v1.34.1 · r134 패치 — G5 감사 경화: 세션 1회 가드 원자화(같은 라벨 병렬 시작 이중 주입 차단)·예산 단위 봉합 rune 통일(한국어 hot에서 능동 회상 기아 해소)·zero_hit_unowned_pct 행 지식 위생 영역 이동 · [전체 변경 이력](docs/HISTORY.md)
> v1.34.0 · 세션 시작 능동 회상 — 세션당 1회·hot 잔여 예산 내 재점검 대상 주입(새 훅 0)·provenance 접미(`source=·confidence=`) 단일 관례·`zero_hit_unowned_pct` abstention 지표·주입 최소 실증 원칙 문서화 · [전체 변경 이력](docs/HISTORY.md)
> v1.33.1 · r133 패치 — G5 감사 경화: doctor detail 경로 마스킹(훅 경로·서비스 잔재)·DNS 조회 기한·플러그인 버전 파싱 실패 warn 전환·serve 종료코드 테스트 · [전체 변경 이력](docs/HISTORY.md)
> v1.33.0 · `flint report --serve` — 동일 진단 원천 위의 localhost 전용 대시보드(ETag·요청 단위 엔진 연결·CSS 토큰 슬롯)·영역 F doctor 검사 ×5(훅 경로·DSN 분류·수집 신선도·버전 격차·서비스 잔재)·METRICS 원천 데이터 총목록+등록표 동기 게이트·`--html/--out` deprecated 경유 전환 · [전체 변경 이력](docs/HISTORY.md)
> v1.32.4 · r131 패치 — setup 면제 정확 서술(설계상 모든 경로 면제·--json 모드 포함)·통칭 문구 정정(QUICKSTART·README 양어) · [전체 변경 이력](docs/HISTORY.md)
> v1.32.3 · r130 패치 — --json 상한 문장을 실제 이중 모드 명령(read·pending)으로 한정·나머지 면제 명령은 모든 경로에서 면제 유지(setup은 설계상 — --json 모드도 1회성·tuning·maintain·export·report는 --json 플래그 없음) · [전체 변경 이력](docs/HISTORY.md)
> v1.32.2 · r129 패치 — 이중 모드 명령(read·pending)의 300s 면제는 대화형 호출만: --json 경로(MCP flint_read·스킬 스크립트)는 행 엔진(hung engine) 가드 유지·양 경로 Run 수준 probe 테스트·래핑 데드라인 테스트 실제 래핑·RUNBOOK 모순 정정 · [전체 변경 이력](docs/HISTORY.md)
> v1.32.1 · r128 패치 — QueryOnce가 래핑된 DeadlineExceeded도 영구 오류로(v1.32.0 상한이 죽은 ctx를 재시도하던 것)·사용자 대기 경로(TUI 폼·setup 다운로드) 300s 상한 면제(hooks와 동일)·FLINT_COMMAND_TIMEOUT QUICKSTART·RUNBOOK 문서화·릴리스 문서 수치 정정·분할 문자열 게이트 우회는 수용 선언(방언 게이트가 임계값 키까지 오탐 — 패턴 확장은 후속) · [전체 변경 이력](docs/HISTORY.md)
> v1.32.0 · 구조 청산 — 무기한 블록 자리에 context 데드라인(명령 상한 300s·FLINT_COMMAND_TIMEOUT 오버라이드·hooks 세션 경로는 무제한 유지·서비스 매니저 출력 조회 5s)·MCP Serve 초과행 재귀 대신 루프 재프레이밍(초과 행 통째로 스킵 — 잔여 바이트가 요청으로 재해석되던 결함 제거)·QueryOnce 영구 오류(context.Canceled) 즉시 반환(재시도는 일시 오류만·호출부 116곳 무변경)·thresholds 단일 소스(thresholdMetas 1행 편집이 reflect로 기본값·파싱·fallback 생성)·maintain 2,190→446줄 등록 슬라이스+모드 파일 6종(핸들러 바이트 동일 이동)·doctor 2,050→345+8파일(체크 7종+hookfix·등록 테이블 유지)·RunWrite 535→19줄(parse→validate→dedupe→persist→render)·스키마 게이트 1단계(lib/db 대표 쿼리 3종+TestSchemaSealedOutsideDB)·errLine 단일 경로+printErr 헬퍼·seam 규칙 문서화(docs/contracts/seam.md) · [전체 변경 이력](docs/HISTORY.md)
> v1.31.3 · r126 패치 — 업그레이드 왕복 진입부 가드(부분 와이어링 시 선제 ErrNotWired — 다운로드 성공 경로의 nil 심 패닉 제거)·성공 경로 회귀 테스트 커밋 · [전체 변경로그](docs/HISTORY.md)
> v1.31.2 · r125 패치 — 업그레이드 왕복 롤백의 nil 서비스 심 가드(부분 와이어링에서 에러 대신 패닉) · 실패 경로 회귀 테스트 커밋(파이프라인 실패 시 기존 모드 유지) · 미사용 상수 제거 · [전체 변경로그](docs/HISTORY.md)
> v1.31.1 · native-service — `flint engine status|start|stop`이 프로덕션 와이어링에서 등록된 서비스에 도달(관리형 컨테이너 compose · 네이티브 launchd/systemd user) · macOS 정지는 KeepAlive 재기동 대신 서비스 종료(bootout) · `setup --engine`은 파이프라인 성공 후에만 모드 기록 · CI macOS 스모크가 doctor·session-start에도 FLINT_CONFIG_DIR/FLINT_HOME 전달 · doctor 종수 24로 정정 · [전체 변경 이력](docs/HISTORY.md)
> v1.31.0 · 네이티브 엔진 — `flint engine status|start|stop` 서비스 제어 · `setup --engine managed-container|native|external`(옵트인 모드 선택 — 네이티브: 해시·버전 검증 다운로더·launchd/systemd user 서비스 등록·첫 기동 자격증명 초기화) · container→native 전환(실패 시 원복) · doctor `engine` 검사(등록·실행·버전·핀 정합) · R123 이월(로컬 파서 unknown-flag 에코 토큰 마스킹) · [전체 변경 이력](docs/HISTORY.md)
> v1.30.2 · r122 패치 — 문서가 무플래그 첫 설치 라우팅 정합(관리형 경로 포트 배정·단계 안내 수정)·unknown-flag 에코 토큰 마스킹(인라인 DSN 거부의 파서 누출 차단)·?port= 수기 엣지 선언 · [전체 변경로그](docs/HISTORY.md)
> v1.30.1 · 설치 감사 — 문서를 출시된 v1.30.0 구현에 맞게 정정(엔진 thresholds 3종은 코드 기본값·meta 등록은 후속 · pull/TLS/health-timeout 분류기·연결 수준 악수·외부 모드 purge 게이팅은 이 패치 추가 · compose v1은 처방 안내(폴백 실행 아님) · 포트 배정은 관리형 setup 경로(무플래그 첫 설치 포함)) · 네이티브 L0 스파이크 결론 정정(스펙 통과 기준 4항 잔여 — v1.31.0 G2에서 계획 수립) · doctor 종수 23으로 정정 · [전체 변경 이력](docs/HISTORY.md)
> v1.30.0 · 설치 여정 — `flint setup`이 엔진 compose 정의를 바이너리에 내장(프로젝트명 `flint`·다이제스트 핀 4방 동치 가드 — 저장소 체크아웃 불필요, 첫 실행이 pull·기동·ping+migrate를 한 번에 완결하고 원인별 오류 처방을 제공) · `--port` 자동 배정(5434부터 순차, 5499 예약) · 엔진 확인 악수(Doltgres·핀 검증) · `--engine-url` 외부 엔진 모드(루프백 한정·DSN 마스킹) · 기존 설치 수동 전환 절차(RUNBOOK §11) · 네이티브 엔진 L0 스파이크 통과(v1.31.0 착수) · [전체 변경 이력](docs/HISTORY.md)
> v1.28.1 · R119 패치 — T()가 ko에 없는 키에 en 폴백으로 응답 — 신규 키가 원시 키 문자열로 노출되지 않음(R119-2의 ≤24시간 캐시 참고 사항은 수용 확정·자동 해소) · [전체 변경로그](docs/HISTORY.md)
> v1.28.0 · 표면 확장·품질 청산 — TTY에서 무인수 `flint read`·`flint tuning`이 대화형 TUI로 진입(플래그 호출·비TTY·FLINT_NO_TUI는 현행 플래그 모드 그대로·read 계측 보존)·i18n KO 카탈로그(FLINT_LANG·usage 우선 적용)·테스트 통합(8도메인 버전 접미 56파일→8·스위트 592 불변)·BRAND 동향 회수·BREW tap 검토·서명 재실증 · [전체 변경로그](docs/HISTORY.md)
> v1.29.2 · r117 패치 — 격리 마무리: doctor engine-footprint skip 상세가 원인을 명시(미기동 전용 엔진 — "no running container publishes 5499" — 과 docker 부재 구분 가능)·전용 엔진 DSN 컨테이너 선택을 양방향 테스트로 고정 · [전체 변경로그](docs/HISTORY.md)
> v1.29.1 · r116 패치 — 격리 감사 수정: Makefile FLINT_TEST_DSN 기본값을 전용 엔진(5499)으로(운영 포트 기본값이 로컬 격리 무력화)·docker-compose.test.yml 이미지 다이제스트 고정(동치 가드)·엔진 컨테이너 선택 포트 기반(EngineMemMB·restarts의 오엔진 판독 방지)·tests/ 헬퍼의 FLINT_REALDB_REQUIRED 준수(전 경로 skip 0 센티널) · [전체 변경로그](docs/HISTORY.md)
> v1.29.0 · 물리 격리 — 테스트 전용 엔진(`docker-compose.test.yml` — 127.0.0.1:5499·`internal/testengine` lazy 기동 + 스크래치 수명주기 + 런 종료 purge·테스트는 운영 엔진 무접촉)·`maintain --purge-dropped`(복원 가능 DROP DB 목록 dry-run → 되돌림 불가 purge, 루프백 한정)·doctor `dropped_dbs`·`stray_dbs` 경고(thresholds `dropped_dbs_warn`·`stray_dbs_warn`·meta `stray_dbs_allow` 면제)·engine-footprint 재시작 횟수 표시 · [전체 변경로그](docs/HISTORY.md)
> v1.26.3 · 패치 — 주석 게이트: `internal/commentlint`(지시어 외 Go 주석이면 go test 실패)·`scripts/csweep.go`(AST 스윕 도구 repo 편입)·`make ci`의 `scripts/check_comments.py`(git 추적 셸·파이썬 전수·따옴표/heredoc 인식)·스크립트 주석 227건 삭제 · [전체 변경로그](docs/HISTORY.md)
> v1.26.2 · r113 패치 — 주석 스윕: 556파일·23,960 서술 주석 삭제(go/parser AST — 문자열 리터럴 미페이지·`//go:*`·`//nolint`·`// +build` 지시어 보존) · README 하이라이트 이력 복원(범프 스크립트 결함이 37줄 전량 라벨 교체) · 범프 스크립트는 배지만 동기(하이라이트 미조정) · release-gate 하이라이트 라벨 유일 검사 추가 · [전체 변경로그](docs/HISTORY.md)
> v1.26.1 · r111-r112 패치 — 명시적 검사의 캐시 갱신(`flint update --check`·doctor `--check update`가 latest·ETag·checked_at 기록 — doctor Fix 안내가 실제로 참) · 키워드형 DSN 파생 거부(exit 3 + FLINT_DEMO_DSN 안내) · FLINT_DEMO_DSN 오버라이드 시 base 서버 ensure 생략 · 오프라인 registry 폴백(캐시 사본+나이, 경고 아님) · 42P04 duplicate_database 관용(병렬 초회 seed) · [전체 변경로그](docs/HISTORY.md)
> v1.26.0 · 표면 신뢰 — 업데이트 캐시 3원칙(ETag 조건부 재검증: 304면 캐시 latest 승계·모순 검출기: 실행 버전보다 오래된 캐시는 즉시 재 fetch·`checked_at` `report --text` 노출)·`flint seed --demo`(내장 합성 데이터셋을 격리 `_demo` DB에 적재·`--reset --yes`는 데모 행만 삭제)·doctor `versions`(자가 버전·캐시 latest·PATH·mcp-registry 나란히)·threshold `update_cache_age_h` · [전체 변경 이력](docs/HISTORY.md)
> v1.25.1 · r108-r109 패치 — gc-pending JSON 마감 수정(`maintain --gc` 사전 안내 라인이 JSON으로 파싱됨)·방언 프로브 이전(`dolt_log_depth` SQL의 lib/db 소유 확정 — `db.DoltLogDepth`, doctor는 호출만)·깊이 프로브 numeric 수정(`commit_order::int8` + `CellToInt` pgtype.Numeric — 실엔진에서 깊이가 0으로 읽히던 결함)·`db_size` Doltgres 상시 skip 문서화·engine-footprint 타임아웃 2s→5s·단독 `--shallow` 문구 · [전체 변경 이력](docs/HISTORY.md)
> v1.25.0 · 엔진 운영 — `maintain --gc --yes`(dolt 가비지 컬렉션: 접속 단절 안내 → 재연결 루프 → 스키마 재검증 → meta 이력)·doctor `dolt_log_depth`(O(1))·`db_size` 경고 + `engine-footprint` 표시·RUNBOOK §10/§11·thresholds `dolt_log_depth_warn`·`db_size_warn_mb`·ANALYZE 폐기(무연산 실증)·audit_log 리플레이 스파이크 성공(순수 PG) · [전체 변경 이력](docs/HISTORY.md)
> v1.24.2 · r106 패치 — race-recheck echo 커버리지(레이스 재확인 중복의 echo 적립 fake 회귀 추가)·quiet-failure 회귀(`echo_candidates` INSERT 실패가 write를 실패시키지 않음)·echo 스캔 주석(pending 행의 기존 후보 포함 서술) · [전체 변경 이력](docs/HISTORY.md)
> v1.24.1 · r104-r105 패치 — `echo_candidates` 적립 회로 연결(write 시점 echo 후보 저장: 신규 행 id, 중복 재전송은 기존 행 기준)·merge-audit active 한정 페치(pending 배제)·`--merge`의 D4 처분 카운터 계상·int4 입력 가드(`--recall-audit --limit`·`read --related`) · [전체 변경 이력](docs/HISTORY.md)
> v1.24.0 · 지식 관리 잔여 — 스키마 v19(`kind`·`source_path`·`echo_candidates`)·`maintain --merge-audit`(중복 쌍 보고, 비용 추정 동반)·`--recall-audit`(나쁜 회상 환류 보고)·`read --related`(무방향 이웃 링크)/`--why`(토큰 tier·RRF 신호·제로히트 근거) · [전체 변경 이력](docs/HISTORY.md)
> v1.23.2 · r102 패치 — 카운터 자릿수 상한(shadow 카운터 업서트 18자리 가드)·v1.23.0 스펙 정렬(경계 발화·미옵 비용)·what-if p50/p90 도메인 필터 · [전체 변경 이력](docs/HISTORY.md)
> v1.23.0 · 자율 튜닝 shadow — 스키마 v18 `tuning_predictions`(예측 기록만, 적용 0건)·`auto_mode` 정책 스위치(기본 off, 화이트리스트: `suggest_min_score`)·read 꼬리 shadow 라운드(24h 게이트·streak 규칙)·`flint tuning what-if`/`status`(재분류·p50/p90·드리프트 카드) · [전체 변경 이력](docs/HISTORY.md)
> v1.22.1 · r96-r98 패치 — 병렬 재매치 스탬프 보존(해시 동일 재적용 시 유지)·maintain --recheck --conditions 갱신·flint distill 파일 상한(distill_max_runes)·동분 덮어쓰기 금지·id/min-score 입력 가드 · [전체 변경 이력](docs/HISTORY.md)
> v1.21.1 · r91-r94 패치 — 스캔 오류 경로 무관화·볼트 루트 가드(성공 후 영속)·튜닝 키 접속·스캔 경화(재시도·터치 갱신·심볼릭 링크 스킵·60k+ 삭제) · [전체 변경 이력](docs/HISTORY.md)
> v1.21.0 · 볼트 브리지 I — 스키마 v16 `vault_files`·`flint index`(스냅숏-디프, 디프마다 배치 1커밋)·`read` 볼트 통합(`source:"vault"`, 상한 5)·스캔 p95 계측 · [전체 변경 이력](docs/HISTORY.md)
> v1.20.3 · r89 패치 — merge·import 계보 복원의 supersede 락 도메인 편입(사이클 봉쇄) · [전체 변경 이력](docs/HISTORY.md)
> v1.20.2 · r88 패치 — advisory-lock 단일 커넥션 supersede·PAM 자기참조 링크 skip·절단 방어 테스트 · [전체 변경 이력](docs/HISTORY.md)
> v1.20.1 · r84-r87 패치 — 링크 적립 완전성(혼합 배치·PAM 왕복)·supersede 경합 강화·스키마 v15 링크 유니크 인덱스 · [전체 변경 이력](docs/HISTORY.md)
> v1.20.0 · 지식 스키마·검색 — 스키마 v14 지식 링크(refines·supports·contradicts·duplicates)·read `--min-confidence`/`--origin` 필터·supersede 사이클 가드·체인 뷰 · [전체 변경 이력](docs/HISTORY.md)
> v1.19.6 · observability-foundation 패치 — 스키마 v13(near_score·임계값 스냅숏·주간 롤업)·계측 신선도·커버리지 · [전체 변경 이력](docs/HISTORY.md)
> v1.18.3 · R69 — grammar fix · [full changelog](docs/HISTORY.md)
> v1.17.2 · r65-dedup — correction 해시 origin 제외 · [전체 변경 이력](docs/HISTORY.md)
> v1.17.1 · r64-observability — origin 추적 수정 · [전체 변경 이력](docs/HISTORY.md)
> v1.17.0 · echo — 쓰기 시점 모순 감지 · 관측 키 3종 · [전체 변경 이력](docs/HISTORY.md)
> v1.16.2 · R62 — highlight order contract tests (latest-first + EN/KO parity) · [full changelog](docs/HISTORY.md)
> v0.43.0 · 자동 업데이트 · go install · 전체 배포 폐회 · [전체 변경 이력](docs/HISTORY.md)
> v0.44.0 · PLAN 재무장 · 표면 계약 6종 · [전체 변경 이력](docs/HISTORY.md)
> v1.7.2 · 계측 귀속·sync 완화·probe heal(R38) · [전체 변경 이력](docs/HISTORY.md)
> v1.9.1 · R42 하드닝 — 다중 기기 이식(jsonl 회상 재생)·회상 이벤트 수명 관리 · [전체 변경 이력](docs/HISTORY.md)
> v1.9.2 · R43 회복성 — import 회상 이관 회복성·신규 DB 스키마 직행·문서 정합 · [전체 변경 이력](docs/HISTORY.md)
> v1.9.3 · TUI·성능 — 저화면 TUI 크롬 최소화·report 병렬 수집·tuning --tui 옵트인 · [전체 변경 이력](docs/HISTORY.md)
> v1.10.0 · 위생(hygiene) — 424개 파일 주석 영어화·slow 스위트 34.5% 단축·테스트 레이스 수정 · [전체 변경 이력](docs/HISTORY.md)
> v1.10.1 · R47 — v1.10.0 수치 정정 · [전체 변경 이력](docs/HISTORY.md)
> v1.10.2 · R48 — 기록 위생(중복 헤딩·순서) · [전체 변경 이력](docs/HISTORY.md)
> v1.11.0 · 지식화 파이프라인 1단계 — 사용자 교정 채널(`--source correction`) · [전체 변경 이력](docs/HISTORY.md)
> v1.11.1 · R50 — dup_correction 계량 수리 · [전체 변경 이력](docs/HISTORY.md)
> v1.11.2 · r52-reporting — dup_correction report 노출 · [전체 변경 이력](docs/HISTORY.md)
> v1.12.0 · 지식화 파이프라인 2단계 — 회상 실패 격차 후보 · [전체 변경 이력](docs/HISTORY.md)
> v1.12.1 · r54-docs — RUNBOOK 파이프라인 2단계 기록·NFKC 절단 수리 · [전체 변경 이력](docs/HISTORY.md)
> v1.13.0 · 지식 수명주기 — 낡음 점수·변동성 계층 · [전체 변경 이력](docs/HISTORY.md)
> v1.14.0 · 지식화 파이프라인 3단계 — 자동 교정 감지(A안 훅)·survived 감쇠 완화 · [전체 변경 이력](docs/HISTORY.md)
---

## 빠른 시작

```bash
# 사전 빌드된 바이너리 설치 (sha256 검증 포함) → ~/.local/bin
curl -fsSL https://raw.githubusercontent.com/synuns/flint/main/scripts/install.sh | bash

# Doltgres 기동(내장 compose·다이제스트 핀) · 스키마 마이그레이션
flint setup

# 첫 기억 기록
echo "API 게이트웨이는 멱등 POST를 2회 재시도한다" | flint write knowledge
```

선택 데모 데이터: `flint seed --demo` — 내장 합성 데이터셋을 격리 데모 DB(DSN의 db명 + `_demo`)에 적재합니다(멱등, `--reset --yes`는 데모 행만 삭제). 체크아웃한 저장소에서는 `make seed FLINT_SEED_DSN=postgresql://flint:flint-dev@localhost:5434/flint`로 명시적 DSN에 적재.

> 모든 릴리스 자산에는 SLSA 빌드 증명(build provenance)이 첨부됩니다 — 내려받은 파일은 `gh attestation verify <파일> --repo synuns/flint`로 검증할 수 있습니다.

### 바이너리 설치

네 가지 경로 — 하나만 고르면 됩니다. 어떤 경로든 다음 단계는 `flint setup`입니다: 엔진 compose 정의를 바이너리에 내장하고 있어 저장소 체크아웃 없이(필요한 건 Docker뿐) 임의 디렉터리에서 기동합니다.

**1 · 원라인 설치 스크립트** — 사전 빌드 바이너리, 체크섬 검증 포함:

```bash
curl -fsSL https://raw.githubusercontent.com/synuns/flint/main/scripts/install.sh | bash
```

플랫폼(darwin/linux × arm64/amd64)을 감지해 대응 릴리스 아카이브와 릴리스 `checksums.txt`를 함께 내려받고, 해당 자산 한 개의 sha256을 검증한 뒤 `~/.local/bin`에 설치합니다 — `--bin-dir DIR`로 재정의 가능. 필요한 건 `curl`·`tar`·`shasum`(또는 `sha256sum`)뿐, Go 툴체인 불필요. 대상 디렉터리가 `PATH`에 없으면 추가할 줄을 그대로 출력해 줍니다.

**2 · go install**:

```sh
go install github.com/synuns/flint/cmd/flint@latest
```

모듈 경로는 `github.com/synuns/flint`(v0.22.0+) — 버전은 빌드 정보(debug.ReadBuildInfo)에서 옵니다. 이후 바이너리 갱신은 `flint update`가 담당합니다.

**3 · 수동 다운로드 + 체크섬 검증** — [릴리스 페이지](https://github.com/synuns/flint/releases)에는 바이너리, 체크섬, 플러그인 zip이 자동 첨부됩니다:

```bash
ver=$(curl -fsSL https://api.github.com/repos/synuns/flint/releases/latest \
	| sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
asset="flint_${ver#v}_darwin_arm64.tar.gz"          # 조정: darwin|linux _ arm64|amd64
base="https://github.com/synuns/flint/releases/download/$ver"
curl -fsSLO "$base/checksums.txt" "$base/$asset"
line=$(grep " $asset\$" checksums.txt) && [ -n "$line" ] && echo "$line" | shasum -a 256 -c -  # no shasum? pipe to: sha256sum -c -
tar -xzf "$asset" && install flint ~/.local/bin
```

**4 · Homebrew(cask — tap 준비 중)**: flint는 사전 빌드 바이너리만 배포하므로 Homebrew 경로는 **formula가 아니라 cask**입니다 — goreleaser가 바이너리 릴리스의 formula 기반 배포를 폐기(deprecation, 2026)했고, cask는 릴리스 자산 그대로를 sha256 검증과 함께 설치하므로 수동 경로와 동일한 신뢰 사슬이 됩니다. tap이 열리면 `brew install --cask synuns/tap/flint` 형태가 되고, 준비는 릴리스마다 Cask 하나(`url` = 릴리스 자산, `sha256` = `checksums.txt`의 값)를 담은 `homebrew-tap` 저장소로 이뤄집니다. 그 전까지는 1·2번 경로를 이용하세요.

### Claude Code

```text
/plugin marketplace add synuns/flint
/plugin install flint@flint
```

플러그인은 `flint` 바이너리가 `PATH`에 있어야 합니다. 훅과 MCP 서버가 모두 이 바이너리를 호출합니다.

### 다른 에이전트 (Codex, Gemini, Cursor, OpenCode)

```bash
flint setup --mcp
```

설치된 하네스(Codex, Gemini, OpenCode, Copilot, Cursor, Qwen, Devin, Cline)를 감지해 각 네이티브 CLI로 MCP 서버를 등록합니다 — Copilot도 v0.21.0부터 자동 등록 대상입니다([docs/adapters/copilot.md](docs/adapters/copilot.md)). 파일 설정형 하네스(Windsurf, Zed, Amp, VS Code, Continue, Goose, Trae)는 doctor 체크와 어댑터 문서로 안내합니다. 미설치 하네스는 건너뛰고, 어떤 경우도 exit 0입니다.

<details>
<summary><code>flint setup</code>이 하는 일</summary>

내장 compose 정의(프로젝트명 `flint`·Doltgres 이미지 다이제스트 핀 — ADR-0020)를 임시 파일로 기록해 `--project-name flint`로 엔진을 띄우고, 같은 실행에서 스키마를 마이그레이션합니다 — 임의 디렉터리에서 동작합니다. 관리형 실행은 stdout에 JSON 상태 한 줄(`{"status":"ok","mode":"managed","port":N}` — 진행 노트는 stderr)로 마무리합니다. 호스트 포트 기본값은 5434이며, 관리형 경로는 `--port N`과 무플래그 첫 설치(기록된 DSN 없음)에서 동일하게 실행됩니다 — 기존 `flint` 프로젝트 설치는 포트를 승계하고, 요청 포트가 점유됐으면 루프백 바인드 시험으로 빈 포트를 순차 탐색해(5499 제외 — 전용 테스트 엔진 예약) config.json에 기록합니다. 기본 DSN은 `postgresql://flint:flint-dev@localhost:5434/flint`이며 `FLINT_DSN`으로 바꿀 수 있습니다. 직접 운영하는 Doltgres가 있다면 `flint setup --engine-url`로 기록합니다(루프백 한정 — DSN은 `FLINT_ENGINE_URL`이나 `--engine-url-file`로 전달, 인라인 값은 거부·출력은 마스킹). 정리는 `flint teardown`으로.

</details>

<details>
<summary>노출 프로필 설정</summary>

`flint setup --exposure normal|minimal`로 세션 시작 시 주입되는 맥락의 양을 조절합니다. `minimal`이면 헤드라인 한 줄만 나갑니다. 훅은 4개: SessionStart(`session-start`), PostCompact(`compact`), Stop(`session-end`), UserPromptSubmit(`correction_filter.sh` — 프롬프트 속 교정을 감지해 pending 지식으로 적립·비차단).

</details>

전체 가이드: [docs/QUICKSTART.md](docs/QUICKSTART.md)

---

## 동작 원리

<p align="center">
  <img src="assets/flint-architecture.svg" width="880" alt="flint 아키텍처 다이어그램">
</p>

세션마다 네 단계가 순환합니다:

| 단계 | 하는 일 |
| :--- | :--- |
| **주입** | 세션 시작 시 훅이 `flint hot` 실행 — 예산 내 맥락 블록 스트리밍 |
| **작업** | 에이전트가 CLI나 MCP로 읽고 씀 |
| **기록** | `flint write`가 해시 dedup으로 JSONL 추가 |
| **검증** | `flint audit`로 이력 확인 · `flint restore --drill`로 복구 훈련 |

기억이 썩지 않고 복리로 쌓이게 하는 세 가지 규율이 있다. **수집은 유한하다** — 검증 전 후보는 만료가 있는 pending으로 들어오고, 해시 dedup을 거치며, 세션 요약 원문은 절대 그대로 쌓이지 않는다. **승격은 판단이다** — `flint approve`는 세션이 "이것이 무엇을 바꾸는가"를 보일 수 있을 때만 후보를 지식으로 올린다. **가치는 사후에 증명된다** — 아무도 회상하지 않는 지식은 감쇠 점수·dead 점유 감사로 순위에서 빠진다. 손으로 청소하는 게 아니라 대사로 가벼워진다.

한 장으로 보면:

```
세션 시작 ──훅──► flint hot ──예산 내 [flint] 블록──► 에이전트 맥락
   │
   ├── flint write ──해시 dedup──► Doltgres (모든 변경이 커밋)
   │                                    │
   └── flint read / MCP 도구 ◄── 활성 뷰 RRF 검색
                                        ▼
                          audit · backup · restore · report
```

<p align="center">
  <img src="assets/flint-lifecycle.svg" width="880" alt="flint 세션 라이프사이클 — 기억이 축적되는 순환">
</p>

---

## 로컬 밖의 AI — 브리지

flint의 표면(훅·stdio MCP·localhost Doltgres)은 에이전트가 내 기기에서
실행된다고 가정한다 — 의도된 설계다([ADR-0013](docs/adr/0013-threat-model.md)).
그렇지 않은 AI — 클라우드 비서, 폰 앱 — 에는 커넥터 대신 브리지를 쓴다:

```bash
flint brief [query]        # 마스킹된 읽기 전용 블록 — 아무 채팅에 붙여넣어 전달
```

`brief`는 활성 지식만 조회한다(pending은 기기 밖으로 나가지 않는다).
비밀 패턴은 치환(`[REDACTED:…]`)하고 `[flint]` 데이터 블록 마커를 실어
수신 AI가 지시가 아닌 데이터로 취급하게 한다([ADR-0015](docs/adr/0015-bridge-surface.md)).
비서의 답을 가져올 때는 `flint write knowledge`에 confidence를
`conf_floor` 미만으로 넣어 회수한다(저신뢰 기록은 pending으로 분류),
또는 `flint import`(항상 pending) — 미검수 필자와 같은 검수 대기열에
떨어지고 `flint approve`로 승격된다. 원격 라이브 쓰기는 설계상 금지다.

---

## 왜 안전한가

flint는 여러분의 코딩 세션 안에서 동작하므로, 제약 조건은 부록이 아니라 제품
자체입니다([ADR-0013 위협 모델](docs/adr/ADR-0013-threat-model.md)):

- **로컬·오프라인.** flint·하네스·Doltgres 전부 내 기기에서 실행됩니다 — 클라우드 서비스도, 계정도, 텔레메트리도 없습니다. 유지보수 경로(`doctor`·`report`·`maintain`)는 오프라인 CLI입니다.
- **비간섭 5원칙.** (1) 주입은 최소, 검색은 풀 — flint가 먼저 말 걸지 않는다. (2) 훅은 비차단·타임아웃 — 세션 종료를 절대 막지 않는다. (3) flint 장애는 조용한 skip(exit 2) — 세션 시작·작업·종료를 지연시키지 않는다. (4) 경고는 임계값 기반 — 초과할 때만 말한다. (5) flint 데이터는 데이터 — `[flint]` 블록은 경계 밖에서 지시문이 될 수 없다.
- **주입 최소 — 실증 원칙.** 주입 효과는 주장하지 않고 측정으로 확인합니다 — 주입량(hot bytes)과 성공률(read 성공·fallback)의 상관은 주간 rollup 소비로만 계측하고, 새 실시간 쿼리는 두지 않습니다. 잔여 예산이 없으면 아무것도 주입하지 않습니다.
- **데이터 소유는 사용자.** 모든 데이터는 여러분의 Doltgres와 `~/.flint`에 있습니다 — 언제든 export(`flint export`, Ed25519 서명 가능한 PAM), `age` 암호화 백업, 기록·백업 시 비밀 스캔, 원격 라이브 쓰기는 설계상 금지([ADR-0015](docs/adr/0015-bridge-surface.md)).

---

## 커맨드

두 기록 유형이 나란히 산다: **지식**(세상에 대한 주장 — 게이트 있음: claim 없는 write는 pending으로 강제되고 `flint approve --claim`이 단일 승격 게이트)과 **진행 상태**(의도 — `flint state`로 무조건 기록, 끝나면 resolve, open 동안 hot 스냅샷에 합성). `flint read`의 회상 대상은 지식뿐이다 — event 행은 명시 조회(`--claim event`)로만.

| 커맨드 | 설명 |
| :--- | :--- |
| `flint setup` | Doltgres 기동(내장 compose·다이제스트 핀) · 마이그레이션 · 시드/플러그인 안내 — `--port N` · `--engine managed-container\|native\|external`(모드 선택 — 네이티브는 핀 버전 다운로드·플랫폼 서비스 등록·자격증명 초기화) · `--engine-url`(외부 엔진, 루프백 한정) |
| `flint engine` | 기록된 엔진 모드의 서비스 제어 — `status` · `start` · `stop`(모드×명령 행렬: managed-container는 내장 compose, native는 등록된 서비스, external은 안내만 — 미정의 조합은 안내와 함께 거부) |
| `flint seed --demo` | 내장 합성 데모 데이터셋을 격리 데모 DB에 적재(db명 + `_demo`, `FLINT_DEMO_DSN` 오버라이드; 멱등; `--reset --yes`는 `demo/` 행만 삭제 — 소유자 불일치 exit 3) |
| `flint update` | 최신 릴리스 자가 업데이트(체크섬 검증·`--check` 드라이런) |
| `flint tuning` | 임계값 키 조회·오버라이드(`list`/`get`/`set`/`reset`; 자율 튜닝 표면 `auto off\|shadow`·`what-if`·`status`); TTY 무인수 호출은 대화형 폼 진입(서브커맨드·플래그·비TTY·FLINT_NO_TUI는 현행 모드 유지·`--tui` 강제) |
| `flint write <kind>` | 표준 입력에서 지식 기록 (멱등) |
| `flint import [path]` | 규칙 파일(CLAUDE.md 등)을 pending 지식으로 변환(`--commit`); `--format jsonl\|pam` 지식 재이식·수입(jsonl 왕복·PAM v1.0) |
| `flint export [--format f]` | 지식 export jsonl·md·txt·csv·pam(PAM v1.0 표준 memory-store; `--status`·`--tag`·`--since` 필터, `--out` 파일 + `.sha256` 사이드카, PAM `--sign` Ed25519 서명 — 스펙 §18) |
| `flint index [--vault p]` | 마크다운 볼트를 `vault_files`에 색인(스냅숏-디프, 디프마다 배치 1커밋; `--vault` 경로 저장, `--limit` 스캔 상한) |
| `flint read <query>` | 회상 뷰로 활성 지식 검색 (claim `event` 행 제외 — 명시 조회는 `--claim <decision\|procedure\|lesson\|event>`; 색인된 볼트 행은 지식 블록 뒤에 추가); TTY 무인수 호출은 대화형 브라우저 진입(쿼리 입력·결과 목록·상세 전환); 옵트인 `--related N`은 무방향 지식 링크 이웃을 뒤에 추가(상한 5)·`--why`는 행별 근거 부록(토큰 tier·RRF 신호·제로히트 사유) — 플래그 모드 출력은 불변 |
| `flint hot --session <id>` | 세션 시작 맥락 주입 |
| `flint brief [query]` | 외부 AI에 붙여넣는 마스킹 브리프(읽기 전용) |
| `flint done --session <id>` | 세션 마무리 기록 |
| `flint approve --id n` | 대기 중 지식을 활성으로 승격 — 단일 claim 게이트: claim 없는 행(backfill `event` 포함)은 `--claim <decision\|procedure\|lesson\|event>` 필수(부재 시 exit 3); claim 없는 write는 confidence와 무관하게 pending |
| `flint state` | 진행 상태(의도) 접점 — `open --task T [--detail D] [--next A] [--project N] [--session LABEL]`(서버측 정규화 해시가 같은 open 과업의 재보고 흡수 → 한 줄 JSON `{id, status: open\|updated\|noop}`; 세션 라벨 — 플래그 우선·라벨 파일 폴백 — 로 `flint hot`이 현재 프로젝트의 스냅샷 행을 우선 정렬, 미해석 시 출력 불변) · `list [--all]` · `resolve --id N [--by K]`; open 행은 hot 스냅샷에 노출(상한 `state_snapshot_max`)·오래된 행은 `maintain --stale-state`가 승인제 resolve 제안 — 무조건 기록, claim 게이트 없음 |
| `flint maintain` | 관리: 임계값 확인 · 무효화 · 병합 · `--merge-audit` 중복 쌍 보고(multiset Dice·비용 추정 동반·리스팅만) · `--recall-audit` 나쁜 회상 환류 보고(리스팅만 — 각 행이 리포트 전용 `recommend` 필드(`supersede`·`expire`·`hold`)를 가짐 — 처분은 항상 승인제 서브커맨드로) · `--age-audit` 낡음 보고(suggested_vol 제안 동반) · `--reaffirm` 유효기간 갱신 · `--recheck` 조건 재검토 · `--gc [--shallow] --yes` dolt 가비지 컬렉션(운영자 명령 — 전 세션 접속 단절, 같은 실행 안에서 재연결+스키마 재검증) (`--tui` 대화형 검수, 옵트인) |
| `flint distill --out DIR` | 회상 점수가 임계 이상인 미출력 활성 지식을 마크다운 파일 1개로 출력(`--min-score`는 distill_min_score 대체; 출력 행은 distilled_at 스탬프 — 재실행은 잔여분만 — ADR-0018) |
| `flint audit --limit n` | 최근 n개 커밋 조회 |
| `flint backup` | SQL 덤프 + sha256 체크섬 (`--encrypt` age 저장 암호화·`--mask` 치환 진단 덤프) |
| `flint restore --file f` | 백업 복구 (`--drill`로 훈련) |
| `flint sync` | Doltgres 원격과 지식 동기화 — `remote add/list/remove` (url 마스킹), `push`/`pull [--dry]`; 병합 충돌 시 exit 1과 수동 해결 안내; 원격 url은 어떤 출력·로그에도 남지 않는다 (사람 운영자 명령) |
| `flint profile` | 프로파일 판정: 근거 지표+추천, `--apply normal\|minimal` |
| `flint status` | 스키마 버전 · 대기 건수 · hot p95 |
| `flint mcp` | MCP 서버 (read·write·hot·pending·approve·work 도구) |
| `flint pending` | 대기 지식 검수 뷰 (승격·병합·만료·보류 판정, `--json`) |
| `flint compact` | compact 요약을 pending 지식으로 적립 (PostCompact 훅 진입점) |
| `flint nudge` | compact가 남긴 마커의 세션 종료 증류 리마인더 (`--clear` 종결) |
| `flint doctor --check c` | 건강 검진 23종 (`--check` 서브셋, fail 시 exit 1) |
| `flint report --days n` | 일별 유입·zero_pct·p95·stale 리포트 (`--html` 정적 대시보드) |
| `flint diff <from> <to>` | 두 as-of 지점 간 지식 변경 비교 |
| `flint teardown` | 설정 해제 (훅·설정, `--purge` 시 데이터도 제거) |
| `flint version` | 버전 출력 |

**종료 코드:** 0 성공 (`read` 제로히트도 exit 0 — 텍스트는 침묵, `--json`은 `{"hits":0}` 한 줄) · 1 오류 · 2 저장소 장애 · exit 3 — 잘못된 입력 (값 없는 trailing 플래그, 잘못된 `--status`·`--as-of` 값, `write` 스키마 위반).

`flint read`는 구문 검색이 무결과면 단어 단위 fallback으로 재시도합니다 — fallback 재정렬은 행별 recall 횟수를 additive 신호로 융합해, 동점에서는 회상이 많은 지식이 위로 갑니다.

기억처럼 감쇠하는 기억: `read`가 적중할 때마다 recall_score가 강화되고, 미사용
항목은 매일 감쇠(하한 0)하며 `hot`/`brief` 순위에 반영된다 —
`flint maintain --decay-audit`는 archive 후보를 제안하고, 실행은 항상 승인제다.

### 대화형 TUI (v1.5.0)

`flint setup`(무인자, 이미 설정된 기기) 위저드와 `pending`·`export`·`report`의 4화면. DSN이 기록되지 않은 무플래그 첫 설치는 위저드 대신 관리형 엔진 경로로 진입한다(포트 배정 · 엔진 기동 · 마이그레이션 — JSON 상태 한 줄로 마무리). 터미널에서는 기본 대화형 — 무인자 실행 시 TUI가 열린다(v1.5.2부터). 파이프·비터미널 실행과 플래그 지정 시 기존 출력이 그대로 유지된다. `flint maintain --tui`(v1.6.0)는 옵트인 관리 화면 — 의심 목록 → 병합 시 무엇이 남는지 미리보기 → 병합/유지/무효화, 실행은 기존 서브커맨드에 위임.

운영: [docs/RUNBOOK.md](docs/RUNBOOK.md) · 튜닝: [docs/TUNING.md](docs/TUNING.md) · 메트릭: [docs/METRICS.md](docs/METRICS.md) · 성능: [docs/PERF.md](docs/PERF.md) · 문서 지도: [docs/README.md](docs/README.md)

---

## 에이전트 & 어댑터

아래 어댑터는 전부 로컬 실행이다 — "MCP 지원"은 `flint` 바이너리를 spawn할
수 있는 로컬 MCP 클라이언트를 뜻한다. 로컬 런타임 없는 AI는 [브리지](#로컬-밖의-ai--브리지)를 쓴다.

| 에이전트 | 방법 |
| :--- | :--- |
| **Claude Code** | 플러그인: 훅 + `/flint` 커맨드 |
| **Codex** | MCP ([docs/adapters/codex.md](docs/adapters/codex.md)) |
| **Gemini** | MCP ([docs/adapters/gemini.md](docs/adapters/gemini.md)) |
| **Copilot** | MCP ([docs/adapters/copilot.md](docs/adapters/copilot.md)) |
| **Cursor** | MCP ([docs/adapters/cursor.md](docs/adapters/cursor.md)) |
| **OpenCode** | MCP ([docs/adapters/opencode.md](docs/adapters/opencode.md)) |
| **Qwen Code** | MCP ([docs/adapters/qwen.md](docs/adapters/qwen.md)) |
| **Devin CLI** | MCP ([docs/adapters/devin.md](docs/adapters/devin.md)) |
| **Cline** | MCP ([docs/adapters/cline.md](docs/adapters/cline.md)) |
| **Windsurf** | MCP ([docs/adapters/windsurf.md](docs/adapters/windsurf.md)) |
| **Zed** | MCP ([docs/adapters/zed.md](docs/adapters/zed.md)) |
| **Amp** | MCP ([docs/adapters/amp.md](docs/adapters/amp.md)) |
| **VS Code** | MCP ([docs/adapters/vscode.md](docs/adapters/vscode.md)) |
| **Continue** | MCP ([docs/adapters/continue.md](docs/adapters/continue.md)) |
| **Goose** | MCP ([docs/adapters/goose.md](docs/adapters/goose.md)) |
| **Trae** | MCP ([docs/adapters/trae.md](docs/adapters/trae.md)) |
| **OpenClaw** | 어댑터 ([docs/adapters/openclaw.md](docs/adapters/openclaw.md)) |
| **Hermes** | 어댑터 ([docs/adapters/hermes.md](docs/adapters/hermes.md)) |

어댑터 문서: [docs/adapters/](docs/adapters/)

---

```
flint/
├── cmd/flint/          # 진입점
├── lib/                # store · query · db · cli · backup · mcp
├── flint-plugin/       # Claude Code 플러그인
├── docs/               # PLAN · RUNBOOK · TUNING · ADR · 어댑터
└── docker-compose.yml  # Doltgres 5434
```

---

## 라이선스

flint는 오픈소스로, [MIT 라이선스](LICENSE)로 배포됩니다.

---

<p align="center">
  <em>사람의 노트는 Obsidian에, 에이전트의 기억은 flint에.</em>
  <br/><br/>
  <strong>F L I N T</strong>
</p>
