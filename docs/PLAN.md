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

과거 엔트리(v0.1~v0.18.x, 2026-10-21~11-06)는 docs/HISTORY.md로
아카이브됐다(v0.21.2 — 마일스톤 C③). 아래는 현행 시대(효과 계측
v0.19.0+) 엔트리.

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

**G. 고도화 로드맵 (2026-11-08 서브에이전트 다각 조사 — 8각도·외부 레퍼런스 대조)**

| 테마 | 항목 | 레퍼런스 | 우선순위 | 트리거 |
|---|---|---|---|---|
| 검색 품질 1막 | RRF 융합 랭킹(정합수+read_count 통합)·자모 bigram·pgvector 에뮬 실증 probe(ADR, 채택 분리) | sqlite.org/fts5.html·dolthub.com/blog/2026-09-03-doltgres-extensions-update | **high** | RRF 무조건·pgvector 채택은 zero_pct 20%+read 50 트리거 |
| F 프라이버시 실행 | doctor secrets 사후 스캔(gitleaks 관례·레포 밖 커스텀 룰)·백업 age 암호화(순수 Go)·하드 삭제·덤프 마스킹 | github.com/gitleaks/gitleaks·github.com/FiloSottile/age | **high** | 이미 지시됨 |
| 온보딩 임포트 | CLAUDE.md/AGENTS.md → flint 지식 변환(전환 마찰 제거)·dedup 자동 승격 | Claude Code memory·Cursor Rules 관례 | med | 외부 사용자 전환 수요 |
| 증류 자동화(규칙 기반) | pending 임계 → maintain 자동 지시 배선·compact 요약 압축(Letta lossy 관례) | MemGPT core 상한·Mem0(LLM 판정은 비간섭 원칙상 배제) | med | marker 적립→소비 주기 관측 |
| E 대시보드 — 정적 export | flint report --html 단일 파일(임베드 템플릿·합성 데모) | Grafana 대비 정적 export(E 기준 ①~③) | med | 텍스트 소비 관측 후 |
| 성능 관측 저비용 | doctor active 행 수·DB 용량 체크 | — | low | active 1만 행 도달 시 인덱싱 재평가 |
| 다중 에이전트 | domain 스코프(D④) 승계·전용 기능 이연 사유 명시 | mem0.ai multi-agent 문서 | low | 팀 사용 수요 증거 시 |

판정: 플러그인·확장면 불필요(기존 plugin+tuning+어댑터로 커버). 다음
마이너 추천: **v0.25.0 검색 품질 1막**(RRF+probe) — 계측 체계가 이미
있어 개선 효과 검증이 즉시 가능(GSM 원칙상 지표 선행). pgvector 채택은
임베딩 생성 경로(외부 API 프라이버시/로컬 모델 비대) 미해결 — probe로
가능성 확인만, 채택은 트리거 후 별도 ADR.

**F. 프라이버시 기능 (2026-11-08 지시)** — ① 저장 지식 사후 비밀 스캔:
doctor 신규 체크(secrets) — 저장 시점 스캔(ScanSecrets)은 회피·이후
유입을 못 잡으므로 보유 지식 전수 재스캔·발견 시 warn(수정 안내) ②
백업 암호화(옵트인): ~/.flint/backup 평문 SQL에 대한 passphrase 암호화
— 사이드카 체크섬과 한 세트, restore 복호 경로 ③ 선택 삭제·스크럽:
무효화(soft)가 아닌 하드 삭제 — §15 '보존·영구삭제'(filter-branch) 이연
항목과 연계, Doltgres 재작성 실험 전제 ④ 덤프/내보내기 마스킹 옵션.
원칙: 기존 방어 계층(저장 살균·비밀 스캔·0600 권한·개인값 repo
불침투·계측 최소화 — METRICS §4 '쿼리 본문 미기록') 승계·강화.

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

### v0.25.3 패치 (2026-11-08) — v0.25.x 패치 루프 3차: 2차 리뷰·미커버 스윕 13건 중 7건

다각도 재검(사용자 챌린지) — v0.25.x 2차 심층 리뷰 6건 + 미커버 표면
스윕 7건 병렬:

- [x] U1(high) MCP store 누수 — tools/call마다 cli.Run이 pgxpool 2개를
  열고 닫지 않아 장기 세션에서 풀·고루틴·커넥션 영구 누적(max_connections
  고갈 시 전 명령 마비). Run 완료 후 defer st.Close()
