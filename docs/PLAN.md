# flint 기획서

- **이름**: flint — version-controlled memory for AI agents
- **날짜**: 2026-10-20
- **상태**: v0.5 완료(2026-10-22) — 다음 마일스톤 미정
- **태그**: agent, doltgres, cli, mcp, knowledge-base

---

## 1. 배경과 목표

- 옵시디언의 이중 역할(인간 문서 + LLM wiki) 실패 → 분리 결정.
- **옵시디언 = 인간용 원본(PARA)의 유일한 소스 오브 트루스.**
- **flint = 에이전트가 자율 관리하는 파생 계층** (상태·인덱스·로그).
- 흐름 규칙(단방향): 옵시디언 → flint는 인덱싱·요약만(복사 금지).
  flint → 옵시디언은 "다듬을 인사이트" 증류만.

## 2. 확정 요구사항

1. 에이전트 전용 레포, 완전 자율 관리 (사람용 아님)
2. CLI·토큰 최적화 검색 — 필요한 최소 바이트만 컨텍스트 진입
3. DB 기반 저장 (마크다운 관리 기피)
4. git 이력 = 자율 쓰기의 롤백·감사 안전망 (필수)
5. 동시성 — 서브에이전트 병렬 쓰기 safe (필수)
6. MySQL 기피
7. 대시보드 버림 (저비용 부활 경로만 유지)

## 3. 후보 탐색 기록 (context7 문서 검증)

- **조합형**: basic-memory+Datasette(마크다운 기반 기각) / SilverBullet(올인원 최근접)
- **올인원**: Khoj(DB-first), Wiki.js(DB+git 백업) — 파일-first 아니어서 기각
- **저장 패턴 선례**: OpenClaw(SQLite 운영 + git엔 schema·JSONL 덤프),
  Hermes(MEMORY.md 2,200자·USER.md 1,375자 예산), Letta(DB-only)
- **git-native DB**: Dolt / DoltLite(SQLite 포크·프롤리 트리·FTS5,
  파일락 없음 → 동시성 기각) / Doltgres / cr-sqlite(merge만) / 기타
- **성숙 사례 심층**: Graphiti(bi-temporal 무효화), mem0 v3(additive 전환),
  Letta(블록 limit·read_only), Cognee(ECL·배경 실행)

## 4. 기술 선택 — Doltgres (확정)

**근거**
1. 동시성 — 서버가 커넥션 조율 + 커밋 시 낙관적 충돌 감지
   (병렬 쓰기가 "조용한 파손"이 아니라 "충돌 → 재시도")
2. Postgres 방언 (MySQL 기피 충족)
3. 1.0 달성(2026-08), dolthub 제작

**제약(수용)**
- 데몬 1개 상시 (Docker `dolthub/doltgresql:latest`, 호스트 포트 5434(ADR-0010),
  `DOLTGRES_USER/DOLTGRES_PASSWORD`)
- 확장 제한: pgvector·PostGIS 불가 — 임베딩은 태그+LIKE → 필요시 앱 사이드 코사인
- 락 클로저(FOR UPDATE) 미지원 → 재시도 필수
- ILIKE 미지원 → `lower() LIKE` 대체
- git식 CLI 없음 — 버전관리는 전부 SQL 함수(`SELECT dolt_commit(...)`)

## 5. 아키텍처 — 참조 3레이어

```
모든 에이전트 (Claude Code·서브에이전트·OpenClaw·Hermes·…)
   │ 접점 2개: ① flint CLI (셸 있는 에이전트)  ② flint mcp (MCP 클라이언트)
   ▼
flint 엔진 (단일 Go 바이너리 — DB 접속·검색·커밋·백업)
   ▼
Doltgres (localhost:5434, docker compose 상시 — restart 정책)
   ↑ 파생 규칙: 복사 금지, 인덱스·요약만
옵시디언 볼트 (인간용 원본)
```

세션 라이프사이클:
- SessionStart 훅 → `flint hot --session <id>` (핫 요약 주입)
- 작업 중 → `flint read` (검색, 풀 방식)
- 결정·종료 시 → `flint write` (기록+커밋)
- Stop 훅 → `flint done --session <id>` (미기록 임계값 경고)

## 6. 데이터 모델

