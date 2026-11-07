# flint 버전 이력 (아카이브)

§14 마일스톤의 과거 엔트리(v0.1~v0.18.x) — v0.21.2(패치 루프 8차·
마일스톤 C③)부터 PLAN.md은 현행 맥락(효과 계측 시대 v0.19.0+)만
유지하고 이력은 여기에 보관한다. 내용은 이동 시점 그대로(무수정).

### v0.1 코어 루프 (2026-10-21 완료)
- [x] 기술검증 3건 → Doltgres 구동·스키마 → write/read/hot/status
- [x] 수동 백업(덤프) → Claude Code 플러그인(훅+스킬) → 시딩
- 시딩: 활성 프로젝트 3건(데모 게임 프로젝트·데모 유틸 프로젝트·데모 스크립트 모음) + flint 기획 결정
  일괄 + 사용자 선호(출력 스타일)
### v0.2 운영 편의 (2026-10-21 완료)
- [x] approve 정식화 / maintain 트리거 / done 임계값 경고 / 백업 자동
### v0.3 확장 (2026-10-22 완료)
- [x] flint mcp / Hermes 카탈로그·OpenClaw 어댑터 / 임계값 튜닝
### v0.4 운영 콘텐츠·계측 (2026-10-22 기획·완료)
- [x] 콘텐츠 정책 v2 구현 — CONTENT.md(유형 8종·플레이북 계약·증류 루프·어휘표) write 검증 반영:
  어휘 밖 tags/domain 경고, seed 정합 승격
- [x] 자가발전 루프 — maintain 세션에서 패턴 탐지(성공 2회·실패→성공)→플레이북 후보 제안,
  done 시 플레이북 사용 로그·성공률 기록, 성공률 기반 무효화 후보 보고
- [x] 계측 영속화 — hot 길이 통계를 meta에 누적(현재 프로세스 내 한정 결함:
  CLI 단발 프로세스라 hot_calls가 매 실행 초기화됨), p95 실측 실현
- [x] hot 예산 meta화 — 2,000자 상수 → meta hot_budget(TUNING.md 예약 키 회수)
- [x] setup/teardown 원커맨드 — §9 약정 중 미구현: docker compose up·Migrate·초기 시딩·
  플러그인 설치 안내 원커맨드
- [x] 세션 자동 요약 — done 시 sessions 테이블 자동 기록(수동 write 의존 제거)
- [x] MCP 뷰 캐시 방어 — 장수 mcp 서버 프로세스의 stale 뷰 describe 위험:
  read 경로 simple protocol 격리 또는 스키마 버전 감시·풀 재생
- [x] 보안 구조 방어(ADR-0006) — 메모리 포이즈닝·비밀 유출 대응:
  입출력 살균·비밀 패턴 스캔 경고, 백업 sha256 무결성+restore 가드, origin 필터·분포
- [x] CI·릴리스 파이프라인 — GitHub Actions(go test + make e2e, Doltgres 서비스)·
  GoReleaser·릴리스 워크플로(§12 약정 이행)
- v0.5 이연: 백업 로테이션·보존 정책 / 검색 품질(랭킹·태그 제안)
- 후보(운영 데이터 확보 후): 병렬 쓰기 충돌 실측 스파이크·복구 훈련 자동화

### v0.5 신뢰성·검색 품질 (2026-10-22 기획·완료)
- [x] mcp CLI 연결 — `flint mcp` 서브커맨드 등록+usageLine 갱신.
  검증은 실 bin/flint 프로세스 e2e(JSON-RPC 왕복) — 라이브러리 직접 호출 테스트만으론
  배선 결함이 새어감(v0.4 부채: .mcp.json이 unknown command로 즉사)
- [x] 병렬 쓰기 충돌 실측 — N=8 동시 write 프로세스: 통과기준 = exit 1(재시도 소진)
  0건 · 전량 정합 커밋(손실·중복 0). 충돌→재시도 성공률 측정치 ADR화 + e2e 회귀화
