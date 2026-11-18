# flint 런타임 검증 단(verify layer) — 설계·계획

- **날짜**: 2026-11-18
- **상태**: 승인됨(사용자 3문항 응답 + 자율 진행 사전 승인)
- **오케스트레이터**: AI(Ruling: 메모리상 사전 승인 — 문서 자기검토 후 즉시 진행)
- **플랜 detail**: tests(Ruling: 산출물이 스킬 문서·셸 스크립트·feature map이라 알고리즘 리스크 없음)

## 0. 사용자 결정 (2026-11-18 AskUserQuestion)

1. **사람 열람 시점**: 릴리스(G3)마다 — 에이전트가 매 버전 증거 패킷을 생성하고, 릴리스 승인 시에만 사람이 열람·승인.
2. **스코프**: 코어만 — verify-flint 스킬 + feature map(CLI 중심) + 증거 패킷 + tmux 캡처. MCP 재생 하니스·TLA+ formal은 다음 이터레이션.
3. **증거 형태**: tmux 텍스트 스냅샷 표준, GIF/영상(VHS)은 공개 데모 산출에만 선택 사용.

Ruling(자동 결정): 증거 보관 = `.superlite/verify/<버전>/`(로컬 한정, 커밋 금지 — CLAUDE.md "런 로그 로컬만"). 스킬 본체·feature map = repo 커밋(기여자 인프라). VHS tape은 이번 제외(스코프 2번 근거).

## 1. 목표

에이전트가 flint 실동작을 **스스로 구동·검증**하고 증거를 남겨, 사람은 릴리스
시점에 증거 패킷만 열람·승인하는 흐름을 확립한다. "에이전트가 실행 가능한
단계로 검증하고 사람 병목을 없앤다"(pstack guide 06)를 flint 루프(G2/G3)에
접지한다.

배경: pstack은 검증 스킬 **제너레이터**와 원칙만 번들 — 실체(스킬·feature
map·드라이브 하니스·증거 보관)는 전부 프로젝트 구축분(2026-11-17 조사).

## 2. 설계

### 단위 1 — verify-flint 스킬 (`.claude/skills/verify-flint/`)

- pstack `/create-verification-skill` 절차를 따라 생성(구성: Launch·Doctor·
  Drive·Evidence·Cleanup·Helpers + `features/` 맵). 초안은 생성기 관례를
  그대로, flint 특화만 보강한다(재설계 금지).
- **Launch**: doltgres 기동(make up)→ **검증 전용 DSN**(FLINT_TEST_DSN
  관례 승계)→ `make seed` 합성 시드. 개인 운영 DB는 어떤 경로로도
  드라이브하지 않는다(방어 계층 원칙).
- **Doctor**: 드라이브 전 `flint doctor`(read-only)로 건강 확인 — 실패 시
  드라이브 중단.
- **Drive / Evidence / Cleanup**: 기능별 드라이브는 feature map 파일이
  주도(셀렉터·명령·기대). 증거는 커맨드+출력+tmux 캡처를
  `.superlite/verify/<version>/` 에 기록, cleanup 후에도 생존.
- **Helpers**: `scripts/verify/tui_capture.sh`(단위 3).

### 단위 2 — feature map (`.claude/skills/verify-flint/features/`)

`README.md` 인덱스 + 기능당 파일 1개, H2 정확히 4개(Sub-features / How to
get to it (user POV) / Driving it with Claude Code / Gotchas). 초기 커버리지:

1. write 파이프라인(검증·dedup·판정·정규화·이스케이프·exit code)
2. read·hot(status·주입 예산·제로히트 exit 0)
3. export 5포맷(jsonl·md·txt·csv·pam)+ `--out -` stdout·사이드카
4. backup·drill(덤프·사이드카·복구 훈련)
5. status·doctor·report(운영 진단)
6. TUI(export·maintain·pending 폼 — tmux 구동, 텍스트 스냅샷)

MCP 제외(다음 이터레이션).

### 단위 3 — TUI 캡처 래퍼 (`scripts/verify/tui_capture.sh`)