- **projects**: id, name, status, updated — 상태 갱신은 즉시 active
- **knowledge**: id, topic, summary, tags, entity, domain, confidence,
  origin(agent|human), status(pending|active), content_hash(UNIQUE),
  session_id(→sessions), valid_at / invalid_at / ingested_at
- **sessions**: id, ts, agent, summary
- **hot_view**: 주입용(활성 프로젝트 + pending + 최근 결정 7일)
- **active_knowledge** 뷰: `WHERE invalid_at IS NULL` 강제
- **maintenance_runs**: 정리 작업 기록
- **meta**: schema_version (CLI 호환 검사)

채택 근거: 시간 유효성(Graphiti), 출처 연결·additive(mem0),
예산 주입(Hermes·Letta), 신뢰 경계(OpenClaw)

## 7. 쓰기·읽기 상세 계약

### 쓰기 — `flint write <kind>` (5단계 파이프라인)
1. **검증**: payload 스키마 위반 → 기대 스키마 출력 + exit 3 (자가수정)
2. **해시 dedup**: canonical JSON sha256 → 기존 시 `{"status":"duplicate"}` exit 0 (멱등)
3. **판정**: `kind==knowledge && confidence<0.8` → pending, 그 외 active
4. **커밋**: 배치 INSERT → `dolt_add` → `dolt_commit`(메시지 `유형: 내용 [session:ID]`)
   — 충돌 재시도 0s/1s/3s, 3회 실패 시 exit 1
5. **출력**: 단일 줄(JSON 1개)은 `{"status":"committed","hash":"...","state":"pending","id":N}`
   불변. JSONL 다중(2줄 이상, 단일 커밋)은 집계형
   `{"status":"committed","inserted":N,"duplicates":M,"hash":"..."}` —
   `hash`는 **dolt 커밋 해시**(content_hash 아님), inserted/duplicates는
   실제 삽입·skip 줄 수(부분 커밋). duplicate 줄의 살균·비밀 경고는
   `"warnings"` 배열로 병행 출력된다.

payload (stdin JSON / JSONL 다중 = 단일 커밋, 원자성 all-or-nothing):
```json
knowledge: { "topic", "summary", "tags": [], "entity"?, "domain"?, "confidence", "valid_at"? }
session:  { "agent", "summary", "tasks": []? }
project:  { "name", "status": "active|paused|done", "note"? }
```
- 정규화는 CLI 책임: tags → `:a:b:` 저장, ts UTC, domain 기본 "main"
- **write는 절대 UPDATE 금지**(projects 제외). 무효화는 `flint maintain --invalidate` 전용
- 실패 격리: flint 장애가 본 작업을 중단하지 않는다

### 읽기 — `flint read [query]`
- `active_knowledge` 뷰로 강제(무효 사실 차단)
- 무료 텍스트: `lower(topic||summary) LIKE lower('%q%')` (ILIKE 대체)
- 랭킹 정렬: topic 정합 > confidence > 최신 순(v0.5 — §14)
- 필터: `--tag --entity --domain --limit`
- 출력: 기본 `id|topic|summary(80자)` 3컬럼 — `--json --fields id,topic`(gh 패턴) 전체 선택
- **토큰 예산 박제**: limit 기본 10 / 상한 50
- `flint hot`: 출력 형식 고정 + 상한 = meta hot_budget(기본 2,000자, TUNING.md §1)
- reader role은 뷰만, 재시도 1회 후 exit 2

### exit code
`0` 성공·중복 / `1` 재시도 소진(사용자 보고) / `2` 서버 다운(조용히 진행) /
`3` 검증 실패(에이전트 자가수정)

## 8. 큐레이션·운영

- **검수 게이트**: 신규 사실+confidence<0.8만 pending(상태 갱신·로그는 즉시).
  승인 = 대화로 → 에이전트가 `flint approve --id N`. 3개월 후 자동 전환 평가.
- **maintain = 에이전트 큐레이션 세션** (확정): `--check`는 임계값 보고만
  (pending>50 or 중복의심>20), 트리거 시 짧은 정리 세션에서 판정 →
  병합·`invalid_at` 갱신 → 커밋. 순수 SQL 자동 판정은 오탐(GGraphiti·mem0 선례).
