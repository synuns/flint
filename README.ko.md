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
  <img src="https://img.shields.io/badge/release-v0.5-blue" alt="v0.5">
</p>

<p align="center">
  <a href="#빠른-시작">빠른 시작</a> ·
  <a href="#왜-flint인가">왜 flint인가</a> ·
  <a href="#동작-원리">동작 원리</a> ·
  <a href="#커맨드">커맨드</a> ·
  <a href="#에이전트--어댑터">에이전트·어댑터</a>
</p>

**flint은 AI 코딩 에이전트에게 영속적이고 버전 관리되는 장기기억을 줍니다.**

에이전트는 유능하지만 세션 사이에서 모든 것을 잊습니다. flint은 프로젝트 결정·
세션 노트·선호를 Doltgres — 셀 단위 변경이 커밋이 되는 Postgres 호환 데이터베이스 —
에 저장하고, 세션 시작 때 맥락을 다시 주입합니다. 모든 쓰기는 감사 가능하고
모든 기억은 복구 가능합니다.

사람의 노트는 Obsidian(흑요석)에, 에이전트의 기억은 flint(부싯돌)에 — 불과 도구를
얻기 위해 두드리는 돌.

> v0.5 완료 (2026-10-22) — 신뢰성·검색 품질. 로드맵: [docs/PLAN.md](docs/PLAN.md)

---

## 왜 flint인가?

대부분의 AI 에이전트는 매 세션을 백지에서 시작합니다.

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
git clone https://github.com/synuns/flint.git flint && cd flint
make install          # bin/flint 빌드

# 2. 원커맨드 설정 — Doltgres 기동 + 마이그레이션 + 안내
./bin/flint setup
make seed             # 예시 데이터, 멱등(해시 dedup)
```

확인:

```bash
./bin/flint read 덤프      # 검색: active_knowledge 뷰만
./bin/flint hot --session x  # 세션 시작 주입 (기본 예산 2,000자)
./bin/flint audit --limit 5  # 감사: dolt_log 최근 5건
```

<details>
<summary><strong>setup 중에 무슨 일이 벌어지나?</strong></summary>

1. `docker compose up -d`가 호스트 5433에 Doltgres를 띄운다 (ADR-0001).
2. 스키마는 첫 store 커맨드에서 자동 마이그레이션(멱등).
3. 시딩은 안내형 — 재실행 안전: 동일 payload는 `{"status":"duplicate"}` exit 0.
4. `flint-plugin/` 설치가 안내된다.

DSN 기본값 `postgresql://flint:flint-dev@localhost:5433/flint` — 환경변수
`FLINT_DSN`으로 덮어쓴다. 해제는 `./bin/flint teardown`(`--purge` 시 volume까지).
</details>

전체 안내: [docs/QUICKSTART.md](docs/QUICKSTART.md)

---

## 동작 원리

flint은 **에이전트**, **기억 저장소**, 그 **git 같은 이력** 사이의 다리입니다.

![flint 세션 데모](assets/flint-demo.gif)

*실제 세션: `hot`이 문맥을 주입하고, `write`가 결정을 기록하고(멱등·해시 dedup), `read`가 회상한다. GIF는 `vhs assets/flint-demo.tape`로 재생성.*

```text
[ Claude Code ] <----> [ flint ] <----> [ Doltgres ]
[ OpenClaw    ]          CLI + MCP        버전 관리되는
[ Hermes      ]          /plugin          기억 저장소
[ Codex · Gemini · Copilot · OpenCode · Cursor ]
        MCP + 지시파일 어댑터 (v0.9)
```

| 단계 | 동작 |
| :--- | :--- |
| **주입** | 세션 시작: 훅이 `flint hot`을 실행해 2,000자 이내 `[flint]` 맥락 블록 스트리밍 |
| **작업** | 에이전트가 CLI 또는 MCP로 읽고 쓴다 |
| **기록** | `write` / `done`이 해시 dedup로 JSONL 추가 — 재실행도 멱등 |
| **검증** | `audit`는 `dolt_log` 조회; `restore --drill`은 원본 불변 복구 훈련 |

---

## 커맨드

| 커맨드 | 역할 | 설명 |
| :--- | :--- | :--- |
| `flint setup` | **부트스트랩** | Doltgres 기동(5433) + 마이그레이션 + 안내 |
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
└── docker-compose.yml  # Doltgres (호스트 5433)
```

---

<p align="center">
  <em>"사람의 노트는 Obsidian에, 에이전트의 기억은 flint에."</em>
  <br/><br/>
  <strong>F L I N T</strong>
  <br/><br/>
  <code>v0.5 · DoltgreSQL · Go</code>
</p>