- [x] 스키마 인덱스 — 스파이크 선행: CREATE INDEX·함수 인덱스(lower) Doltgres
  지원 확인 + ~10k 행 read p95 측정. 지원·효용 입증 시 schema v3 적용,
  무효 시 "시퀀셜 스캔 수용" ADR로 종결(에이전트 기억 규모 수천 건)
- [x] 감사·롤백 루북 — `flint audit` 신설(dolt_log 최근 20, --session 필터) +
  RUNBOOK.md(오염 감지→dolt_revert 절차·복구 훈련 절차). revert 커맨드화는
  사용 빈도 입증 후(룰링 — 문서 루북 우선)
- [x] 복구 훈련 정례화 — `flint restore --drill`: 덤프를 임시 스키마에 재생·행수
  대조·maintenance_runs 기록. maintain --check에 30일 경과 보고(meta last_drill)
- [x] 백업 로테이션·보존 — meta backup_keep(기본 14): backup 성공 후 구덤프+사이드카
  삭제. "DB 행은 전 보존, 파일 덤프는 keep-N" 원칙 구분 명시(TUNING.md §15 연계)
- [x] 검색 품질(v0.5 범위 축소) — ①제로히트 계측: read 0건 비율 meta 누적·status
  노출 ②랭킹: topic 정합 > confidence > 최신 순 정렬.
  n-gram·코사인은 트리거(제로히트율 20% 초과·read_calls≥50) 도달 시 별도 이연

### v0.6 적립 가시성·데이터 유지보수 (2026-10-22, 스펙 §1·§2)

적립 가시성(§1):

- [x] 유입 계측: write/hot/read 성공 실행마다 meta 기록 — last_write_at·
  last_hot_at·last_read_at(RFC3339) + 일별 키 `daily:YYYY-MM-DD` JSON
  {"write":N,"hook":N,"manual":N,"mcp":N,"dup":N,"hot":N,"read":N}.
  14일 초과 키 자동 정리. write 소스 = `--source hook|manual|mcp`
  (기본 manual·무효값 exit 3), 플러그인 훅은 hook, MCP 경로는 mcp,
  duplicate 응답은 dup 별도 증분.
- [x] status 확장: last_* 3필드 추가(기존 필드·순서 불변, 미설정 키는 null).
- [x] stall 진단(주기+grace 이원 모델, Healthchecks.io 준용): last-seen =
  heartbeat(nodata 패턴 — 성공 신호의 부재 감지). 경고 = stall_hot_h
  (기본 48h) 초과 hot 부재·stall_write_h(기본 72h) 초과 write 부재.
  warn은 exit 0 유지.
- [x] `flint doctor`: 검사 5종 — hook-file(훅 설정·플러그인 캐시에 flint
  등록 여부, FLINT_CLAUDE_HOME 오버라이드)·db(Ping)·schema(버전 4 일치)·
  integrity(flint_knowledge_* 인덱스 3행)·last-seen(3경로 경과+grace).
  brew doctor 준용: --list-checks·--check NAME 서브셋, fail만 exit 1·
  warn은 0, fail마다 수정 명령 한 줄 제시.
- [x] `flint report [--days N(기본 7·상한 14)]`: 일별 유입(소스·dup 구분)·
  zero_pct·hot p95·pending/active/invalidated·stale 수·기간 무효화 수.
  한 줄 JSON.

데이터 유지보수(§2):

- [x] 항목 참조 계측(schema v4): knowledge.read_count int NOT NULL DEFAULT
  0·last_read_at timestamptz. read 결과·hot 주입 항목 id 전수 증분
  (BumpReads). 신규 인덱스 없음(status 인덱스 재사용·개인 규모 시퀀셜
  수용). 계측 실패는 조회를 깨지 않음(조용히).
- [x] `flint maintain --stale [--days N] [--limit M(기본 20)]`: active 중
  (last_read_at < now-N일) 또는 (read_count=0 AND ingested_at < now-N일).
  미참조 버킷(read_count=0) 구분 표시. 보고 전용·cron 친화적·exit 0.
  처리는 기존 maintain --invalidate(무효화=가역). 물리 삭제·자동
  confidence 변경 없음(§15 이연 유지).
