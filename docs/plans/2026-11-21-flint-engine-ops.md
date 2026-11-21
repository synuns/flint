# flint 엔진 운영 — dolt GC·히스토리 관측·컨테이너 footprint (2026-11-21)

상태: active
원천: docs/PLAN.md §6 이월②(품질·성능 인접) — 2026-11-21 갭 점검에서 누락
확인(코드에 `dolt_gc|ANALYZE|log_depth` 0건 — 쓰기마다 dolt 커밋이 쌓이는
구조에 억제·관측 장치 부재).
준수: CLAUDE.md(비간섭 5원칙) · LOOP.md(TDD·SOLID·게이트) ·
docs/RUNBOOK.md(운영 절 신설 분리)

> 2026-11-21 기획 리뷰 1차 심사(CHANGES_REQUIRED) 반영 완료 — GC 차단
> 장치·db_size 측정 수단·meta 키 명시 보강. 릴리스 편성 미정 — 투두 유지.
> 실DB 크기·로그 깊이 수치는 문서에 인용하지 않는다(경고 임계값은 코드
> 기본값 + meta 오버라이드 원칙).

## 0. 문제

- **히스토리 무한 증가**: write 파이프라인이 매 쓰기마다 dolt 커밋을
  낸다(감사 순수성 설계). 그러나 디스크 회수(GC)·깊이 관측·통계 관리
  장치가 전무하다 — 시간이 지날수록 디스크·백업 크기·감사 쿼리 비용이
  단조 증가하며 아무 경고도 없다.
- **엔진 버전업 절차 부재**: Doltgres 이미지 갱신이 어떤 순서로 안전한지
  RUNBOOK에 없다(백업→복원→업그레이드 연계 없이 이미지만 바꾸는 위험).
- **컨테이너 자원 비계측**: 토큰 비용은 원 단위로 계량하면서 상시 구동
  엔진의 메모리·CPU footprint는 어디에도 보이지 않는다.

## 1. 레퍼런스 조사 (2026-11-21, 웹)

**dolt GC**
- Doltgres 공식 — 실행 중 서버에서 `SELECT dolt_gc()` 사용 가능,
  `--shallow` 옵션(빠르고 덜 철저).
  https://www.doltgres.com/docs/reference/server/garbage-collection
- 온라인 GC 발표(2023) — 저장 프로시저로서의 비차단 GC 설계.
  https://www.dolthub.com/blog/2023-01-25-online-gc
- GC 호출 후 접속이 끊기는 것은 예상 동작(연결 재수립 필요) — 도구
  입장에서 반드시 고려할 계약. https://github.com/dolthub/dolt/issues/6633
- 최신 버전은 자동 GC 보유 — 수동 GC는 대부분 불필요하다는 로드맵 언급.
  https://www.dolthub.com/blog/2024-10-30-roadmap-update

**로그 깊이 통제**
- 공식 문서상 깊이 제한은 clone `--depth`(얕은 복제)뿐 — 실행 중 저장소의
  히스토리 리텐션 정책 기능은 없음.
  https://www.doltgres.com/docs/reference/version-control/dolt-sql-functions

**통계(ANALYZE)**
- Doltgres의 ANALYZE·플래너 통계 지원은 문서상 미확정(포스트그레스 호환
  문법 수용은 추정만 가능) — 착수 시 실증 필요. 일반 Postgres 참고:
  https://www.postgresql.org/docs/current/sql-analyze.html

**컨테이너 계량**
- `docker stats --no-stream`은 회수 가능 캐시를 제외한 working-set을
  보고 — 유휴 footprint 계량의 올바른 도구.
  https://docs.docker.com/reference/cli/container/stats/ 와 실측 선례
  (postgres:16-alpine 유휴 측정 — shared_buffers 설정값이 아니라
  docker stats로 잰다는 논점).

## 2. 설계 — 채택: 사람 운영 명령 경유 + 경고 임계 + 엔진 게이트 명시

**채택(우아함 준거 — 신규 상주 부품 0·자동화 최소·엔진 계약 존중)**

1. **`flint maintain --gc [--shallow] --yes`** — `SELECT dolt_gc()` 래핑.
   - GC는 접속을 끊는다(§1) → **세션 중 자동 실행 영구 금지**. maintain는
     사람 운영자 명령이므로 사전 경고 문구 + 실행 후 재연결·스키마 게이트
     재검증을 같은 흐름에서 수행한다.
   - **자동 경로 차단 장치**(리뷰 보강): ①`--yes` 필수(teardown --yes
     선례 — 확인 없이 실행 불가) ②에이전트 계약 문서 배제(sync 커맨드
     SKILL 미기재 선례 — RUNBOOK에만 기재, 사람 운영자 안내 전용)
     ③실행 전 안내 문구에 접속 단절 명시. CLI는 커맨드 자체를 막을 수
     없으므로 확인 절차·문서 배제로 실효 차단한다.
   - 엔진 자동 GC와의 관계: 최신 엔진은 자동 GC를 갖춘다는 조사 근거로,
     수동 --gc는 진단 결과 "필요하다고 판명된 경우"의 탈출구로 위치.
