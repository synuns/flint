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
  <strong>에이전트의 기억을 버전 관리합니다.</strong>
  <br/>
  <sub>Claude Code·OpenClaw·Hermes가 공유하는 Doltgres 기반 세컨드 브레인.</sub>
</p>

<p align="center">
  <a href="README.md">English</a> | <a href="README.ko.md">한국어</a>
</p>

<p align="center">
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white" alt="Go 1.25"></a>
  <img src="https://img.shields.io/badge/DoltgreSQL-versioned_data-2f7d6f" alt="DoltgreSQL">
  <img src="https://img.shields.io/badge/MCP-native-8b5cf6" alt="MCP">
  <img src="https://img.shields.io/badge/harnesses-8-10b981" alt="하니스 8종">
  <img src="https://img.shields.io/badge/release-v0.16.7-blue" alt="v0.16.5">
</p>

<p align="center">
  <a href="#빠른-시작">빠른 시작</a> ·
  <a href="#왜-flint인가">왜 flint인가</a> ·
  <a href="#동작-원리">동작 원리</a> ·
  <a href="#커맨드">커맨드</a> ·
  <a href="#에이전트--어댑터">에이전트·어댑터</a>
</p>

**flint은 AI 코딩 에이전트에게 영속적이고 버전 관리되는 장기기억을 줍니다.**

**flint은 AI 코딩 에이전트에게 영속적·버전 관리되는 장기기억을 부여합니다 — 세션이 쌓일수록 더 똑똑해지도록.**

지금까지 쓴 모든 AI 에이전트는 매 대화를 백지에서 시작합니다. 프로젝트 구조를 다시 배우고, 이미 정한 결정을 다시 내리고, 이미 문서화한 함정을 다시 발견합니다. 기억상실증에 걸린 유능한 동료입니다.

flint은 이를 바꿉니다. 각 세션에서 에이전트가 배운 것 — 결정·관례·함정·선호 — 을 Doltgres(버전 관리되는 기억 저장소)에 기록합니다. 다음 세션 시작 때 flint이 축적된 맥락을 다시 주입합니다. **에이전트가 지식을 복리로 쌓습니다. 제로로 리셋하지 않습니다.**

1주차: 아키텍처를 배웁니다. 4주차: gRPC를 REST 대신 선택한 이유를 기억합니다. 12주차: 마이그레이션 순서·불안정 테스트 회피법·배포 체크리스트를 알고 있습니다 — 직접 기록했고, 지금 다시 읽기 때문입니다.

사람의 노트는 Obsidian(흑요석)에, 에이전트의 기억은 flint(부싯돌)에 — 불과 도구를
얻기 위해 두드리는 돌.

> v0.16.5 완료 (2026-11-05) — MCP 하네스 자동 등록(`setup --mcp`)·MCP Registry 배포·멀티플랫폼 .mcpb 번들. 로드맵: [docs/PLAN.md](docs/PLAN.md)

---

## 왜 flint인가?

대부분의 AI 에이전트는 매 세션을 백지에서 시작합니다. **flint을 활용하는 에이전트는 지금까지 배운 모든 것과 함께 시작합니다.**

| 문제 | 벌어지는 일 | flint의 해법 |
| :--- | :--- | :--- |
| **건망증** | 지난주 결정을 에이전트가 잊음 | `flint hot`이 세션 시작 때 예산 내 맥락 주입 |
| **맥락 유실** | 같은 배경설명을 반복해 붙여넣음 | `flint read`가 active_knowledge 뷰 검색 |
| **보이지 않는 편집** | 기억이 흔적 없이 변조됨 | Doltgres 셀 단위 이력 + `flint audit` |
| **에이전트 사일로** | 서로의 작업을 잊음 | CLI·MCP·플러그인 어댑터로 하나의 공유 저장소 |
| **취약한 복구** | 오염·유실이 곧 데이터 손실 | `flint restore --drill` 복구 훈련 + 백업 로테이션 |

---

## 빠른 시작

```bash
# 1. 설치 — Go 단일 바이너리, 의존은 pgx/v5뿐

# 방법 A: 릴리스 바이너리 다운로드 (v0.16.0부터)
#   태그 push 시 Release가 자동 생성된다 — 바이너리·checksums·플러그인 zip:
#   https://github.com/synuns/flint/releases

# 방법 B: 소스 빌드
git clone https://github.com/synuns/flint.git flint && cd flint
make install          # bin/flint 빌드
make build            # 운영 빌드, 버전은 최근 git 태그에서 파생

# 2. 원커맨드 설정 — Doltgres 기동 + 마이그레이션 + 안내
flint setup           # 소스 빌드는 ./bin/flint setup
make seed             # 예시 데이터, 멱등(해시 dedup)
```

### 다른 MCP 하네스 등록 (v0.16.5부터)