- [x] 임계값: stale_days 기본 90(Confluence 스테일 스캔 표준·분기 리뷰
  관례)·stall_hot_h 48·stall_write_h 72 — Thresholds 확장 + meta
  오버라이드(기존 체계 재사용, TUNING.md §2).
- [x] 랭킹 tie-breaker: 기존 순위(topic 정합>confidence>최신) 뒤에
  last_read_at DESC NULLS LAST만 추가(조회시 파생·파라미터 0개·가역).
  가중 freshness 스코어는 민감성(arXiv 2509.19376 자체 보고)으로 이연 —
  트리거 read_calls≥50.
- [x] 유지보수 경로(maintain·report·doctor)는 전부 오프라인 CLI —
  핫패스 비간섭(Letta sleep-time 준거).

### v0.7 적립 자동화·실사용 재개 (2026-10-23 기획)

- [x] 자동 write 훅 — 세션 종료/컴팩트 시점 자동 증류 적립(메커니즘은 레퍼런스
  조사로 룰링: PreCompact 훅 vs SessionStart 넛지 vs 스킬 주도)
- [x] distill 스킬(플러그인) — 에이전트 자가 기억 추출·write --source hook
- [x] dup 대응 강화 — 자동화로 늘어나는 중복의 병합 제안 보고
- [x] 포트/DSN 설정 — FLINT_DSN 우선·설정 파일·compose 오버라이드 가이드
  (5433 점유 시 실사용 재개 조건)
- [x] 테스트 DSN 미설정 조기 실패
- [x] doctor 확장 — 자동 적립 훅 등록 검사·포트 충돌 진단
- [x] setup 원커맨드 보강
- 후보(v0.8): 시간여행 노출(read --as-of·diff·status --as-of — §16 검증
  전제) / 수명주기 액션(maintain 배치 무효화 dry-run·보존 정책 — stale
  실측치 축적 후)

### v0.8 시간여행 노출·수명주기 액션 (2026-10-23 기획)

시간여행 노출:

- [x] `flint read --as-of <시점>` — 시점 지정 조회(AS OF 쿼리, §16 검증 전제)
- [x] `flint diff` — 두 시점 knowledge/sessions 행 비교 보고(한 줄 JSON)
- [x] `flint status --as-of <시점>` — 시점 스냅샷 상태
- [x] audit 연계 — dolt_log 기반 시점 목록으로 as-of 후보 보조

수명주기 액션:

- [x] maintain 배치 무효화 — stale 항목 일괄 무효화 dry-run 기본·실행은
  명시 플래그(무효화=가역, 기존 원칙 승계)
- [x] 보존 정책 — invalidated 항목 보존 기한 관리(물리 삭제는 §15 이연 유지)
- [x] superseded 개념 — 갱신 지식 적립 시 구 항목 무효화 연결(Zep
  invalidated-not-deleted 준거, v0.7 증류 스킬 4연산과 연계)

### v0.9.2 2차 코드리뷰 수정 (2026-10-27 완료, PR #9)

- [x] 코드리뷰 지적 8건 — 레이스 ErrDuplicate 재확인 skip·잔여 재시도,
  agentForSession UUID만 claude 매핑, Classify 전역 상태 제거(conf_floor
  0~1 단일 게이트), dup 계측 일괄 증분(RecordUsageCount) 등
- [x] project kind 멱등 회귀 수정 — 자연키(name) 사전 dedup,
  ExistsProject 배선으로 시딩 멱등 복원

### v0.9.3 운영 정착 (2026-10-28 기획·완료)

- [x] R1 테스트 격리 — regression 고유 스키마·DSN, e2e DSN 헬퍼 교체
- [x] R2 실DB 정크 정리 — regression 잔여 행 삭제
- [x] R3 duplicate 응답 id 노출 — `{"status":"duplicate","id":N}`
- [x] R4 maintain 중복 그룹 보고 — `{"status":"dup-suspects","groups":[{"topic":T,"ids":[…]}]}`
- [x] R5 문서 동기화 — README(EN/KO) 배지 v0.9.3·아키텍처/라이프사이클
  SVG 임베드, 마일스톤 기록

