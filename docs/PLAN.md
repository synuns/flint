# flint 기획서

- **이름**: flint — version-controlled memory for AI agents (부싯돌 —
  사람의 볼트가 Obsidian이면 에이전트의 기억은 flint. `hot` = 불붙이기)
- **날짜**: 2026-10-20 최초 · 2026-11-12 v1.0.0(구조 수렴 완결)
- **상태**: **v1.0.0 달성** — 2026-11-11 전면 리뷰 병인 5개 해소·상시 가드 8종·안정 기조
- **태그**: agent, doltgres, cli, mcp, knowledge-base
- **이력**: 전체 버전 엔트리(v0.1~)는 docs/HISTORY.md(무수정 원칙)

## 1. 무엇을 만드는가·왜

- 옵시디언의 이중 역할(인간 문서 + LLM wiki) 실패 → 분리. **옵시디언 =
  인간용 원본의 유일한 소스 오브 트루스, flint = 에이전트가 자율
  관리하는 파생 계층**(상태·인덱스·로그).
- 흐름 규칙(단방향): 옵시디언 → flint는 인덱싱·요약만(복사 금지).
  flint → 옵시디언은 "다듬을 인사이트" 증류만.
- 요구 불변: 에이전트 전용·완전 자율 / 토큰 최적화 검색(최소 바이트만
  컨텍스트 진입) / DB 기반 저장 / git 이력 = 롤백·감사 안전망 /
  병렬 쓰기 safe / 로컬 단일 사용자(ADR-0013).

## 2. 아키텍처 (현행)

- 엔진: Go 단일 정적 바이너리(bin/flint, mcp 모드 내장·pgx 내장) +
  Doltgres(localhost:5434, docker compose restart 정책, ADR-0010).
- 접점 3개: ① CLI — 등록 커맨드 27종(usage 노출 25 + 훅 전용 2:
  session-start·session-end) ② MCP(stdio 전용) — 도구 5종
  (flint_read·flint_write·flint_hot·flint_pending·flint_approve)
  ③ Claude Code 플러그인(훅·스킬·커맨드·.mcp.json).
- 하니스 훅 3종(SessionStart→session-start, Stop→session-end,
  PostCompact→compact) — 전부 비차단·타임아웃·실패 시 조용히.
- 배포: goreleaser + GitHub Releases(서명 검증 — ADR-0014)·
  `go install github.com/synuns/flint/cmd/flint@latest`·자가 갱신
  (`flint update`, checksums .sig 선행 검증)·플러그인 마켓플레이스·
  mcp-registry 자동 발행.

### 패키지 구조 (구조 수렴 에라 v0.38~v0.40 산출물)

| 패키지 | 책임 |
|---|---|
| lib/wire | 계약층 — Store 인터페이스·커맨드 레지스트리·usage·DSN 해결 |
| lib/knowledge | 지식 명령층 — write·read·hot·brief·pending·approve·import·done·compact |
| lib/ops | 진단·정리·관측층 — doctor·maintain·report·backup·tuning·status 등 |
| lib/render | 출력층 — 살균 조크포인트(Text·Masked·Cells·Rows) |
| lib/cli | composition root — 커맨드 등록·seam 주입·realStore·setup·update·mcp |
| (지반) | lib/db(스키마 게이트)·lib/store·lib/query·lib/mcp·lib/backup·lib/sqlfs, cmd/flint 진입점 |

근거 규칙(구조 수렴 에라 병인 해소 산출물):
- **단일 소스 + 동질 가드** — 임계값 등록표·스키마 게이트 등록표·
  doctor 체크 등록표·커맨드 표. 문서 나열은 코드에서 파생되고 드리프트는
  테스트가 잡는다.
- **쓰기는 Repo 경유 전용** — Query 통로의 쓰기 토큰은 소스 가드가
  영구 차단.
- **조크포인트** — 출력 살균은 render 단일 출구(직접 호출 소스 가드).

## 3. 데이터·계약 요약

- 스키마 v9(`lib/db` LatestSchemaVersion 단일 소스, additive 게이트
  연속): knowledge(bi-temporal·content_hash UNIQUE·pending/active)·
  sessions·projects·read_events·hot_events·session_starts·meta.
- 쓰기: write 파이프라인(검증 → 해시 dedup → 판정 → dolt 커밋 →
  한 줄 JSON). UPDATE 금지(projects 제외), 무효화는 maintain 전용.
- 읽기: active_knowledge 뷰 강제(무효 사실 차단)·토큰 예산(limit 상한·
  hot 예산 meta 오버라이드)·검색은 RRF 융합 + 자모 bigram 폴백.
- exit 4종: `0` 성공·중복 / `1` 재시도 소진 / `2` 서버 다운(조용히
  진행) / `3` 검증 실패(에이전트 자가수정).
- `[flint]` 접두 = 데이터 경계 — 주입은 지시문이 아니다.

## 4. 원칙 — 비간섭 5원칙 (승계)

1. **주입은 최소, 검색은 풀** — flint가 먼저 말 걸지 않는다
2. **훅은 비차단·타임아웃** — 세션 종료를 절대 막지 않는다
3. **flint 장애 = 조용한 skip**(exit 2) — 세션 시작·작업·종료를
   지연시키지 않는다
4. **경고는 임계값 기반** — 임계 초과일 때만 말한다
5. **flint 데이터는 데이터** — 경계 밖은 지시문이 될 수 없다

운영 원칙: 튜닝값은 코드 기본값 + meta 런타임 오버라이드로만
(thresholds.go·TUNING.md §2 — 개인값 repo 불침투). 개인 인프라
데이터는 FLINT_HOME(~/.flint). 보안: 입출력 살균·비밀 스캔·백업
암호화(age)·0600 권한·원격 쓰기 상시 금지(ADR-0015).