- 스키마 마이그레이션: dolt 브랜치에서 실험 → merge
- 감사: `dolt_log`·`dolt_diff_*`가 곧 감사 리포트
- 백업: `flint backup` — flint 자체 SQL 덤프 + 데이터디렉터리 스냅샷 이중화(ADR-0002),
  sha256 무결성 체크섬, 하루 첫 세션 지연 트리거.
  복구 `flint restore --file`(가드 탑재). 복구 훈련 정례화는 v0.5.
  dolt 원격 푸시는 미지원으로 제외. 세션 로그 유실은 수용 명시.

## 9. UX 원칙 — 사용자 접점 3개

1. 설치 1회 (`git clone && flint setup` — 엔진+플러그인+훅 원커맨드)
2. 승인 = 대화로 ("승인해") 3. 조회 = 대화로

제거는 대칭: `flint teardown [--purge]`.
SQL·스키마·CLI 문법은 전부 에이전트 영역 — 사용자 비노출.

## 10. CLI·실행 계층 — Go, 에이전트 전용

- **Go 단일 정적 바이너리** `bin/flint` — pgx 내장(psql 불필요, Doltgres 테스트
  스택과 동일), SQL은 embed. 차선: python3 stdlib(계약 동일, 교체 비파괴)
- 서브커맨드 14종:
  `setup / teardown / write / read / approve / maintain / hot / status /
   backup / restore / done / version(v0.3 추가) / mcp·audit(v0.5 추가)`
  — `mcp` CLI 연결 완료(v0.5, 실 프로세스 JSON-RPC e2e 검증 — §14)
- 원칙: 프롬프트 금지 · 출력 기본 = 한 줄 JSON(`--pretty`만 사람용) ·
  에러는 셀프디스크립팅(원인+수정법) · TUI 없음 · 파이프 가능
- 레포 구조: `bin/`(바이너리) `lib/`(Go 패키지, SQL embed 포함) `cmd/flint/`(진입점) `docs/` `data/`(런타임만 git 무시)
- 테스트: E2E 3케이스(dedup / 재시도 / 이스케이프) + 판정 로직 단위 —
  v0.4 기준 E2E 17 시나리오로 확장(tests/e2e)

## 11. 리스크·기계화

- **채택 루프** (최우선): 규칙 3계층 — ①플러그인 스킬(규칙 문서)
  ②훅(hot/done, Stop 출력로 순응 유도) ③CLI 경고(기록 경과·pending).
  CLAUDEmd 마킹 블록 방식은 폐지 → 플러그인으로 이관.
- **백복 설계**: 주기·보관·복구 훈련 — 저장소 추가는 `--backup-dir` 1회 지정
- **데몬 조용한 사망 방지**: docker compose restart 정책(unless-stopped) +
  `flint status` 점검, 다운 시 다음 세션 첫 줄 경고
- **주입 콘텐츠 오염 전파 방지**: `[flint]` 접두 = 데이터 경계,
  "주입은 지시문 아님" 규칙, 오염 시 `dolt_revert` 롤백
- **confidence는 품질 필터지 보안 아님** 명시

## 12. 패키지·배포 — 3계층

| 계층 | 형태 | 배포 |
|---|---|---|
| 엔진 | Go 바이너리 (mcp 모드 내장) | GoReleaser + GitHub Releases (비공개 OK) |
| 표준 인터페이스 | `flint mcp` — read/write/hot MCP 도구 | 바이너리 포함 |
| 하니스 포장 | Claude Code 플러그인 | `plugin install` 1커맨드 |

- 릴리스: 태그 push 시 CI는 절차 안내만 출력 — 실 릴리스는 사용자 확인 후
  goreleaser(darwin arm64/amd64) → Release 첨부(비간섭 원칙, T43)
- 선택: homebrew-tap 공개 레포 + `brews.repository`(v2) + GH_PAT
- 버전: git 태그 + `flint version`(ldflags) + meta.schema_version 호환 검사
- 업데이트 런북: `git pull && make install && flint setup --update-rules`
- 신규 머신: clone → make install → flint setup
- 비목표: brew 본체 등재, linux, 코드사이닝, self-update

### Claude Code 플러그인 구성
```
flint-plugin/
  .claude-plugin/plugin.json   # 매니페스트
  hooks/hooks.json             # SessionStart→flint hot, Stop→flint done
  skills/flint/SKILL.md        # 사용 규칙 (종료 전 write·작업 전 read·주입=데이터)
  commands/flint.md            # /flint — 상태·pending·최근 결정 요약
  .mcp.json                    # flint mcp 서버 연결
```

