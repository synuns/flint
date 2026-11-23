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
  <img src="https://img.shields.io/badge/release-v1.26.1-blue" alt="v1.26.1">
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

> v1.26.1 · 표면 신뢰 — 업데이트 캐시 3원칙(ETag 조건부 재검증: 304면 캐시 latest 승계·모순 검출기: 실행 버전보다 오래된 캐시는 즉시 재 fetch·`checked_at` `report --text` 노출)·`flint seed --demo`(내장 합성 데이터셋을 격리 `_demo` DB에 적재·`--reset --yes`는 데모 행만 삭제)·doctor `versions`(자가 버전·캐시 latest·PATH·mcp-registry 나란히)·threshold `update_cache_age_h` · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · r108-r109 패치 — gc-pending JSON 마감 수정(`maintain --gc` 사전 안내 라인이 JSON으로 파싱됨)·방언 프로브 이전(`dolt_log_depth` SQL의 lib/db 소유 확정 — `db.DoltLogDepth`, doctor는 호출만)·깊이 프로브 numeric 수정(`commit_order::int8` + `CellToInt` pgtype.Numeric — 실엔진에서 깊이가 0으로 읽히던 결함)·`db_size` Doltgres 상시 skip 문서화·engine-footprint 타임아웃 2s→5s·단독 `--shallow` 문구 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · 엔진 운영 — `maintain --gc --yes`(dolt 가비지 컬렉션: 접속 단절 안내 → 재연결 루프 → 스키마 재검증 → meta 이력)·doctor `dolt_log_depth`(O(1))·`db_size` 경고 + `engine-footprint` 표시·RUNBOOK §10/§11·thresholds `dolt_log_depth_warn`·`db_size_warn_mb`·ANALYZE 폐기(무연산 실증)·audit_log 리플레이 스파이크 성공(순수 PG) · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · r106 패치 — race-recheck echo 커버리지(레이스 재확인 중복의 echo 적립 fake 회귀 추가)·quiet-failure 회귀(`echo_candidates` INSERT 실패가 write를 실패시키지 않음)·echo 스캔 주석(pending 행의 기존 후보 포함 서술) · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · r104-r105 패치 — `echo_candidates` 적립 회로 연결(write 시점 echo 후보 저장: 신규 행 id, 중복 재전송은 기존 행 기준)·merge-audit active 한정 페치(pending 배제)·`--merge`의 D4 처분 카운터 계상·int4 입력 가드(`--recall-audit --limit`·`read --related`) · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · 지식 관리 잔여 — 스키마 v19(`kind`·`source_path`·`echo_candidates`)·`maintain --merge-audit`(중복 쌍 보고, 비용 추정 동반)·`--recall-audit`(나쁜 회상 환류 보고)·`read --related`(무방향 이웃 링크)/`--why`(토큰 tier·RRF 신호·제로히트 근거) · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · r102 패치 — 카운터 자릿수 상한(shadow 카운터 업서트 18자리 가드)·v1.23.0 스펙 정렬(경계 발화·미옵 비용)·what-if p50/p90 도메인 필터 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · 자율 튜닝 shadow — 스키마 v18 `tuning_predictions`(예측 기록만, 적용 0건)·`auto_mode` 정책 스위치(기본 off, 화이트리스트: `suggest_min_score`)·read 꼬리 shadow 라운드(24h 게이트·streak 규칙)·`flint tuning what-if`/`status`(재분류·p50/p90·드리프트 카드) · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · r96-r98 패치 — 병렬 재매치 스탬프 보존(해시 동일 재적용 시 유지)·maintain --recheck --conditions 갱신·flint distill 파일 상한(distill_max_runes)·동분 덮어쓰기 금지·id/min-score 입력 가드 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · r91-r94 패치 — 스캔 오류 경로 무관화·볼트 루트 가드(성공 후 영속)·튜닝 키 접속·스캔 경화(재시도·터치 갱신·심볼릭 링크 스킵·60k+ 삭제) · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · 볼트 브리지 I — 스키마 v16 `vault_files`·`flint index`(스냅숏-디프, 디프마다 배치 1커밋)·`read` 볼트 통합(`source:"vault"`, 상한 5)·스캔 p95 계측 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · r89 패치 — merge·import 계보 복원의 supersede 락 도메인 편입(사이클 봉쇄) · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · r88 패치 — advisory-lock 단일 커넥션 supersede·PAM 자기참조 링크 skip·절단 방어 테스트 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · r84-r87 패치 — 링크 적립 완전성(혼합 배치·PAM 왕복)·supersede 경합 강화·스키마 v15 링크 유니크 인덱스 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · 지식 스키마·검색 — 스키마 v14 지식 링크(refines·supports·contradicts·duplicates)·read `--min-confidence`/`--origin` 필터·supersede 사이클 가드·체인 뷰 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · observability-foundation 패치 — 스키마 v13(near_score·임계값 스냅숏·주간 롤업)·계측 신선도·커버리지 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · R69 — grammar fix · [full changelog](docs/HISTORY.md)
> v1.26.1 · r65-dedup — correction 해시 origin 제외 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · r64-observability — origin 추적 수정 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · echo — 쓰기 시점 모순 감지 · 관측 키 3종 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · R62 — highlight order contract tests (latest-first + EN/KO parity) · [full changelog](docs/HISTORY.md)
> v1.26.1 · 자동 업데이트 · go install · 전체 배포 폐회 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · PLAN 재무장 · 표면 계약 6종 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · 계측 귀속·sync 완화·probe heal(R38) · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · R42 하드닝 — 다중 기기 이식(jsonl 회상 재생)·회상 이벤트 수명 관리 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · R43 회복성 — import 회상 이관 회복성·신규 DB 스키마 직행·문서 정합 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · TUI·성능 — 저화면 TUI 크롬 최소화·report 병렬 수집·tuning --tui 옵트인 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · 위생(hygiene) — 424개 파일 주석 영어화·slow 스위트 34.5% 단축·테스트 레이스 수정 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · R47 — v1.10.0 수치 정정 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · R48 — 기록 위생(중복 헤딩·순서) · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · 지식화 파이프라인 1단계 — 사용자 교정 채널(`--source correction`) · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · R50 — dup_correction 계량 수리 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · r52-reporting — dup_correction report 노출 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · 지식화 파이프라인 2단계 — 회상 실패 격차 후보 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · r54-docs — RUNBOOK 파이프라인 2단계 기록·NFKC 절단 수리 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · 지식 수명주기 — 낡음 점수·변동성 계층 · [전체 변경 이력](docs/HISTORY.md)
> v1.26.1 · 지식화 파이프라인 3단계 — 자동 교정 감지(A안 훅)·survived 감쇠 완화 · [전체 변경 이력](docs/HISTORY.md)
---