### v0.9.4 훅 자가수복 (2026-10-28 기획·완료, PR #12) — 소급 기록

- [x] R1 훅 자가수복 — `doctor --fix`가 훅 스크립트 훼손·부재 시
  복구(멱등, 기존 진단 경로 승계)
- [x] R2 문서 동기화 — RUNBOOK 훅 자가수복 절차, 마일스톤 기록

### v0.9.5 채택 품질 (2026-10-29 기획·완료, PR #13)

- [x] R1 중복 절감 — SKILL.md 규칙 3종 추가(프롬프트 토큰 절감)
- [x] R2 read 촉진 — nudge 힌트 문구 반영(v0.7 계약 문구는 불변)
- [x] R3 설정 신규 생성 — `doctor --fix`가 settings.json 부재 시
  최소 뼈대 생성(백업 없음, 기존 merge·멱등 경로 승계)

### v0.9.6 검색 고도화 (2026-10-29 기획·완료)

- [x] R1 토큰 폴백 — read 구문 제로히트 시 단어 단위 OR 재검색·
  정합수 재랭킹, `read_fallback_calls` 계측
- [x] R2 계측 노출 — status JSON에 `read_fallback_calls` 필드
- [x] R3 문서 동기화 — README(EN/KO) read 폴백 1줄, 마일스톤 기록

### v0.9.7 전파·이전 정리 (2026-10-29 기획·완료, PR #15) — 소급 기록

- [x] R1 스킬 싱크 — `setup --update-rules`로 ~/.claude/skills 재동기화
- [x] R2 포트 5434 — Doltgres 포트 충돌 회피 기본값 이전
- [x] R3 배지·관측 — README 배지 갱신·관측 지표 보강
- [x] R4 문서 동기화 — 마일스톤 기록

### v0.10.0 검색 품질·백업 신뢰성 (2026-10-30 기획·완료, PR #16(예정))

- [x] R1 did-you-mean — read 제로히트 시 근접 후보 제안(suggestions)
- [x] R2 NFKC 유니코드 정규화 검색 — 매칭 비교 문자열 양측 정규화
- [x] R3 자동 백업 사이드카 — `flint done` 자동 백업 sha256 사이드카
  무결 검증
- [x] R4 doctor backup-age — 마지막 백업 72h 초과 시 warn 진단
- [x] R5 문서 동기화 — README(EN/KO) 배지 v0.10.0·제안/무결성 1줄,
  마일스톤 기록

### v0.11.0 노출 최소화 (2026-10-31 기획) — 프로파일·요약 주입·nudge 분기·훅 통합

- [x] R1 노출 프로파일 — `flint setup --exposure`로 주입 노출 수준 지정
- [x] R2 hot 요약 줄 — 세션 시작 주입을 요약 줄로 최소화
- [x] R3 nudge 분기 — 종료 시점 증류 안내 분기 처리
- [x] R4 훅 통합 — 플러그인 훅 3엔트리 통합
- [x] R5 문서 동기화 — SKILL/README(EN/KO) 노출·펼침 안내, 마일스톤 기록

### v0.12.0 pending 열람 완결·버전 파생 (2026-10-31 기획·완료) — read --status·VERSION 태그

- [x] R1 read --status — pending 상태 조건 열람(--as-of 병용 시 as-of 우선)
- [x] R2 make build VERSION 태그 — 최근 git 태그에서 버전 파생(태그 부재 시 dev 폴백)
- [x] R3 문서 동기화 — README(EN/KO) 배지 v0.12.0·read --status·make build
  1줄, SKILL pending 열람 안내, 마일스톤 기록

### v0.13.0 지능 축 (2026-10-31 기획) — 채택 재랭킹·모순 탐지

- [x] R1 채택 재랭킹 — RankOrder 최종 타이브레이크 read_count DESC
  (안정 정렬·기존 순서 존중)
- [x] R2 maintain --conflict-suspects — 동일 topic 요약 유사도 0.6
  미만 그룹 리스팅(dup-suspects 출력 승계 + 요약 앞 40자)
- [x] R3 doctor conflicts 검사 — 모순 후보 그룹 1+ warn(안내:
  maintain --conflict-suspects)·0이면 ok