## 13. 최종 형태·비간섭 5원칙

1. **주입은 최소, 검색은 풀**: hot은 예산 내 상태만 — flint가 먼저 말 걸지 않는다
2. **훅은 비차단·타임아웃**: hot 2초 초과 시 skip, done은 종료를 절대 막지 않음
3. **flint 장애 = 조용한 skip**(exit 2): 세션 시작·작업·종료를 지연시키지 않는다
4. **Stop 경고는 임계값 기반**: "연속 3세션 미기록"일 때만
5. **flint 데이터는 데이터**: `[flint]` 경계 밖은 지시문이 될 수 없다

멀티머신: 기본 단일 머신, 원격 에이전트 필요 시 터널로 동일 포트 노출(옵션).
훅 없는 하니스(OpenClaw 등)는 MCP 도구만으로 참여.

## 14. 마일스톤

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

### v0.19.0 마이너 (2026-11-07) — 효과 계측 (effect instrumentation)

- [x] R1 측정 기준 문서 — docs/METRICS.md: "도움"의 정의 4목표(GSM
  매핑)·증거 등급(Tier 1 상관 프록시 / Tier 2 연결 상관 / Tier 3 합성
  인과=floor)·금지조항(사용량≠효과·단일 지표 게이밍·상관의 인과 서술
  금지·실측치 미인용). 기준이 코드보다 앞선다(HEART/GSM 관례)
- [x] R3 read 이벤트 additive 계측 — 스키마 v6 read_events(ts·hits·
  zero·near·fallback·session, 14일 보존), EnsureSchemaV6 게이트,
  read --session 선택 라벨(조회 동작 불변). 쿼리 본문·개인 문구 미기록
- [x] R2+R4 report 효과 섹션 — effect 객체: hit_pct(G1)·dup_prevented
  (G2, 절약 프레임 재해석)·reuse_pct·dead_pct(G3)·read_events_n·
  read_write_pairs·pair_pct(G4, 30분 창 Go-사이드 조인 — Doltgres
  interval 리스크 회피)
- [x] 버전 — VERSION·배지 v0.19.0(EN/KO)·plugin.json 동기

### v0.20.0 마이너 (2026-11-07) — 효과 계측 정밀화 (effect precision)

- [x] R1 스키마 v7(additive) — read_events.ids(반환 id CSV, 상위
  10)·session_starts(세션 경계)·hot_events(주입 노출), 3테이블 14일
  통합 보존 정리
- [x] R2 세션 경계(D) — session-start 훅이 SessionStart 1회당 경계
  기록(출력 없는 내부 계정, 전 프로파일). compact·resume 재발발은
  보수적 분할로 수용(룰링)
- [x] R3 페어링 정밀화(D) — 같은 세션 배정에서 read 직후 30분 내
  write만 페어(타 세션 우연 동반 제외). 경계 부재 환경은 시간창
  폴백. 신지표 recall_led_pct(회상 선도 세션 비율 — done 회상
  경고와 같은 관점의 관측 버전)
- [x] R4 사용 연쇄(B) — 반환 id 기록 + chain_pct(24h 내 재반환 또는
  같은 topic write). IR 암시적 피드백 관례 — 프라이버시 무해·비간섭
- [x] R5 노출 계층(E) — hot --full 주입 id 기록(hot_events·30일 보존)
  + dead_pct 정합 가드: 주입은 BumpReads로 read_count를 올리므로(리뷰
  재정의) 정상 경로에 후보 밖 — 가드는 참조 증분만 실패한 계측 유실
  불일치를 메운다
- [x] 룰링: 기저율 셔플(C) 이연 — 세션 경계가 우연 동반을 구조적으로
  제거, 잔여는 표본 축적 후 재평가(METRICS §4 명시)
- [x] 버전 — VERSION·배지 v0.20.0(EN/KO)·plugin.json 동기

### v0.21.0+ 마이너 후보 (2026-11-07 사용자 지시 — 방향 확정)

**A. 자동 업데이트** — 릴리스는 완전 자동(VERSION→태그→goreleaser→
MCP)이나 설치 단말 갱신이 수동. 후보: ① `flint update`(최신 Release
확인·자가 교체·실패 롤백) ② 세션 시작 시점 버전 확인 후 [flint]
경계 내 1줄 갱신 안내 ③ 플러그인 마켓 갱신 경로 안내. 제약: 비간섭
원칙(§13) — 자동 다운로드·교체는 명시 opt-in, 기본은 안내만.