2. **doctor 경고 2종**(등록표·thresholds 동반):
   - dolt_log 깊이 초과 — 안내 문장 "maintain --gc 고려".
   - 데이터베이스 크기 임계 초과 — 측정은 `pg_database_size()` 기준
     (착수 시 Doltgres 동작 실증 — ANALYZE spike와 동일 대칭, 미지원 시
     진단 조용히 스킵. 데이터 디렉터리 크기 폴백은 미결정 §5).
   - 값은 코드 기본값 + meta 오버라이드(TUNING §2).
3. **로그 깊이 절단은 비목표로 명시** — 엔진이 리텐션 제어를 제공하지
   않는다(§1). read LIKE 개량과 동일 패턴: "엔진 지원 시 재판정" 행을
   PLAN §6에 남긴다.
4. **컨테이너 footprint 표시** — doctor/status에 `docker stats --no-stream`
   1회 조회 표시(계측 실패는 조용히 — PLAN §4 원칙 3). 신규 저장 없음(표시
   전용).
   - **자동 정지 금지**: 콜드스타트는 세션 시작 지연 = 비간섭 위반.
     사용 정지는 RUNBOOK 정책으로만(docker compose stop/start 가이드).
5. **엔진 버전업 절차(RUNBOOK 신설)** — 백업 → `restore --drill` →
   이미지 갱신 → doctor·스키마 게이트 순서로 고정. 업그레이드 판단은
   사람 운영자(릴리스 노트 확인 포함).
6. **GC 실행 이력 기록** — meta 키 2종(`last_gc_at`·`last_gc_mode`) —
   감사 가능성 최소 보존. 별도 계측 테이블 불필요(빈도가 희박한 운영
   이벤트).

**기각 대안(근거 명시)**
- 자동 GC 스케줄 — 접속 단절을 세션 중에 유발할 수 있음(비간섭 위반),
  엔진 자동 GC와 이중화.
- 로그 절단 도입 시도 — 엔진 미지원으로 불가능하며, 억지 경로는
  저장소 훼손 위험.
- 컨테이너 자동 정지/기동 — 콜드스타트 지연이 원칙 위반.
- ANALYZE 예약 실행 — 지원 여부 미확정(§1)을 기능으로 확약할 수 없음.

## 3. 계약

- **maintain usage 확장**: `--gc [--shallow] --yes` — usage 라인·커맨드 표·
  파서 통합 규칙(N9 불변) 동반.
- **doctor 체크 등록표 2종**: `dolt_log_depth`·`db_size` — 이름·임계·
  안내 문장은 등록표 단일 소스.
- **thresholds**: `dolt_log_depth_warn`·`db_size_warn_mb`(기본값은 코드
  등록)·없음 추가 금지(개인값 불침투).
- **RUNBOOK 절 2종**: "가비지 컬렉션"(접속 단절 계약·재연결 확인)·
  "엔진 이미지 업그레이드"(순서 고정).
- **PERF**: GC 전후 디스크 회수량 측정 관례 추가(합성 리그레션 벤치).

## 4. 마일스톤(투두 — 릴리스 편성 미정, 착수 시 결정)

1. **M1 GC·경고** — maintain --gc·doctor 2종·RUNBOOK 2절·meta 이력 키.
   검증: fake Store 인터페이스 확장(동질 가드)·재연결 e2e.
2. **M2 footprint 표시** — doctor/status 표시·실패 조용히 계약 테스트.
3. **M3 ANALYZE spike(L0)** — Doltgres 지원 실증. 지원 시 maintain
   --analyze 채택, 미지원 시 폐기 선언을 본 문서에 남긴다.

## 5. 미결정 (착수 시 결정)

- `--gc`의 기본 모드(full vs shallow 우선순위).
- 경고 임계 기본치(log 깊이·db 크기)의 합리적 초깃값 근거.
- `pg_database_size()` 미지원 시 데이터 디렉터리 크기 폴백 채택 여부.
- doctor 경고 2종의 위험도 등급(warn vs info).

## 6. 비목표

- 자동 GC·예약 GC(엔진 자동 GC에 위임).
- 실행 중 저장소의 히스토리 깊이 절단(엔진 게이트 — 지원 시 재판정).
- 컨테이너 자동 정지·자원 상한 강제(compose 파일 변경 없음).
- 다중 엔진·원격 엔진 지원.