- [x] R4 문서·TUNING 기록 — README(EN/KO) 채택 재랭킹·conflict-suspects
  1줄, TUNING conflictDupThreshold, 마일스톤 기록

### v0.13.2 패치 정합 (2026-11-01 기획) — 심층 리뷰 2차 수정

- [x] 프로세스 룰: 각 릴리스의 마지막 커밋에 README(EN/KO) release 배지
  갱신 포함(배지 가드는 최신 태그·다음 예정 버전 허용 — 커밋→태그 창 레드 없음)

### v0.14.0 패치 (2026-11-04 기획) — 제로히트 침묵 계약·zero_near 계측

- [x] R1 제로히트 침묵 계약 — read 재검색까지 제로히트 시 무출력·
  exit 1(제안 출력 폐지)
- [x] R2 zero_near 판별 계측 — 최고 근접 점수 suggestMinScore 0.15
  이상 제로히트를 zero_near로 계측
- [x] R3 report 노출·문서 정합 — report에 zero_near 계열 필드 노출,
  README(EN/KO) 침묵 계약 서술 갱신, TUNING zero_near 소절, 배지
  v0.14.0

### v0.15.0 우아함 축 (2026-11-05 기획) — 핫패스 계측 단일화·지표 기간화·진단 폐쇄

- [x] R1 핫패스 계측 단일화 — read_stats 단일 읽기-수정-쓰기
  (BumpReadStats 통합), 계측 왕복 예산 ≤ 5(병합 upsert)
- [x] R2 zero 지표 기간화 — daily read_zero·read_zero_near 키·
  report --days 창 합산 산출(zero_pct·zero_near_pct, 누적 read_stats
  오염 절연)
- [x] R3 doctor zero_near 검사 — 최근 7일 daily 합산 표본 read ≥ 10 &&
  zero_near 비율 ≥ 30%면 warn(어휘 불일치 의심), 체인 말미 등록
- [x] R4 계약 문서화 — README(EN/KO) exit 코드 표(0/1/2/3)·TUNING
  기간화 지표·doctor 임계·왕복 계약 소절, 배지 v0.15.0

### v0.15.1 패치 (2026-11-05 기획) — e2e 침묵 계약 잔존 청산·CI 그린 복구

- [x] R1 e2e 주입 케이스 계약 정합(exit 1·완전 무출력)·R2 배지 v0.15.1
- 태그는 #27 머지 커밋에 지연 발행(검증 스크립트 결함으로 그 시점 CI 레드 —
  v0.15.2에서 수정)

### v0.15.2 패치 (2026-11-05) — CI 빚 청산: 검증자·프로비저닝·진단성·드릴 가드