**B. 지표 고도화** — v0.20.0 계측 위 후속: ① 기저율 셔플 재평가(표본
축적 후 세션 내 잔여 우연) ② 세션 라벨 자동 주입(훅→read 배선)으로
세션 일치 페어링 정밀화 ③ 계측 상한·보존 창(dead_days·pair·chain)의
thresholds 레지스트리 이양(meta 오버라이드) ④ 트렌드 보고(전기 대비
Δ — 단면→추세) ⑤ hit 품질 근사(반환 순위·재사용 가중).

**C. 내부 테이블·문서 관리 고도화** — ① 계측 테이블 운영체계: 보존
기간 meta 오버라이드·용량 점검(doctor 신규 체크)·이벤트 테이블
통합/분리 재평가(v7 3테이블 구조 지속 여부) ② meta 키 위생: 레거시
키 롤링 정리·키 네임스페이스 규약 문서화 ③ 문서 체계: PLAN 장기
이력 아카이브(docs/plans 분리), 버전별 docs 테스트 파일 통합(패턴
중복 축소), METRICS·TUNING·RUNBOOK 상호 링크 정비.

**D. 개인화 기능 고도화** — ① 프로파일 체계(minimal·normal) 확장:
사용 패턴 관측(effect 지표) 기반 프로파일 추천·전환 안내 ② 개인
어휘 관리: 도메인·태그 어휘집 등록·경고 고도화(CheckVocabulary
확장 — 어휘 밖 태그의 자동 후보 제안) ③ 임계값 오버라이드 인터페이스:
meta 수동 갱신을 `flint tuning` 류 커맨드로(TUNING §2 키 조회·설정·
reset, 문서와 단일 소스) ④ 스코프 개인화: 프로젝트·에이전트별
지식 스코프 필터 확장(domain 경로 승계). 원칙: 개인값은 운영 DB에만
(repo 기본 불침투 — CLAUDE.md 방어 계층 승계).

**E. 대시보드 도입 여부(평가 항목)** — effect 지표의 추세 관측이
개발 루프의 판정 근거라 시각화 수요는 실재. 도입 여부 판정 기준:
① 아키텍처 정합 — 단일 바이너리·한 줄 JSON 원칙과 양립하는 형태
(후보: `flint report --html` 정적 내보내기 > 상시 웹서버) ② 프라이버시
— 운영 DB를 떠나지 않는 로컬 렌더링만(외부 전송 금지, 합성 데모로
실증) ③ 유지 비용 — 프레임워크 의존 없는 단일 파일 HTML(임베드
템플릿) ④ 수요 실증 — report 텍스트 소비로 충분한지 관측 기간 후
판정. 기본 방향: 도입 확정이 아니라 평가 후 결정.

### v0.20.1 패치 (2026-11-07) — 패치 루프 1차: 전수 조사 18건 중 정확성 4건·테스트 왜곡 2건

관측·조사 → 레퍼런싱 → 브레인스토밍 → 수정 4단계의 1차 이터레이션
(전체 기능 스윕 7건 + 테스트 품질 감사 9건 + 운영 관측 2건):

- [x] F1 무효화 상태 정합(high) — Invalidate·Merge(from)이
  status='invalidated' 병기(배치 경로와 단일 법칙 — read --status
  invalidated 노출·report counts 정합) + Approve 승격 시 invalid_at
  청소(active+invalid_at 좀비 방어)
- [x] F2 setup 배선 — st 고정 nil로 정상 DB에서도 매번 degraded(QUICKSTART
  약속 불이행). 연결 시도 후 실패 시에만 nil 진행으로 수정 — 깨진 동작을
  '계약'으로 문서화하던 테스트(TestSetupJSONMode)도 재작성(테스트 감사
  축2 사례) + DB 다운 degraded 경로 별도 테스트로 보존
- [x] F3 hot 헤드라인 카운트 왜곡 — 카운트 경로 상한 50(HotHeadlineSQL),
  주입(--full)은 예산 상한 10 계약 불변
- [x] F4 자동 백업 로테이션 — maybeAutoBackup이 keep-N 로테이션 호출
  (수동 경로와 동일 계약, 이전엔 무한 축적)