## 빠른 시작

```bash
# 사전 빌드된 바이너리 설치 (sha256 검증 포함) → ~/.local/bin
curl -fsSL https://raw.githubusercontent.com/synuns/flint/main/scripts/install.sh | bash

# Doltgres 기동 · 스키마 마이그레이션
flint setup

# 첫 기억 기록
echo "API 게이트웨이는 멱등 POST를 2회 재시도한다" | flint write knowledge
```

선택 데모 데이터: `flint seed --demo` — 내장 합성 데이터셋을 격리 데모 DB(DSN의 db명 + `_demo`)에 적재합니다(멱등, `--reset --yes`는 데모 행만 삭제). 체크아웃한 저장소에서는 `make seed FLINT_SEED_DSN=postgresql://flint:flint-dev@localhost:5434/flint`로 명시적 DSN에 적재.

> 모든 릴리스 자산에는 SLSA 빌드 증명(build provenance)이 첨부됩니다 — 내려받은 파일은 `gh attestation verify <파일> --repo synuns/flint`로 검증할 수 있습니다.

### 바이너리 설치

네 가지 경로 — 하나만 고르면 됩니다. 어떤 경로든 다음 단계는 `flint setup`(Doltgres, 포트 5434)입니다.

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

Docker로 Doltgres를 5434 포트에 띄우고, 스키마를 마이그레이션하고, 다음 단계를 안내합니다. 기본 DSN은 `postgresql://flint:flint-dev@localhost:5434/flint`이며 `FLINT_DSN`으로 바꿀 수 있습니다. 정리는 `flint teardown`으로.