- [x] R1 validate_yaml 포트 단얱 5433→5434(v0.9.3 포트 전환 잔존 스테일 —
  CI test 잡은 #20 이상 전부 FAILURE였음이 판명, 즉시머지가 은폐)
- [x] R2 CI flint_test 프로비저닝(psql CREATE DATABASE — 서비스는 flint만
  생성)·GO_TEST_FLAGS 훅 추가(직렬화 필요 시 즉시 적용 가능한 예비선,
  현행은 평행 — 신규 DB 로컬 전수 그린 실측)
- [x] R3 진단성 — db.Retry 소진 시 마지막 오류 래핑(기존 폐기로 원인
  은폐)·드릴 테이블 제거 실패 시 오류 노출
- [x] R4 드릴 가드 requireDropVisibility — doltgresql 업스트림 결함(신규
  초기화 스토리지에서 DROP TABLE 무음 no-op·잔존, 1.3.3·1.4.0 실측)시
  드릴 스킵. 정상 엔진 전량 실행. 업스트림 이슈 후속.
- [x] R5 배지 v0.15.2·v0.15.1 태그 #27 머지 커밋에 지연 발행

### v0.16.0 (2026-11-05) — 릴리스 자동화·플러그인 마켓플레이스 배포

- [x] R1 릴리스 자동화 — 태그 push 시 Release 자동 생성(바이너리·
  checksums·플러그인 zip)
- [x] R2 플러그인 매니페스트 — `.claude-plugin/` 2종으로 마켓플레이스
  배포(`/plugin marketplace add synuns/flint` → `/plugin install flint@flint`)
- [x] R3 문서·배지 — README(EN/KO) 설치 섹션(릴리스 바이너리·플러그인
  설치·기존 setup 병기)·배지 v0.16.0
- 원칙 갱신: 태그 push가 곧 사용자 확인 — Release 자동 생성(v0.16.0부터
  수동 릴리스 절차 없음)

### v0.16.1 패치 (2026-11-05) — CI 리뷰 10건·플러그인 배포 결함

- [x] HIGH-1 release 잡 `needs: test` — 테스트 그린 후에만 릴리스 발행
- [x] HIGH-2 `.mcp.json` command `bin/flint`→`flint` (PATH — hooks와 기준
  통일, 배포 zip에 바이너리 없어도 MCP 기동)
- [x] HIGH-3 linux 교차빌드 — `goos`에 linux(amd64·arm64) 추가
- [x] MED goreleaser 버전 고정(latest 부양 제거)·`workflow_dispatch`
  트리거(재발사 외 재시도 경로)·doltgres digest 고정·잡별
  `timeout-minutes`(test 20·release 15)
- [x] LOW psql 준비 실패 `|| echo` 위장 삼킴 제거(exit 1)·플러그인 zip에서
  `*_test.go` 제외·validate_yaml이 release 잡·hooks.json 검사
- [x] 문서 — 배지 v0.16.1(EN/KO)·plugin.json 0.16.1 동기·README PATH
  요구 문구에 MCP 포함

### v0.16.2 패치 (2026-11-05 기획) — 드릴 계약 완결·pending 만료·쓰기 프로토콜

- [x] R1 드릴 계약 완결 — DROP 잔존 검증(수동 DROP 안내)·지연 정리
  독립 ctx·regression 단얱 갱신
- [x] R2 CI CREATE 가드 — flint_test 생성 실패 시 already exists 외
  exit 1(진단 위치 보존)
- [x] R3 헬퍼·검증 정리 — docs 공유 docStr·flint-plugin 공유 readDoc
  통합·validate_yaml 매니페스트 파싱 1회화
- [x] R4 pending 만료 — `maintain --expire-pending <days> [--commit]`
  (dry-run 기본·invalidated 전환)·doctor 만료 안내
- [x] R5 쓰기 프로토콜·문서 — SKILL 쓰기 우선순위 3계층(갱신 우선·
  최소화·read 선행)·배지 v0.16.2(EN/KO)

### v0.16.3 패치 (2026-11-05 기획) — 튜닝값 레지스트리화

- [x] 튜닝 상수 7종 Thresholds 레지스트리화 — suggest_min_score·
  suggest_scan_cap·conflict_dup_threshold·backup_age_warn_h·
  zero_near_min_read·zero_near_warn_pct·pending_age_days(기본값은
  thresholds.go 단일 소스·meta 오버라이드 우선)·TUNING.md §2 갱신·
  SKILL.md 개인 관측 인용 제거·CLAUDE.md 방어 계층 1줄·배지 v0.16.3(EN/KO)

### v0.16.4 패치 (2026-11-05) — release 잡 멱등 가드

- [x] 이미 발행된 태그는 자산 업로드 스킵(422 already_exists 방지 —
  이력 재작성의 태그 재푸시로 발견)·배지·plugin.json 동기

### v0.16.5 패치 (2026-11-05) — setup --mcp·MCP Registry 준비

- [x] `flint setup --mcp` — 하네스 자동 탐지·등록(exec.LookPath,
  결과 한 줄 JSON)·플랫폼별 `.mcpb` 번들 Release 자산화
  (scripts/make_mcpb.sh·goreleaser post hook + extra_files,
  checksums 불변)·mcp-registry/server.json(플레이스홀더)·README
  사용법·발행 3단계 안내·배지 v0.16.5(EN/KO)·plugin.json 동기

### v0.16.6 패치 (2026-11-06) — MCP Registry 자동 발행

- [x] server.json template($schema·version)·publish_mcp.sh·CI github-oidc
  스텝 — 태그 push 시 무인 발행

### v0.16.7 패치 (2026-11-06) — 완전 자동 릴리스 파이프라인

- [x] VERSION 파일(단일 버전 소스)·autotag 잡(main push→버전 변경 감지→
  자동 태그)·MCP Registry 발행 항상 실행(goreleaser 스킵과 분리)·
  github-oidc id-token: write. 사용자는 PR에 버전만 올리면 끝.

### v0.17.0 패치 (2026-11-06) — 의존성 관리·setup UX·CI 리뷰

- [x] R1 의존성 자동 관리 — dependabot(gomod·github-actions 주간)·
  CI mcp-publisher 다운로드 latest→v1.8.7 고정(재현성)·
  scripts/bump_version.sh(VERSION·배지 EN/KO·plugin.json 일괄 범프 —
  배지 누락 사고 3회 방지 자동화)
- [x] R2 setup UX 고도화 — 단계별 시각적 프로그레스(ANSI ✓/✗·딤 세부,
  비TTY 자동 plain)·`--json` 출력 불변
- [x] R3 CI 파이프라인 설계 리뷰 — docs/CI_REVIEW.md(구조 진단·개선점)
- [x] R4 버전·문서 — VERSION·배지 v0.17.0(EN/KO)·plugin.json 동기

### v0.17.1 패치 (2026-11-06) — CI 리뷰 후속·코드 위생·문서 정합

- [x] R1 CI_REVIEW §4 청산 — 태그 push `|| true` 제거(실패 시 중단)·
  concurrency cancel-in-progress(같은 레프 연속 실행 취소)·DB 대기
  단일화(doctor 통과 후 psql 1회)·PyYAML pip 캐시(setup-python+
  requirements.txt)
- [x] R2 코드 위생 — gofmt 18파일 정리 + `make ci` fmt 게이트 신설·
  as-of 접두어 플레이크 방지(해시 접두어가 상대 문법 \d+[hd]와 겹칠 때
  회피 창 선택 — 제품 계약 "① 상대 우선"은 불변)
- [x] R3 Quick Start 실패 수정 — `make install` GOPATH/bin 실설치화
  (bare `flint` 동작)·`make seed` FLINT_SEED_DSN 명시·Go 배지 1.26·
  exit 3 서술 확장(스키마 위반 포함)·Copilot 자동 등록 과대 표기 정정
- [x] R4 문서 스위트 정합 — QUICKSTART v0.17(setup --mcp·플러그인·
  시각 진행 서술)·RUNBOOK doctor 검사 10종 표+`--expire-pending`
  계약·adapters 4종 자동 등록 안내·TUNING suggest_min_score 폐지 기능
  서술 정정
- [x] R5 버전 — VERSION·배지 v0.17.1(EN/KO)·plugin.json 동기

### v0.18.0 (2026-11-06) — 회상 우선 메모리 (recall-first memory)

테마 선정 근거: 관측 정합 결과 누적 zero_pct(구 메트릭, 초기 이력 지배)와
기간화 zero_pct가 갈라 검색 품질 트리거는 미도달 — 실측 신호는 중복
재제출이 지배적이고 회상이 굶는 상태라는 것. 내부 전수(중복 경로·read
유도 장치)·외부 조사(mem0 NOOP·Anthropic already-exists·세션 시작
프로토콜·Doltgres 확장 현황)를 종합해 "기록만 하고 회상 안 함"을
write·주입·종료·관측 4개 접점에서 교정한다.

- [x] R1 duplicate 응답 피드백(#61) — 기존 행 topic·state 노출(재기록
  대신 read/supersede/approve 유도)·SKILL.md 가이드
- [x] R2 hot 헤드라인 주제 노출(#63) — normal 프로파일에 최근 결정 주제
  상위 3·회상 힌트(minimal 불변·--full 바이트 불변)
- [x] R3 done 회상 경고(#64) — 오늘 read 0회·기록 임계 이상 시 종료 경고
  (recall_warn_writes 기본 3, thresholds 레지스트리 등록)
- [x] R5 폴백 검색면 tags 포함(#65) — 태그로만 분류된 지식 회수(구문
  접합면 불변)
- [x] R4 status 기간화 지표(#62) — read_calls_14d·read_zero_pct_14d 병기
  (누적값 오독 방지, report와 동일 산식)
- [x] 버전 — VERSION·배지 v0.18.0(EN/KO)·plugin.json 동기

### v0.18.1 패치 (2026-11-06) — v0.18.0 코드리뷰 8건·완성도 2건

- [x] R-a 개인 관측 수치 서술 중화 — PLAN 엔트리·테스트 주석·아카이브
  spec 2건에서 실측 백분율 인용 제거(원칙: 근거는 일반 서술로)
- [x] R-b 회상 경고 전달 경로 폐쇄 — Stop 훅 stdout은 자동 배선에서 안
  보이므로 done이 recall 마커를 남기고 다음 SessionStart(nudge)가 전용
  문구 1회 표시 후 자동 소진(증류 마커와 Reason 분기·기존 경로 불변)
- [x] R-c recall_warn_writes -1=끔 의미론(파서·폴백·TUNING 문서화)
- [x] R-d daily 파서 단일화 — windowedDaily 공유 리더(report·status가
  소비, 3중 복제 해소)·read_zero_near_pct_14d 노출(데드 반환 폐지)
- [x] R-e DupIDByHash를 DupRowByHash 위임으로 통합(dedup SQL 단일 소스)
- [x] R-f bump_version.sh가 배지 alt도 교체(구버전 alt 잔존 갭)·README
  alt 정합·hotTopics 상수화·DupRowByHash 실DB 테스트·done/e2e 마커
  격리(FLINT_CONFIG_DIR — 테스트가 실사용자 config에 마커 남기던 오염)
- [x] 버전 — VERSION·배지 v0.18.1(EN/KO)·plugin.json 동기

### v0.18.2 패치 (2026-11-06) — 2차 리뷰 5건 — 마커 수명 주기 분리

- [x] R-a recall 마커 파일 분리(recall-hint.json) — v0.18.1의 단일
  마커 파일 설계 결함 2건 폐쇄: ① Stop 이벤트에서 recall 마커를 증류로
  오인해 엉터리 넛지 + --clear로 회상 알림 소멸 ② recall 기록이 내구성
  증류 마커(pending-distill)를 덮어써 증류 체인 유실. Stop은 증류만
  넛지, SessionStart는 둘 다 안내(회상만 자동 소진)·v0.18.1 잔존
  마이그레이션(증류 파일의 Reason=recall 식별)
- [x] R-b thresholds 파서 범용화 — parseIntInRangeOrDefault(min 하한)
  도입, recall_warn_writes 특수 분기 제거·TUNING에 0 폴백·1=매기록
  경고 문서화
- [x] R-c 관측 수치 잔존 중화 완료 — 아카이브 spec 3건 추가 항목
- [x] R-d doctor hook-file fix 문구 현대화 — settings.json 수동 등록
  안내를 플러그인 마켓플레이스 설치 우선으로
- [x] 버전 — VERSION·배지 v0.18.2(EN/KO)·plugin.json 동기

### v0.18.3 패치 (2026-11-06) — staticcheck 도입·사체 코드 청산

- [x] R-a staticcheck 2026.2.2 도입 — make lint 타깃(PATH→GOPATH/bin
  폴백)·CI test 잡 게이트 추가(버전 고정 — mcp-publisher 관례 준용)
- [x] R-b 사체 코드 제거 — portCheck(doctor v0.7 구판 래퍼, runDoctor
  경로가 실경로)·injectedKnowledgeIDs+hotSurvivorCount(접미 없는 구판
  생존 산정, hotLineSurvivorCount 체인이 실경로) — 각자의 테스트를
  실경로(runDoctor --check port)로 이관 후 제거
- [x] R-c 경미 결함 — nil Context(SA1012)·불필요 Sprintf 3건·미사용
  sentinel·CanonicalJSON 결정성 비교 명료화(2회 호출 변수 비교)