- [x] T1·T8 테스트 왜곡 즉치 — effect fixture 미매칭 쿼리 t.Fatalf
  계약화(즉시 3종 잠복 흡수 발견)·hot_full_guard want 리터럴 고정
- [x] 버전 — VERSION·배지 v0.20.1(EN/KO)·plugin.json 동기

이월(v0.20.2+): docs 테스트 통합(26파일→1)·doctor --check 체크명 정합·
F5 --fields 오타 검증·F6 nudge 죽은 분기·F7 MCP 사체·T2 stale/dead
판별 계약·T3 게이트 v2~v5 내용 단언·T6 신규 테스트 네이밍 정책.

### v0.20.4 패치 (2026-11-07) — 패치 루프 4차: 마일스톤 B③·C①의 패치 규모 항목 조기 회수

사용자 지시로 패치 루프 연속 진행(버전마다 정지하지 않음) — v0.21.0+
후보 중 패치 규모 항목을 회수:

- [x] B③ 계측 임계값 thresholds 레지스트리 이양 — dead_days·
  pair_window_minutes·chain_window_hours·events_retention_days·
  hot_events_retention_days·events_volume_warn 6키(판정 창·보존·용량,
  TUNING §2 동기)
- [x] C① doctor event-volume 체크 — 계측 3테이블 합계 행 수 warn
  (성장은 정상 동작이므로 warn 안내만, fail 아님)·--check 지원
- [x] PruneReadEvents 보존일 meta 오버라이드·report 판정 창 오버라이드
- [x] 버전 — VERSION·배지 v0.20.4(EN/KO)·plugin.json 동기

### v0.20.5 패치 (2026-11-07) — 패치 루프 5차: B② 세션 라벨 자동 주입

- [x] session-start 훅이 현재 세션 라벨을 config 경로 파일
  (session-current)에 기록, read가 --session 미지정 시 조용히 자동
  채택 — G4 라벨링이 에이전트 협조 없이 자동화. session-end가 정리
  (스테일 방지). 병렬 세션 last-writer-wins(룰링 — 보조 계정)
- [x] 버전 — VERSION·배지 v0.20.5(EN/KO)·plugin.json 동기

다음 패치 후보(연속 루프): C② meta 키 위생(레거시 키 실태 조사·
롤링 정리 방안) 또는 보안 각도 스윑.

### v0.20.3 패치 (2026-11-07) — 패치 루프 3차: 문서·매니페스트 정합 7건

관측(문서·매니페스트·배포 자산 스윕 + 운영 G4 파이프라인 실증) → 수정:

- [x] D1 TUNING §8·§10 낡은 '코드 상수 직접 수정' 서술 정정(high) —
  §2 레지스트리화 이후 실상과 정면 모순(튜닝 원칙 위반을 문서가 유도)
- [x] D2 README 하이라이트 줄 v0.17.0→현재(EN/KO) + 재방치 방지
  게이트(TestReadmeHighlightMatchesVersion — 배지 동기 밖 영역)
- [x] D3~D7 사소 정합 — setup 행 '시드 안내' 정확화·PLAN 파일 수
  34→26·QUICKSTART 의존(x/text)·RUNBOOK 스키마 v7·검사 조건부
  보충·KO 서브텍스트 8종·QUICKSTART 주석 위치
- [x] 운영 실증 — session_starts 기록 파이프라인 정상(0건은 설치 후
  SessionStart 미발생, 결함 아님)
- [x] 버전 — VERSION·배지 v0.20.3(EN/KO)·plugin.json 동기

### v0.20.2 패치 (2026-11-07) — 패치 루프 2차: 이월 8건 완료

- [x] T5 docs 테스트 통합 — 버전별 26파일(v0접두) → docs/versions_test.go 단일
  table-driven(표 50행 + 커스텀 로직 18함수 원본 보존, 커버리지 불변)
- [x] O2 doctor --check 정합 — 전체 리포트 체크명(skill-file·harness-*)
  단독 실행 허용(이름 공간 불일치 해소)
- [x] F5 --fields 검증 — 알려진 컬럼만 허용, 오타 위치 fallback 왜곡
  exit 3으로(ReadFieldKnown 사전 검증)