명령 하나로 설치된 모든 하네스를 탐지해 `flint` MCP 서버를 등록한다
(Claude Code는 위 플러그인 사용 — 여기는 나머지 하네스 대상):

```bash
flint setup --mcp
```

출력은 한 줄 JSON — `{"status":"ok","registered":[...],"skipped":[...]}`.
미설치 하네스는 `skipped`에 기록되고, 이미 등록된 경우도 무해하게 건너뛴다.
전부 스킵이어도 exit 코드는 0이다.

### MCP Registry 발행 (관리자)

`flint`는 플랫폼별 `.mcpb` 번들로 모든 Release에 첨부된다.
`mcp-registry/server.json`을
[MCP Registry](https://github.com/modelcontextprotocol/registry)에 발행하려면:

1. `brew install mcp-publisher`
2. `mcp-publisher login github`
3. `mcp-publisher publish mcp-registry/server.json` — 먼저 버전 URL과
   `fileSha256` 플레이스홀더를 실제 Release 값으로 교체한다
   (`shasum -a 256 dist/flint_<ver>_<os>_<arch>.mcpb`).

### Claude Code 플러그인 설치 (v0.16.0부터)

플러그인을 마켓플레이스에 공개했다(`.claude-plugin/marketplace.json`).
선행 조건: `flint` 바이너리가 `PATH`에 있을 것 — 플러그인 훅과 MCP 서버는
`PATH`의 `flint`를 호출한다.

```text
/plugin marketplace add synuns/flint
/plugin install flint@flint
```

확인:

```bash
./bin/flint read 덤프      # 검색: active_knowledge 뷰만
./bin/flint hot --session x  # 세션 시작 주입 (기본 예산 2,000자)
./bin/flint audit --limit 5  # 감사: dolt_log 최근 5건
```

<details>
<summary><strong>setup 중에 무슨 일이 벌어지나?</strong></summary>

1. `docker compose up -d`가 호스트 5434에 Doltgres를 띄운다 (ADR-0010).
2. 스키마는 첫 store 커맨드에서 자동 마이그레이션(멱등).
3. 시딩은 안내형 — 재실행 안전: 동일 payload는 `{"status":"duplicate"}` exit 0.
4. `flint-plugin/` 설치가 안내된다.

DSN 기본값 `postgresql://flint:flint-dev@localhost:5434/flint` — 환경변수
`FLINT_DSN`으로 덮어쓴다. 해제는 `./bin/flint teardown`(`--purge` 시 volume까지).
</details>

노출 프로파일(exposure profile): `flint setup --exposure normal|minimal`로
`hot` 주입량을 조정한다(기본 normal). 훅은 3엔트리로 통합 설치:
SessionStart(`session-start`)·PostCompact(`compact`)·Stop(`session-end`).

전체 안내: [docs/QUICKSTART.md](docs/QUICKSTART.md)

---

## 동작 원리

flint은 **에이전트**, **기억 저장소**, 그 **git 같은 이력** 사이의 다리입니다.

```text
[ Claude Code ] <----> [ flint ] <----> [ Doltgres ]
[ OpenClaw    ]          CLI + MCP        버전 관리되는
[ Hermes      ]          /plugin          기억 저장소
[ Codex · Gemini · Copilot · OpenCode · Cursor ]
        MCP + 지시파일 어댑터 (v0.9)
```

<p align="center">
  <img src="assets/flint-architecture.svg" width="640" alt="flint 아키텍처: 에이전트, flint CLI+MCP, Doltgres 기억 저장소">
</p>

| 단계 | 동작 |
| :--- | :--- |
| **주입** | 세션 시작: 훅이 `flint hot`을 실행해 2,000자 이내 `[flint]` 맥락 블록 스트리밍 |
| **작업** | 에이전트가 CLI 또는 MCP로 읽고 쓴다 |
| **기록** | `write` / `done`이 해시 dedup로 JSONL 추가 — 재실행도 멱등 |
| **검증** | `audit`는 `dolt_log` 조회; `restore --drill`은 원본 불변 복구 훈련 |

네 단계가 하나의 세션 라이프사이클을 이룹니다:

<p align="center">
  <img src="assets/flint-lifecycle.svg" width="640" alt="flint 세션 라이프사이클: 주입, 작업, 기록, 검증">
</p>

---

## 커맨드

| 커맨드 | 역할 | 설명 |
| :--- | :--- | :--- |
| `flint setup` | **부트스트랩** | Doltgres 기동(5434) + 마이그레이션 + 안내 |
| `flint write <kind>` | **기록** | stdin JSONL 추가; 해시 dedup 멱등 |
| `flint read <query>` | **조회** | active_knowledge 뷰 검색 |
| `flint hot --session <id>` | **주입** | 예산 내 세션 시작 맥락 |
| `flint done --session <id>` | **종료** | 세션 마무리 기록 |
| `flint approve --id n` | **큐레이션** | pending 지식을 active로 승격 |
| `flint maintain` | **큐레이션** | 큐레이션 세션: `--check` 임계값, `--invalidate`, `--merge` |
| `flint audit --limit n` | **감사** | `dolt_log` 최근 n건 |
| `flint backup [--out f]` | **백업** | 자체 포함 SQL 덤프 (ADR-0002 포맷) |
| `flint restore --file f [--drill]` | **복구** | 복원; `--drill`은 훈련, 원본 불변 |
| `flint status` | **점검** | 스키마 버전·pending·hot p95 |
| `flint mcp` | **서빙** | MCP 서버 실행 |

`flint read`는 구문 미스 시 단어 단위 재검색(fallback)한다.
`flint read`는 재검색까지 제로히트면 아무것도 출력하지 않고 exit 1로 종료한다(침묵 계약, v0.14.0) — 기존 근접 후보 suggestions 출력은 폐지했고, 근접 후보는 `zero_near` 지표로 내부 계측만 한다(에이전트 노출 없음).
출력 없는 exit 1 = 지식 없음(정상 침묵) · 오류 JSON 한 줄을 동반한 exit 1 = 실제 실패(저장소 오류, MapExit 기본).
`flint read --status pending`은 승격 대기 중인 지식 항목을 나열한다.
`flint read`는 랭킹 동률을 채택(read_count DESC) 순으로 재랭킹한다 — 인기는 최종 타이브레이크일 뿐(v0.13.0).
`flint maintain --conflict-suspects`는 모순 의심 지식(동일 topic·상이 요약)을 리스팅한다(v0.13.0).
백업 덤프는 sha256 사이드카로 무결성을 검증한다 — `flint done` 자동 백업 포함(v0.10.0).

exit 코드(v0.15.0 — 현행 de-facto 동작 명시):

| exit | 의미 |
| :--- | :--- |
| `exit 0` | 성공 |
| `exit 1` | 명령·검증 오류 — 오류 JSON 한 줄 동반(지식 없는 침묵 `read`도 무출력 exit 1 — 위 침묵 계약) |
| `exit 2` | 저장소 장애(`MapExit`) |
| `exit 3` | read 인자 규칙 위반 — 값 없는 trailing 플래그·무효 `--status` / `--as-of` 값 |

그 외: `version`, `teardown`. 운영 절차는 [docs/RUNBOOK.md](docs/RUNBOOK.md) · 튜닝은 [docs/TUNING.md](docs/TUNING.md)

---

## 에이전트·어댑터

| 대상 | 방식 | 위치 |
| :--- | :--- | :--- |
| **Claude Code** | `/flint` 커맨드 + SessionStart/Stop 훅 (`hot` 주입, `done` 기록) | [flint-plugin/](flint-plugin/) |
| **MCP 클라이언트** | `flint mcp` 서버 | `lib/mcp` |
| **OpenClaw** | 어댑터 | [docs/adapters/openclaw.md](docs/adapters/openclaw.md) |
| **Hermes** | 어댑터 | [docs/adapters/hermes.md](docs/adapters/hermes.md) |
| **Codex CLI** | MCP + AGENTS.md 어댑터 | [docs/adapters/codex.md](docs/adapters/codex.md) |
| **Gemini CLI** | MCP + GEMINI.md 어댑터 | [docs/adapters/gemini.md](docs/adapters/gemini.md) |
| **Copilot CLI** | MCP + instructions.md 어댑터 | [docs/adapters/copilot.md](docs/adapters/copilot.md) |
| **OpenCode** | MCP + AGENTS.md 어댑터 | [docs/adapters/opencode.md](docs/adapters/opencode.md) |
| **Cursor** | MCP + AGENTS.md 어댑터 | [docs/adapters/cursor.md](docs/adapters/cursor.md) |

---

## 저장소 구조

```
flint/
├── bin/flint           # Go 단일 바이너리
├── cmd/flint/          # main 진입점
├── lib/                # store · query · db · cli · backup · sqlfs · mcp
├── flint-plugin/       # Claude Code 플러그인 (commands · skills · hooks)
├── docs/               # PLAN · QUICKSTART · RUNBOOK · TUNING · ADR · adapters
├── data/               # seed.jsonl(추적) + 런타임(git 무시)
├── scripts/            # CI 검증
└── docker-compose.yml  # Doltgres (호스트 5434)
```

---

<p align="center">
  <em>"사람의 노트는 Obsidian에, 에이전트의 기억은 flint에."</em>
  <br/><br/>
  <strong>F L I N T</strong>
  <br/><br/>
  <code>v0.9.7 · DoltgreSQL · Go</code>
</p>