- [x] R2(medium) NFD 토큰 자모 신호 누락 — containsHangul을 nfkc 후
  검사(NFD 분해형 질의의 자모 신호 회수)
- [x] U3(medium) tuning 동등 float 표기 거부 — '0.30'·'60.0'·'.5'가
  문자열 비교로 exit 3이던 것을 수치 비교로 수용
- [x] R4(low) rrfFuse 직접 단위 테스트 — 동률·0값 미기여·k 오버라이드
  격차(비대칭 픽스처) 단얜
- [x] R5(low) ADR-0011 재현 절차 기재(환경·SQL·합성 데이터)
- [x] R3·R6(info) 주석 정정 — topic 신호 이중 가중 의도 명시·정규화 멱급
- [x] 버전 — VERSION·배지 v0.25.3(EN/KO)·plugin.json 동기

### v0.26.2 패치 (2026-11-09) — v0.26.x 패치 루프 2차: 프라이버시 전체 재검수 10건 중 7건

독립 재검수 에이전트(6각도: 우회·오남용·정합·재노출·문서·테스트):

- [x] 발견1(high) sessions 사후 스캔 부재 — 주석은 '세션 summary 포함'
  의도였으나 코드는 knowledge만. sessions(id·agent·summary) 스캔 추가
- [x] 발견2(medium) done 세션 기록 agent 무살균 — InsertBatch 직접
  호출이 sanitizePayload 우회([flint] 마커·비밀 무검사 저장 + 발견1로
  사후 스캔도 없던 이중 사각). SanitizeText+ScanSecrets 적용
- [x] 발견3(medium) allowlist 가시성 — doctor 출력에 'allowlist N항목'
  표시(오타·무단 등록 감지) + RUNBOOK 확인 절차
- [x] 발견4 session_id 무살균 컬럼 — sanitizeFields에 추가
- [x] 발견5 RUNBOOK doctor 검사 목록 갱신(13종)·발견9 재노출 주의
  (read 평문 반환·덤프 평문) 서술 추가
- [x] 발견6 신규 3종 패턴 회귀 테스트·발견7 allowlist 파일 로딩
  테스트(env·주석·빈 줄)
- [x] 발견8 상한 도달 문구 정확화('스캔 미실행')
- [x] 발견10(스캔 비대칭) 룰링: doctor는 조인·write는 필드별 — doctor
  조인이 상위집합 감지라 유지, 주석 명시는 차후
- [x] 버전 — VERSION·배지 v0.26.2(EN/KO)·plugin.json 동기

### v0.26.1 패치 (2026-11-09) — v0.26.x 패치 루프 1차: 프라이버시 사이드 이펙트 대비

리스크 브레인스토밍(오탐·가용성·권한 호환·메타데이터·성능 5종 식별) 대응:

- [x] #1(최대 리스크) 오탐 완화 — secrets allowlist
  (~/.config/flint/secrets-allowlist.txt, repo 밖·personal-markers 관례
  승계): 등록 문자열을 마스킹 후 스캔 — 쓰기 경고·doctor secrets 모두
  적용. 문서용 예시 토큰·JWT 문서 오탐 해소
- [x] #5 성능·자원 방어 — doctor secrets 행 상한
  (secrets_scan_row_limit, 기본 10000·thresholds 튜닝): 도달 시 부분
  스캔임을 명시 warn('미발견' 거짓 보고 방지) + write 경고 보완 안내
- [x] #3 백업 권한 경화 호환성 — RUNBOOK에 절차 기록(타 사용자 잡
  접근 거부 대응)
- [x] 버전 — VERSION·배지 v0.26.1(EN/KO)·plugin.json 동기

잔여(룰링·문서화로 대응): #2 무효화 가용성 트레이드오프(설계상 감수)·
#4 --reason 메타데이터(의도된 감사 흔적).

### v0.27.2 패치 (2026-11-09) — v0.27.x 패치 루프 2차: 세션 지식화 실질화

다각도 재감사(5에이전트 병렬 + 실증) 24건 발견 중 세션 지식화 주관 8건:

- [x] [정책] v0.27.0 엔트리의 운영 실측치 인용(백분율 2종) 일반 서술화 —
  저장소 최상위 원칙(실측 수치 근거 인용 금지) 위반 수선
- [x] [활성화] PostCompact 표준 경로 연결 — Claude Code 훅 규격상
  compact_summary를 전달하는 훅은 PostCompact뿐(Stop stdin에는 해당
  필드가 없음 — 공식 문서·운영 관측으로 확인). flint compact가 세션
  행 적립과 별개로 maybeSessionKnowledge를 호출한다. Stop stdin 경로는
  타 하네스 폴백으로 유지
- [x] [활성화] stale 종료 경로에서도 지식화 — 조기 return 분기에 갇혀
  미기록 연속 시 요약이 폐기되던 사각 제거
- [x] [계약] tags를 []string{"process"}로 전달 — ":process:" 문자열은
  normalizeTags default 분기에서 빈 문자열로 소거되어 모든 적립 행의
  tags가 빈 값으로 저장되던 결함(done·import 양 경로)
- [x] [계약] 요약 500룬 절단(import 대칭)·살균 후 길이 게이트·topic
  살균(write 경로 대칭)
- [x] [보안] done·import 적립 시점 ScanSecrets 경고 — write 경로 계약
  정렬(경고만, doctor 사후 스캔과 이중 방어)
- [x] [가시성] 적립 실패·마커 실패 stderr 1줄(완전 침묵 관례 비대칭 수선)
- [x] 테스트 — 빈도 상한(당일 skip·경과 적립·파손 fail-open)·-1 끔
  센티널·dedup skip·살균 후 게이트·절단·stale 경로 적립·PostCompact
  적립·stdin 4분기·tags 단언. allowlist 테스트 전역 상태 cleanup 추가
- [x] 리뷰 8건 반영 — 마커 순서(compact 마커 먼저, 지식화 마커 생존)·
  세션 식별 가능할 때만 마커·비밀 경고는 dedup 통과분만(재경고 소음
  방지)·빈 게이트를 임계값 로드 앞으로(불필요 왕복 제거)·origin hook
  명시(기계 적립 식별)·runDone 꼬리 단일화(분기 이중 유지 드리프트
  방지)·warnSecrets 헬퍼 3중 복제 수렴·폴백 주석 정정
- [x] 버전 — VERSION·배지 v0.27.2(EN/KO)·plugin.json 동기

이월(v0.27.3 후보): import 파서 견고화(주석-펜스 상태기 재작성·~~~
펜스)·import 계약(배치 내 dedup·혼재 읽기 불가 보고·origin 정규화·
ExistsHash 오류 계약)·doctor secrets sessions/행 상한 테스트·SKILL 낡은
표현. 수용 룰링: 빈도 상한 경쟁(계측 목적·피해 제한적)·500룬 절단 후
본문 수정 재적립 미반영(문서 언급). 지식 행 session_id 연결은 스키마
무변경 원칙으로 마이너 후보.

### v0.27.1 패치 (2026-11-09) — v0.27.x 패치 루프 1차: 세션 지식화 활성화

- [x] 운영 관측 발견 — v0.27.0 리뷰 1로 '대기 상태'였던 세션 지식화의
  소스 경로 활성: runDone이 Stop 훅 stdin의 compact_summary
  (PostCompact 훅이 이미 생성)를 res.Summary로 연결 — 표준 배선에서
  실제 요약이 지식화에 흐른다. 파싱 실패·부재는 기존 스킵 계약 유지
- [x] 버전 — VERSION·배지 v0.27.1(EN/KO)·plugin.json 동기

### v0.27.0 마이너 (2026-11-09) — 세션 지식화: 기억이 자라는 루프

브레인스토밍(신규 발굴+운영 데이터+경쟁 동향) → "유입은 많고 순환이
막힘"(pending 방치·재발견 다수·회상 포화 신호) 판정:

- [x] R1 세션 지식화 — done이 세션 종료 요약에서 지식 후보를 pending
  으로 적립(규칙 기반: 최소 길이·빈도 상한 session_knowledge_days·
  content_hash 중복 방지). LLM 판정 배제(§13)·approve 게이트 승계
- [x] R2 증류 회로 폐쇄 — R1이 distill 마커를 남겨 nudge 예산 문구
  (v0.26.0 R5)와 자연 연결: 세션→지식→nudge→maintain→approve 순환