- [x] F6 nudge 죽은 조건문 제거·F7 MCP 사체 제거(NewSimpleStore 체인 —
  테스트 순환까지 정리)
- [x] T2 판별 계약 — 제품 SQL 자기 식별 마커(/* stale */·/* dead */),
  substring 추측 판별 폐지
- [x] T3 게이트 산출물 표 단언 — v2~v7 컬럼·인덱스·테이블 존재 검증
  (게이트 no-op 회귀 방어)
- [x] T6 테스트 작성 규약 — docs/TESTING.md(네이밍·fixture 라우팅·
  단언 원칙)
- [x] 버전 — VERSION·배지 v0.20.2(EN/KO)·plugin.json 동기

## 15. 이연 결정 (트리거 정의됨)

- [x] 주입 예산 상한: hot_view 2주 실측 p95로 확정 → 회수(v0.4): hot_budget meta + p95 계측(TUNING.md §1)
- [ ] 보존·영구삭제: 데이터 축적 후 판단 (진짜 삭제는 filter-branch) — v0.5 백업 보존 정책과 연계
- [x] 백업 포맷 확정: pg_dump 검증 결과에 따름 → 회수(W0): pg_dump 실패, 자체 SQL 덤프 확정(ADR-0002)
- [x] 임계값 수치(pending>50·중복>20·conf<0.8·연속 3세션): 운영 중 조정 → 회수: meta 오버라이드 체계(TUNING.md §2)
- [ ] 검색 품질 고도화(자모 bigram·RRF 융합 랭킹): 제로히트율 20% 초과 +
  read_calls≥50 트리거 도달 시 별도 마일스톤 — v0.18.0 정합: 기간화
  zero_pct 기준 미도달(누적 구 메트릭은 초기 이력 지배로 폐기 판단).
  fallback_pct가 지속 20%+ 시 폴백 tags 확장(R5) 효과 먼저 실측.
  코사인은 Doltgres pgvector 에뮬레이션(2026-09 발표, 부분 지원) 실증
  전까지 보류

## 16. 기술 검증 (v0.1 첫 작업)

- [x] pg_dump 백업(export 방향) 호환 → FAIL(Doltgres 미지원): 자체 SQL 덤프로 대체 확정(ADR-0002)
- [x] dbhub↔Doltgres 실접속 → 대체 회수: flint mcp 서버가 실접속 증명(실DB e2e 통과), dbhub 외부 도구 검증 폐기
- [x] Doltgres AS OF 시간여행 쿼리 → 동작 확인(ADR-0002 전제로 채택)
- [x] flint 이름 전역 충돌 스윕 → 예비 스윕 완료, 비공개 한정 유지(ADR-0003, 공개 전 재스윕 의무 유지)

## 17. 정정 기록·비고

- datasette 대시보드 부활 불가(SQLite 전용) → PG 계열로 변경
- Doltgres 성숙도: 초기 추정과 달리 1.0 달성
- "Doltgres=Postgres 생태" 오류 → 확장 제한 공식 명시
- 전 검증은 웹 검색 다운(429)으로 context7 문서 기반 — 재충전 후 보강 권장

## 18. 명명

- **flint** — 부싯돌. 사람의 볼트가 Obsidian(흑요석)이면 에이전트의 기억은
  flint(부싯돌). 도구를 만드는 돌, 불씨를 내는 돌(`flint hot` = 불붙이기).
- 태그라인: **"version-controlled memory for AI agents"**
- 네이밍 관행 부합: 자연물·광물 계열(DB 계열 관례), 이름=상징·태그라인=기능 분리

### v0.1 구현 확정 정정 (2026-10-21)
- SQL embed 경로 `sql/`→`lib/sqlfs/` (go:embed 상위 접근 제약). 커맨드 진입 `cmd/flint/main.go`
- read `--kind` 필터 제거 — active_knowledge 뷰는 knowledge 단일 테이블
- 스키마 미정의 payload 키(`session.tasks`·`project.note`)는 저장 전 제거 + `warnings`로 고지
- Doltgres 제약 해결: 다중문장 Exec 미지원→문장별 실행, CREATE OR REPLACE 뷰 컬럼 미갱신→simple 프로토콜 DROP+CREATE
- 루프 교훈: wave0(스켈레톤)은 병렬 태스크보다 반드시 선행 (후행 시 스텝 덮어쓰기 사고 — v0.1 인시던트)