tmux 새 세션→바이너리 구동→키 시퀀스 주입→`capture-pane` 텍스트 스냅샷
저장→세션 종료. 인자: 커맨드·키시퀀스·출력 경로. bash -n·shellcheck로 검증.

### 단위 4 — 증거 패킷 (`.superlite/verify/<버전>/`)

`manifest.md`(기능별: 드라이브 명령·요약·캡처 파일 목록·판정)+ `captures/`.
생성 주기 = G3(마일스톤/릴리스). 사람 열람 = 릴리스 승인 시. repo 비커밋.

### 단위 5 — 루프 접점 (LOOP.md G3 + ADR)

- LOOP.md §4 G3에 "verify-flint 증거 패킷 생성" 추가, §7 외부 승인 항에
  "릴리스 승인 = 증거 패킷 열람" 명시.
- ADR 1건(다음 번호): 검증 단 도입 결정(맥락·옵션·결정·되돌림 트리거).

### 오류 처리

- Doctor 실패·doltgres 기동 실패 → 드라이브 중단, manifest에 blocked 기록.
- 개별 드라이브 실패 → 해당 기능만 실패로 기록, 전체 중단 아님(증거는
  사실대로).
- 검증 DSN 미설정 → 드라이브 거부(개인 DB 오구동 방지가 우선).

### 테스트

- tui_capture.sh: bash -n + 실구동 1회(라이브 인도 드라이브에서).
- 스킬·맵: 문서 — generator의 인도 조건(launch→doctor→기능 1개
  드라이브→증거→cleanup→증거 생존 실측 1회)으로 실증.
- 기존 go 테스트·make ci 무영향 확인.

### 기각한 대안

- 증거를 repo `docs/verify/`에 커밋: CLAUDE.md 계약(런 로그 로컬만) 위반.
- MCP 하니스 동시 구축: 코어 안정화 후로 미룸(사용자 결정 2).
- superlite subagent-driven-development로 디스패치: `.superlite` 런 상태를
  활성 v1.7.x 루프 세션과 공유해 충돌 위험 — poteto-agent 직접 디스패치로
  대체(LOOP.md §2 계약 준수, 스윕에서 실증된 방식).

## 3. 계획 (taskgraph)

```
T1 브랜치·워크트리(feat/verify-layer) + 스킬 초안 생성
   └ T2 flint 특화 보강(Launch·Doctor·Evidence·Cleanup·검증 DSN 가드)
      ├ T3 feature map 작성(커버리지 6 — 병렬 가능)
      └ T4 tui_capture.sh + TUI 드라이브 정의
         └ T5 라이브 인도 드라이브 1회(합성 DB: write 경로+TUI 1화면→증거 패킷 실증)
            └ T6 LOOP.md G3 갱신 + ADR
               └ T7 게이트(make ci·go test 무영향)·커밋·PR(푸시 전 사용자 확인)
```

- T1·T2·T6은 코디네이터 인라인(문서 중심), T3·T4는 poteto-agent 파견,
  T5는 코디네이터가 실측(Prove It Works).
- 커밋 단위: 태스크 1개 = 커밋 1개. 게이트: 문서 태스크은 make ci,
  스크립트는 bash -n+실구동, T7은 go test ./... -count=1 무영향 확인.

## 4. 게이트 결과 (2026-11-18, 리뷰 반영 후)

- make ci: green(go test 전 패키지 + check_personal)
- tui_capture.sh: bash -n 통과·실구동 재검증(remain-on-exit·폴링 강화 후
  `down` 키 입력→선택 이동 캡처 확인). shellcheck 미설치로 bash -n만.
- 최종 리뷰: CHANGES_REQUESTED 7건 중 6건 반영·1건(export.md 낡은
  Gotcha)은 comment-sicko 단계에서 선반영 — 전 건 처리. MUST KILL
  2건(ClampLimit·DashPositional)은 백로그 이월.
- 라이브 인도 드라이브 증거: .superlite/verify/1.7.2/(로컬).
