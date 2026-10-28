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
- 데몬 1개 상시 (Docker `dolthub/doltgresql:latest`, 호스트 포트 5433(ADR-0001),
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
Doltgres (localhost:5433, docker compose 상시 — restart 정책)
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
- 시딩: 활성 프로젝트 3건(메이플 머지게임·미니 던전·툴박스) + flint 기획 결정
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

## 15. 이연 결정 (트리거 정의됨)

- [x] 주입 예산 상한: hot_view 2주 실측 p95로 확정 → 회수(v0.4): hot_budget meta + p95 계측(TUNING.md §1)
- [ ] 보존·영구삭제: 데이터 축적 후 판단 (진짜 삭제는 filter-branch) — v0.5 백업 보존 정책과 연계
- [x] 백업 포맷 확정: pg_dump 검증 결과에 따름 → 회수(W0): pg_dump 실패, 자체 SQL 덤프 확정(ADR-0002)
- [x] 임계값 수치(pending>50·중복>20·conf<0.8·연속 3세션): 운영 중 조정 → 회수: meta 오버라이드 체계(TUNING.md §2)
- [ ] 검색 품질 고도화(n-gram·코사인): 제로히트율 20% 초과 + read_calls≥50 트리거 도달 시 별도 마일스톤 — 계측은 v0.5 T56(read_stats) 개시

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