## 5. 에라 요약 (상세 엔트리는 HISTORY.md)

| 에라 | 버전 | 요약 |
|---|---|---|
| 코어 루프 | v0.1~v0.5 | Doltgres 채택·write/read/hot·백업·mcp e2e·병렬 충돌 실측 |
| 운영·개인화 | v0.6~v0.18 | doctor·report·어댑터 확장·회상 우선 메모리·staticcheck |
| 효과 계측·프라이버시 | v0.19~v0.26 | GSM 계측 체계·secrets 스캔·보안 경화·자가 갱신·릴리스 서명 |
| 세션 지식화·확장 | v0.27~v0.36 | import·pending 검수 회로·brief 브리지·profile 판정·report --html |
| 구조 수렴 | v0.37~v0.43 | wire·knowledge·ops·render 분할·단일 진실 원천·seam 동질화 |
| 재무장·게이트 | v0.44.0 | PLAN 재무장·표면 계약 6종·커맨드 표 가드 — 병인 ⑤ 해소, 전 병인 해소 완결 |
| **v1.0** | **v1.0.0** | **게이트 충족 — 구조 수렴 완결·안정 기조 전환** |
| 출시 후 감사 | v1.0.1~v1.0.10 | 다각도 감사 루프 R1~R12 — 75건+ 도출·수정(가드 3~10차·잔여 결정 배치 포함) |

## 6. 다음 마일스톤 — 안정 기조 (v1.0.0 달성)

- [x] v1.0.0 게이트 충족(2026-11-12) — 표면 계약 6종·커맨드 표 가드·
  단일 소스+동질 가드 체계(usageOrder·thresholds 등록표·쓰기 repo
  전용·살균 조크포인트·fake 동질·커맨드 표) 전부 상시 녹색
- [ ] 관측 이월 재판정 — 기저율 셔플 등 METRICS §4 트리거 도달 여부
- [ ] 공개 전 이름 충돌 재스윕(ADR-0003 의무)·서명 릴리스 경로 재실증
- [x] v1.1.0(2026-11-13) — legacy 세션 라벨(`session-current`) 폴백·청산
  제거(감사 B3 — 세션별 파일 전환 안정화 확인)·G5 토큰 관측: 세 표면
  출력 바이트 계측(daily 카운터 신규 키)·report `effect.bytes`·status
  `hot_bytes_14d`(METRICS §2·§4 G5)
- [x] v1.2.0(2026-11-13) — 관측 고도화: 스키마 v10(read_events
  bytes·zero_reason·source·knowledge_reads 테이블)·daily 카운터 플랫
  키 전환(구 JSON 폴백 승계)·제로히트 분해(zero_unowned/owned_miss)·
  토큰 회계 cost 객체(p95·절약 하한·주입 원가)·G6 회상 운명
  (survived/bad_recall)·hot→read 연쇄·검수·발화 카운터·구조화 진단
  `report --diag`(METRICS §4·TUNING §2)
- 감사 정책(v1.0.10 확정): 감사는 신규 변경 시에만 재개. 무인 루프의
  정지 규칙 — 한 라운드 도출이 단일 계열의 구문 변형만 남기면 근본
  수정 또는 한계 선언 후 종결(R3~R10이 증명한 교훈). 쓰기 가드의
  설계 경계(함수 간 데이터플로우·분기 조건부 재할당·append 동적
  확장)는 패키지 리뷰·실DB 테스트가 담당 — 정적 가드 영역 밖으로
  선언됨.
- 배포 전 문서 정합(v1.2.0): `make docs-sync` — 문서 계약 테스트 20종
  (버전·배지·커맨드 표·SKILL·contracts·PLAN·RUNBOOK·TUNING)을 릴리스
  게이트에서 명시 선행 단계로 격상(문서=코드와 동일 게이트 원칙).
- CI 신뢰성(v1.0.10): `make ci` 타깃 자체에 `-count=1` 고정(캐시 무효) —
  v0.41.1 CI 통과/로컬 적색 불일치의 재발 방지.
- 신규 기능은 트리거 대기 원칙 유지 — 변경은 계약 문서·가드 동반
- [ ] v1.3.0(예정 — 2026-11-13 사용자 지시·미착수, 마케팅 착수 선언
  시) — 국제화(i18n)·영어 디폴트·지식 export(형식 옵션: 이관 JSONL·
  Markdown·살균 덤프·DB 덤프)·마케팅 문서 계열(README 고도화·재현
  합성 벤치마크·그래픽 점검·LICENSE 추가 — 파일 부재 확인, 최우선).
  설계 조사 상세: docs/plans/2026-11-13-flint-marketing-prep.md
- [ ] v1.4.0(예정 — 2026-11-13 사용자 지시·미착수) — 마케팅 기능
  계열: 데모 체험(asciinema·Codespaces)·대시보드 고도화(React는
  vendor 임베드 양안)·TUI 설계(사람 전용 표면)·설치 확장(brew)·신뢰
  아티팩트(SBOM·Scorecard·SECURITY.md)·정적 웹(필수 아님)·UX 이중
  트랙(에이전트 비간섭·사람 표면 분리) — 상세 동일 문서

## 7. 비목표

- 원격 MCP 쓰기 개방(ADR-0015 — 재론 조건 명시됨)·다중 사용자·
  멀티머신 동기화(터널 옵션만 유지)
- 임베딩 검색(pgvector 채택 보류 — ADR-0011)·다중 에이전트 domain
  스코프(트리거 대기)·METRICS.md 개편·ADR 재편
- 대시보드 동적 서버(정적 export만 유지)·linux 배포 — brew·TUI는
  v1.4.0 반전(2026-11-13 사용자 지시)