- [x] R3 `flint import [path] [--commit]` — CLAUDE.md·AGENTS.md·
  .cursorrules·GEMINI.md·copilot-instructions를 헤딩 단위 pending
  지식으로 변환(dry-run 기본·dedup 멱등·confidence 0.5 승격 경로)
- [x] R4 confidence 감쇠 산출 — doctor stale 체크에 시간 경과 정보
  확장은 이버전 제외(스코프 절제 — R1 순환 데이터가 먼저)
- [x] R5 SKILL.md 최신화 — import·tuning·update·secrets 명령 반영
- [x] 버전 — VERSION·배지 v0.27.0(EN/KO)·plugin.json 동기

### v0.26.0 마이너 (2026-11-08) — 프라이버시 실행 (F) + 증류 예산 가시성

다음 마이너 심층 레퍼런스 조사(에이전트: gitleaks 룰셋·age API·
Doltgres 이력·Letta/Mem0·Claude Code memory 포맷) → 권고 F 채택:

- [x] R1 doctor secrets 사후 스캔 — 비밀 패턴 카탈로그 확장(anthropic
  ·gitlab-pat·jwt·google — gitleaks 상위 룰 이식) + doctor secrets
  체크: knowledge 전수 재스캔, 발견 시 warn(패턴명+id만 출력 — 본문
  인용 금지). 저장 시점 스캔의 사각(회피·이후 유입) 폐쇄
- [x] R4(조사 확정) 하드 삭제 ADR-0012 — Doltgres filter-branch
  부재 확인(전수 문서 조사)으로 경로=백업 재구축 확정·구현 별도 이연
- [x] R5(부탐) 증류 예산 가시성 — nudge SessionStart 문구에 pending
  N/상한 M 동봉(PendingMax 재사용·임계 초과 시에만 — Letta 인밴드
  예산 노출 관례, 규칙 기반 정합)
- [x] R2·R3(백업 age 암호화·마스킹) — 이버전 제외: age 의존 추가는
  별도 실증 후(스코프 절제 — R1·R5로 핵심 가치 선공급)
- [x] 리뷰 7건 — ① 무효화 행 제외 재스캔(Fix 해소 경로 정합) ② 스캔
  전열 확장(쓰기 시점과 동일 — tags·entity·domain·origin) ③ sk-ant
  이중 보고 방지(특수 패턴 마스킹 — gitleaks 우선순위 관례, RE2
  lookahead 불가 대응) ④ nudge 초과 계약 정렬(>) ⑥ 전수 구체화
  룰링(Store[][]any 추상화 내 동일 수준) ⑦ sort 임포트 그룹. ⑤
  캐시 공유는 유지보치(24h 캐시와 즉시성 요구가 다름 — 룰링)

### v0.25.4 패치 (2026-11-08) — v0.25.x 패치 루프 4차: 이월 5건 완결

- [x] U2(medium) lazyDumpFile 부분 실패 방어 — 본문 Write 실패 시 해시
  갱신 중단·failed 마킹, close는 사이드카 없이 부분 파일 폐기(체크섬
  통과 오탐 백업 슬롯이 온전한 구 백업을 rotate에서 밀어내던 것 차단)
- [x] U4(low) pattern.go·usage.go 죽은 표면 주석 정정 —
  maintain --distill 플래그는 존재하지 않음(내부 유틸임을 명시)
- [x] U5(low) JSON-RPC 이월 유지 — 오류 응답은 프로토콜 준수 강화라
  별도 스코프(현 주요 클라이언트는 객체 params만 사용, 실발견률 낮음)
- [x] U6(low) MCP 스캐너 초과 줄 생존 — 4MB 상한 유지하되 ErrTooLong
  시 서버 사망 아닌 스킵 생존(재귀 재개) — 4MB 초과 flint_write 한
  번으로 세션 전체 상실 방어
- [x] U7(low) expire-pending dry-run 나열 상한 20(무상한 JSON 방어)
- [x] 버전 — VERSION·배지 v0.25.4(EN/KO)·plugin.json 동기