</details>

<details>
<summary>노출 프로필 설정</summary>

`flint setup --exposure normal|minimal`로 세션 시작 시 주입되는 맥락의 양을 조절합니다. `minimal`이면 헤드라인 한 줄만 나갑니다. 훅은 3개: SessionStart(`session-start`), PostCompact(`compact`), Stop(`session-end`).

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
- **데이터 소유는 사용자.** 모든 데이터는 여러분의 Doltgres와 `~/.flint`에 있습니다 — 언제든 export(`flint export`, Ed25519 서명 가능한 PAM), `age` 암호화 백업, 기록·백업 시 비밀 스캔, 원격 라이브 쓰기는 설계상 금지([ADR-0015](docs/adr/0015-bridge-surface.md)).

---

## 커맨드

| 커맨드 | 설명 |
| :--- | :--- |
| `flint setup` | Doltgres 기동 · 마이그레이션 · 시드/플러그인 다음 단계 안내 |
| `flint seed --demo` | 내장 합성 데모 데이터셋을 격리 데모 DB에 적재(db명 + `_demo`, `FLINT_DEMO_DSN` 오버라이드; 멱등; `--reset --yes`는 `demo/` 행만 삭제 — 소유자 불일치 exit 3) |
| `flint update` | 최신 릴리스 자가 업데이트(체크섬 검증·`--check` 드라이런) |
| `flint tuning` | 임계값 키 조회·오버라이드(`list`/`get`/`set`/`reset`; 자율 튜닝 표면 `auto off\|shadow`·`what-if`·`status`; 옵트인 `--tui` 폼) |
| `flint write <kind>` | 표준 입력에서 지식 기록 (멱등) |
| `flint import [path]` | 규칙 파일(CLAUDE.md 등)을 pending 지식으로 변환(`--commit`); `--format jsonl\|pam` 지식 재이식·수입(jsonl 왕복·PAM v1.0) |
| `flint export [--format f]` | 지식 export jsonl·md·txt·csv·pam(PAM v1.0 표준 memory-store; `--status`·`--tag`·`--since` 필터, `--out` 파일 + `.sha256` 사이드카, PAM `--sign` Ed25519 서명 — 스펙 §18) |
| `flint index [--vault p]` | 마크다운 볼트를 `vault_files`에 색인(스냅숏-디프, 디프마다 배치 1커밋; `--vault` 경로 저장, `--limit` 스캔 상한) |
| `flint read <query>` | 활성 지식 검색 (색인된 볼트 행은 지식 블록 뒤에 추가); 옵트인 `--related N`은 무방향 지식 링크 이웃을 뒤에 추가(상한 5)·`--why`는 행별 근거 부록(토큰 tier·RRF 신호·제로히트 사유) — 기본 출력은 불변 |
| `flint hot --session <id>` | 세션 시작 맥락 주입 |
| `flint brief [query]` | 외부 AI에 붙여넣는 마스킹 브리프(읽기 전용) |
| `flint done --session <id>` | 세션 마무리 기록 |
| `flint approve --id n` | 대기 중 지식을 활성으로 승격 |
| `flint maintain` | 관리: 임계값 확인 · 무효화 · 병합 · `--merge-audit` 중복 쌍 보고(multiset Dice·비용 추정 동반·리스팅만) · `--recall-audit` 나쁜 회상 환류 보고(리스팅만 — 처분은 항상 승인제 서브커맨드로) · `--age-audit` 낡음 보고(suggested_vol 제안 동반) · `--reaffirm` 유효기간 갱신 · `--recheck` 조건 재검토 · `--gc [--shallow] --yes` dolt 가비지 컬렉션(운영자 명령 — 전 세션 접속 단절, 같은 실행 안에서 재연결+스키마 재검증) (`--tui` 대화형 검수, 옵트인) |
| `flint distill --out DIR` | 회상 점수가 임계 이상인 미출력 활성 지식을 마크다운 파일 1개로 출력(`--min-score`는 distill_min_score 대체; 출력 행은 distilled_at 스탬프 — 재실행은 잔여분만 — ADR-0018) |
| `flint audit --limit n` | 최근 n개 커밋 조회 |
| `flint backup` | SQL 덤프 + sha256 체크섬 (`--encrypt` age 저장 암호화·`--mask` 치환 진단 덤프) |
| `flint restore --file f` | 백업 복구 (`--drill`로 훈련) |
| `flint sync` | Doltgres 원격과 지식 동기화 — `remote add/list/remove` (url 마스킹), `push`/`pull [--dry]`; 병합 충돌 시 exit 1과 수동 해결 안내; 원격 url은 어떤 출력·로그에도 남지 않는다 (사람 운영자 명령) |
| `flint profile` | 프로파일 판정: 근거 지표+추천, `--apply normal\|minimal` |
| `flint status` | 스키마 버전 · 대기 건수 · hot p95 |
| `flint mcp` | MCP 서버 (read·write·hot·pending·approve 도구) |
| `flint pending` | 대기 지식 검수 뷰 (승격·병합·만료·보류 판정, `--json`) |
| `flint compact` | compact 요약을 pending 지식으로 적립 (PostCompact 훅 진입점) |
| `flint nudge` | compact가 남긴 마커의 세션 종료 증류 리마인더 (`--clear` 종결) |
| `flint doctor --check c` | 건강 검진 21종 (`--check` 서브셋, fail 시 exit 1) |
| `flint report --days n` | 일별 유입·zero_pct·p95·stale 리포트 (`--html` 정적 대시보드) |
| `flint diff <from> <to>` | 두 as-of 지점 간 지식 변경 비교 |
| `flint teardown` | 설정 해제 (훅·설정, `--purge` 시 데이터도 제거) |
| `flint version` | 버전 출력 |