(v0.25.3 이월 — 처리 완료:
죽은 표면(distill 미실종)·U5 JSON-RPC 오류 응답 부재·U6 스캐너 4MB
상한 서버 사망·U7 expire-pending 무상한 나열.

### v0.25.2 패치 (2026-11-08) — v0.25.x 패치 루프 2차: rrf_k 주입 순서 배선 결함

- [x] 직접 검증 발견(사용자 챌린지 "잘 살펴봐") — SetRRFK 주입이 폴백
  재랭킹 뒤(제안 블록)에 있어 단발 CLI에서 rrf_k 오버라이드가 같은
  실행에 반영되지 않음. 주입을 재랭킹 앞으로 hoist·loadThresholdsFn
  seam으로 순서 회귀 테스트. 낡은 v0.13.0 재랭킹 주석 갱신
- [x] 버전 — VERSION·배지 v0.25.2(EN/KO)·plugin.json 동기

### v0.25.1 패치 (2026-11-08) — v0.25.x 패치 루프 1차: RRF 제4신호 topic 정합

- [x] 운영 관측 발견 — 폴백 재랭킹에서 topic 정합 신호가 융합에 빠져
  read_count 지배적 주제 외 행이 topic 정합 행을 누름(구문 경로
  RankOrder의 topic 우선 관례가 폴백 융합에서 소실). topicMatchCount
  제4신호로 회수 — 운영 실측에서 topic=2/rc=6 행이 rc=109 행 선회
- [x] 버전 — VERSION·배지 v0.25.1(EN/KO)·plugin.json 동기

### v0.25.0 마이너 (2026-11-08) — 검색 품질 1막: RRF 융합·자모 bigram·pgvector probe

마일스톤 G 추천 이행(계측 체계 선행 — 개선 효과 검증 즉시 가능):

- [x] R1 RRF(Reciprocal Rank Fusion) 융합 랭킹 — 폴백 재랭킹을 순차
  타이브레이크(정합>read)에서 정합수·read_count·자모 유사도 3신호
  RRF 융합으로(k=60). 구현 초판의 부호 반전 결함(값=rank 혼동)을
  순위 기반 rrfFuse로 수정·회귀 테스트로 봉쇄
- [x] R2 자모 bigram — 한글 토큰을 초·중·종성 분해 bigram으로 근사
  매칭(오타·어미 변형 흡수 — "백업"↔"백업용"·자동↔수동 부분 공유)
- [x] R3 pgvector 에뮬 실증 probe — ADR-0011: CREATE EXTENSION·
  vector(3)·cosine `<=>` 순위까지 실증 성공. 채택은 보류(임베딩 생성
  경로·인덱스 성능·트리거 미도달 — ADR에 명시)
- [x] 버전 — VERSION·배지 v0.25.0(EN/KO)·plugin.json 동기
- [x] 리뷰 7건 — ① 자모 신호 한글 토큰 한정(비한글 이중 계상 방지)
  ② RRF 계약 주석 갱신(구 순차 타이브레이크 서술 폐기) ③ rrf_k를
  thresholds 레지스트리로(SetRRFK 주입·tuning 노출·TUNING §2) ④
  bigramSet 재사용(자모 분해만 신규) ⑤ FormatRows 주석 복원 ⑥ 정규화·
  토큰 집합 hoist(행당 중복 계산 제거) ⑦ ADR URL 개행 수정

### v0.24.2 패치 (2026-11-08) — v0.24.x 패치 루프 2차: 개인화 평가 발견 — 결정 판정 adr 태그 전환

- [x] hot_view '최근 결정' 팔을 domain='decision'에서 tag='adr'
  기준으로(v0.23.2 정합에도 실사용 0건 — 결정은 domain=main+tag=adr
  관례. 개인화 평가 실측이 확정, 기획 리뷰 대안 b 채택). CONTENT §4에
  정준 경로 문서화·decision은 레거시 표기
- [x] 버전 — VERSION·배지 v0.24.2(EN/KO)·plugin.json 동기

### v0.24.1 패치 (2026-11-08) — v0.24.x 패치 루프 1차: profile_hint 조기 return 결함

- [x] 운영 관측 발견 — profile_hint가 ids 없는 read 이벤트만 있는
  운영에서 영구 공백: 힌트 할당이 hasIDs 조기 return 뒤에 있었다.
  표본 하한 판정은 ids 유무와 무관하므로 가드 앞으로 이동
- [x] 버전 — VERSION·배지 v0.24.1(EN/KO)·plugin.json 동기

### v0.24.0 마이너 (2026-11-08) — 개인화 2막 + 임계값 설계 완결

사용자 지시: 개인화 2막에 임계값 효과 실증·재조정 기준·조정 시점·
리스트 충분/과잉의 전체 설계 점검 포함(관측+완전성 감사+레퍼런싱 합성).

- [x] R1 튜닝 누수 4건 — reportStaleDays→stale_days 단일화(오버라이드
  우회 결함)·daily 보존 3곳→daily_retention_days 신규 키(Doltgres
  파라미터 회피: Go 계산 컷오프)·backup_interval_h 신규(주기 오버라이드,
  backup_keep과 세트)·tuning 커버리스 확장(hot_budget·backup_keep·
  backup_interval_h·exposure_profile)
- [x] R2 발동 창구 정당화(감사 ③-1) — done이 recall_starved_count meta
  적립(마커 병행) + doctor recall-starved 체크: minimal 프로파일이
  마커를 침묵시켜도 관측 유지. session-start 정비 요약(pending_max,
  24h 캐시 — 임계값 6키의 유일 창구가 수동 커맨드였던 것의 최소 연결)
- [x] R3 TUNING §0 조정 가이드 — 언제(관측 창 후)·재조정 트리거 표·
  표기 정정 3건(pending_max·drill_max_days 소비 경로, events_volume_warn
  방어 상한 성격)
- [x] R4 개인 어휘 meta 합집합(vocab_tags·vocab_domains — repo가 아닌
  운영 DB, 기본 집합과 union)·reset --all·consumer 표시·profile_hint
  (D① 축소형 — 표본 하한 미달 '보류', METRICS §5 보고 전용 준수)
- [x] R5 hot_events 원천 문서화(--full 수동 전용 — 운영 공백이 정상)
- [x] 설계 점검 결론 반영: 키 수 과잉 아님(제거 후보 0) — 부족한 것은
  누수 4건(수정)과 자동 노출(연결)이었다
- [x] 버전 — VERSION·배지 v0.24.0(EN/KO)·plugin.json 동기
- [x] 리뷰 7건 — tuning get meta키 분기·정비 캐시 실패 시 미갱신·
  recall 조회 실패 warn 위장 방지·status read_window_days 필드(거짓
  14d 명칭 방어)·SetVocabExtras 대체语义(mcp 장기 프로세스)·meta키
  기본값 소비자 상수 단일소스·profileHint 표본 하한 th 연동

### v0.23.2 패치 (2026-11-07) — v0.23.x 패치 루프 2차: 개인화 기획 리뷰 — 결함 1·문서 1

개인화 기획 리뷰(에이전트 설계 조사, 지시 반영) 결과 처리:

- [x] 결함 1(high) hot_view decision 팔 ↔ 어휘표 모순 — domain 어휘
  4종화(knownDomains·CONTENT §4에 decision 추가). 초기 스키마부터
  존재한 은폐 계약 불일치(어휘 준수 시 '최근 결정' 팔 공회전·
  decision 사용 시 unknown domain 경고)
- [x] 갭 TUNING §5 exposure_profile 문서화(값·효과·설정 경로)
- [x] 버전 — VERSION·배지 v0.23.2(EN/KO)·plugin.json 동기

v0.24 후보 적립(기획 리뷰 권고): 어휘 meta 오버라이드(vocab_tags·
vocab_domains 합집합)·tuning 커버리스 확장(hot_budget·backup_keep·
exposure_profile)·reset --all·consumer 표시·D① 축소형(profile_hint
표본 하한 포함·경계 내 안내)·D④ 관측 패치(도메인별 read 분해) 후
계층 도메인 §15 이연.

### v0.23.1 패치 (2026-11-07) — v0.23.x 패치 루프 1차: TUNING 관리 경로 문서화

- [x] TUNING §2 서두에 `flint tuning` 오버라이드 관리 경로 문서화
  (set 검증 규칙·개인값 저장 위치) — 새 관리면의 문서 정합
- [x] 운영 실증 — set→get(override 반영)→reset→get(기본 복귀) 왕복
- [x] 버전 — VERSION·배지 v0.23.1(EN/KO)·plugin.json 동기

### v0.23.0 마이너 (2026-11-07) — 개인화 1막: tuning 커맨드·어휘 제안

사용자 지시(다음 마이너 = D개인화 브레인스토밍 순서표):

- [x] D③ `flint tuning` — TUNING §2 전체 키의 list/get/set/reset.
  값 검증은 실경로 파싱으로(폴백되면 거부) — 문서·레지스트리·명령이
  단일 소스. 이전까지 psql 직접 갱신이던 관리 진입장벽 해소
- [x] drill_max_days 레지스트리 이관 — drillReport가 메타 직독에서
  thresholds 단일 소스로(키 메타 22종 전량 tuning 노출)
- [x] D② 어휘 제안 — 어휘 밖 tag·domain에 근사 후보(접두 우선·편집거리
  ≤2) 경고에 병합("유사: adr") — 오타 수정 가이드
- [x] 버전 — VERSION·배지 v0.23.0(EN/KO)·plugin.json 동기

### v0.22.2 패치 (2026-11-07) — v0.22.x 패치 루프 2차: 리뷰 6건 — 프록시·리다이렉트·테스트 seam

- [x] ① 프록시 회귀 수정 — updateClient가 DefaultTransport 복제로
  ProxyFromEnvironment 유지(v0.22.1의 bare Transport가 프록시 환경
  전체를 단절시켰던 것)
- [x] ② 리다이렉트 https 가드 — CheckRedirect로 https→http 다운그레이드
  거부(S1의 실질 방어 완결)
- [x] ③ InsecureSkipVerify 전역 제거 — 클라이언트 팩토리 seam +
  httptest srv.Client() 주입(프로덕션 바이너리에서 검증 비활성 코드
  소멸, gosec G402 해소)
- [x] ④ 다운로드 상한 초과 명시적 에러 — 체크섬 불일치 오탐 진단 방지
- [x] ⑤ API 응답 8MiB 상한 해석 — 메타 경로 무한 적립 방어
- [x] ⑥ README 태그라인 실내용 반영 — 이후 매 패치 갱신 관례 명시
- [x] 버전 — VERSION·배지 v0.22.2(EN/KO)·plugin.json 동기

### v0.22.1 패치 (2026-11-07) — v0.22.x 패치 루프 1차: update 경로 보강

- [x] S1 다운로드 URL https 강제 — API 응답의 browser_download_url이
  평문 스킴이면 거부(응답 변조·다운그레이드 방어). 테스트는 httptest
  TLS 서버 + 자체 서명 수용 seam(프로덕션 검증 불변)
- [x] S2 다운로드 상한 128MiB(io.LimitReader) — 무한 적립 방어,
  초과분은 체크섬 불일치로 즉시 중단
- [x] 버전 — VERSION·배지 v0.22.1(EN/KO)·plugin.json 동기

### v0.22.0 마이너 (2026-11-07) — 자동 업데이트 + go install 배포 경로

사용자 지시(브레인스토밍 확정: 출시≠실행 갭 — 루프가 직접 실증한 고통):

- [x] R1 `flint update` — 최신 Release 확인→자산 다운로드→checksums.txt
  sha256 검증(필수)→tar 추출→version 실행 검증(verify-then-swap)→
  원자 교체(rename). 실패 경로 전부 원본 불변. --check 드라이런
- [x] R2 세션 시작 갱신 안내 — 캐시 TTL 24h(하루 1회 왕복), [flint]
  경계 내 1줄, 네트워크 실패 조용히(§13 — 안내만, 자동 실행 없음)
- [x] R3 doctor update 체크 — 릴리스 지연 warn(안내만)
- [x] R4 모듈 경로 전환 `flint`→`github.com/synuns/flint`(임포트 63파일·
  Makefile·goreleaser ldflags) — `go install github.com/synuns/flint/
  cmd/flint@latest` 배포 경로 개통. ReadBuildInfo 폴백으로 go install
  빌드의 버전 자가 보고(CurrentVersion)
- [x] 버전 — VERSION·배지 v0.22.0(EN/KO)·plugin.json 동기

### v0.21.2 패치 (2026-11-07) — 패치 루프 8차: C③ PLAN 이력 아카이브

- [x] §14 마일스톤 과거 엔트리(v0.1~v0.18.x)를 docs/HISTORY.md로
  이관 — PLAN은 현행 시대(효과 계측 v0.19.0+)만 유지(868→456행).
  이력 무수정 원칙(이동 시점 그대로)
- [x] 문서 회귀 테스트의 과거 버전 계약행·커스텀 함수 4종을
  HISTORY.md로 재지정(커버리지 불변)
- [x] 버전 — VERSION·배지 v0.21.2(EN/KO)·plugin.json 동기

### v0.21.1 패치 (2026-11-07) — 패치 루프 7차: 어댑터 문서 스니펫 공식 검증

- [x] 신규 10종 스니펫을 공식 문서와 대조 검증(에이전트 웹 조사) —
  6종 정확·4종 수정: qwen(유효 JSON), zed(command 문자열·args 형제),
  continue(v1 스키마 메타데이터 필수), goose(command→cmd)
- [x] 전 10종에 등록 확인(`flint doctor --check harness-<name>`)·
  홈 경로 env 오버라이드 안내 보강
- [x] 버전 — VERSION·배지 v0.21.1(EN/KO)·plugin.json 동기

### v0.21.0 마이너 (2026-11-07) — 전 AI 에이전트 어댑터 지원 (adapter coverage)

사용자 지시(2026-11-07): 일부 하니스만 명시하던 어댑터를 모든 AI
에이전트로 확장. 외부 조사(공식 문서 기반) → A/B/C급 분류 → 구현.

- [x] R1 harnessSpec.MCPAdd 필드화 — mcpAddArgs switch 제거, 하니스
  추가가 레지스트리 1행으로 종결
- [x] R2 A급 8종 자동 등록 — 기존 5종 + Qwen Code·Devin CLI·Cline
  신규 + Copilot CLI 검증 승격(안내 전용 분기 폐지)
- [x] R3 B급 6종 의사체크 — Windsurf·Zed·Amp·VS Code(.mcp.json)·
  Goose·Trae: doctor harness 체크+문서(등록 CLI 없음 — 파일 설정형).
  룰링: continue는 설정 경로가 파일 단위로 확정되지 않아 문서 전용
- [x] R4 어댑터 문서 10종 신규 + copilot.md 승격 갱신 + README 하니스
  표(EN/KO) 확장 — `.mcp.json`이 Copilot CLI·VS Code 양쪽을 커버하는
  수렴 표준임을 문서화
- [x] 버전 — VERSION·배지 v0.21.0(EN/KO)·plugin.json 동기

### v0.20.6 패치 (2026-11-07) — 패치 루프 6차: 보안 경화 5건 + C② 위생

- [x] S1(medium) hot 출력 재살균 — read와 동일 방어(ADR-0006): 저장 후
  유입 [flint] 마커·제어 문자가 세션 시작 주입 경계를 위조하지 않게
- [x] S2(medium) 살균·비밀 스캔 필드 확장 — tags 배열 원소·domain·origin
  적용(이전 사각지대 — 비밀 경고 없이 저장·백업 영속)
- [x] S3(low) 백업 파일 권한 0600·디렉터리 0700(자동·수동 덤프·사이드카)
- [x] S4(low) restore 사이드카 부재 시 재생 전 차단 — 무검증 임의 SQL
  재생 폐지(종료 후 경고→가드, ADR-0006 즉시 중단 취지)
- [x] S5(low) 스킬 싱크 심볼릭 링크 skip(링크 대상 민감 파일 노출 차단)
- [x] C② meta 키 위생 — 고아 키(last_dup_manual_at, 코드 무참조) 운영
  정리·drill_max_days TUNING §2 등록(문서 미등록 오버라이드)
- [x] 검증 차단 확인 3건 — MCP 인자 주입·SQL 접합·read 이중 방어(스윕
  보고)
- [x] 버전 — VERSION·배지 v0.20.6(EN/KO)·plugin.json 동기

### v0.20.5 패치 (2026-11-07) — 패치 루프 5차: B② 세션 라벨 자동 주입

- [x] session-start 훅이 현재 세션 라벨을 config 경로 파일
  (session-current)에 기록, read가 --session 미지정 시 조용히 자동
  채택 — G4 라벨링이 에이전트 협조 없이 자동화. session-end가 정리
  (스테일 방지). 병렬 세션 last-writer-wins(룰링 — 보조 계정)
- [x] 버전 — VERSION·배지 v0.20.5(EN/KO)·plugin.json 동기

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