**종료 코드:** 0 성공 (`read` 제로히트도 exit 0 — 텍스트는 침묵, `--json`은 `{"hits":0}` 한 줄) · 1 오류 · 2 저장소 장애 · exit 3 — 잘못된 입력 (값 없는 trailing 플래그, 잘못된 `--status`·`--as-of` 값, `write` 스키마 위반).

`flint read`는 구문 검색이 무결과면 단어 단위 fallback으로 재시도합니다.

기억처럼 감쇠하는 기억: `read`가 적중할 때마다 recall_score가 강화되고, 미사용
항목은 매일 감쇠(하한 0)하며 `hot`/`brief` 순위에 반영된다 —
`flint maintain --decay-audit`는 archive 후보를 제안하고, 실행은 항상 승인제다.

### 대화형 TUI (v1.5.0)

`flint setup`(무인자) 위저드와 `pending`·`export`·`report`의 4화면. 터미널에서는 기본 대화형 — 무인자 실행 시 TUI가 열린다(v1.5.2부터). 파이프·비터미널 실행과 플래그 지정 시 기존 출력이 그대로 유지된다. `flint maintain --tui`(v1.6.0)는 옵트인 관리 화면 — 의심 목록 → 병합 시 무엇이 남는지 미리보기 → 병합/유지/무효화, 실행은 기존 서브커맨드에 위임.

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
