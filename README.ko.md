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
  <sub>세션이 쌓일수록 에이전트가 똑똑해집니다 — Claude Code, Codex, Gemini, Copilot, Cursor 지원</sub>
</p>

<p align="center">
  <a href="README.md">English</a> | <a href="README.ko.md">한국어</a>
</p>

<p align="center">
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white" alt="Go 1.25"></a>
  <img src="https://img.shields.io/badge/DoltgreSQL-versioned_data-2f7d6f" alt="DoltgreSQL">
  <img src="https://img.shields.io/badge/MCP-native-8b5cf6" alt="MCP">
  <img src="https://img.shields.io/badge/harnesses-8-10b981" alt="8 하네스">
  <img src="https://img.shields.io/badge/release-v0.17.0-blue" alt="v0.17.0">
</p>

<p align="center">
  <a href="#빠른-시작">빠른 시작</a> ·
  <a href="#동작-원리">동작 원리</a> ·
  <a href="#커맨드">커맨드</a> ·
  <a href="#에이전트--어댑터">에이전트</a>
</p>

AI 코딩 에이전트는 세션이 끝나면 모든 걸 잊습니다. 지난주에 정한 아키텍처, 한 달 전에 문서화한 함정, 어제 겪은 배포 이슈 — 전부 백지 상태로 다시 시작합니다. 실력은 좋은데 건망증이 심한 동료와 같습니다.

flint는 이걸 바꿉니다. 에이전트가 무언가 배우면 — 결정, 관례, 주의사항 — flint가 Doltgres에 기록합니다. Doltgres는 모든 변경이 커밋으로 남는 데이터베이스라서, 기억이 쌓이는 과정 자체가 이력으로 보존됩니다. 다음 세션이 시작되면 flint가 그동안 쌓은 맥락을 다시 주입합니다. 에이전트는 처음부터 다시 배우는 게 아니라 지난번에 이어서 작업합니다.

효과는 복리로 쌓입니다. 첫 주에는 기술 스택을 익히고, 한 달쯤 지나면 gRPC를 REST 대신 선택한 이유를 기억하고, 석 달쯤 되면 마이그레이션 순서와 불안정한 테스트 회피법, 배포 체크리스트까지 알고 있습니다. 직접 기록해뒀으니까요.

사람은 Obsidian에 노트를 남깁니다. 에이전트는 flint에 기억을 남깁니다 — 불과 도구를 얻기 위해 두드리는 부싯돌처럼.

> v0.17.0 · Dependabot · setup UX · CI 설계 리뷰 · [전체 변경 이력](docs/PLAN.md)

---

## 빠른 시작

```bash
# 설치
git clone https://github.com/synuns/flint.git && cd flint
make install

# Doltgres 기동 · 스키마 마이그레이션 · 데모 데이터
flint setup
make seed
```

[릴리스 바이너리](https://github.com/synuns/flint/releases)를 받아도 됩니다 — 바이너리, 체크섬, 플러그인 zip이 자동으로 첨부됩니다.

### Claude Code

```text
/plugin marketplace add synuns/flint
/plugin install flint@flint
```

플러그인은 `flint` 바이너리가 `PATH`에 있어야 합니다. 훅과 MCP 서버가 모두 이 바이너리를 호출합니다.

### 다른 에이전트 (Codex, Gemini, Cursor, OpenCode, Copilot)

```bash
flint setup --mcp
```

설치된 하네스를 자동으로 감지해서 MCP 서버를 등록합니다. 미설치 하네스는 건너뛰고, 결과는 한 줄 JSON으로 출력됩니다.

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

<p align="center">
  <img src="assets/flint-lifecycle.svg" width="880" alt="flint 세션 라이프사이클 — 기억이 축적되는 순환">
</p>

---

## 커맨드

| 커맨드 | 설명 |
| :--- | :--- |
| `flint setup` | Doltgres 기동 · 마이그레이션 · 시드 |
| `flint write <kind>` | 표준 입력에서 지식 기록 (멱등) |
| `flint read <query>` | 활성 지식 검색 |
| `flint hot --session <id>` | 세션 시작 맥락 주입 |
| `flint done --session <id>` | 세션 마무리 기록 |
| `flint approve --id n` | 대기 중 지식을 활성으로 승격 |
| `flint maintain` | 관리: 임계값 확인 · 무효화 · 병합 |
| `flint audit --limit n` | 최근 n개 커밋 조회 |
| `flint backup` | SQL 덤프 + sha256 체크섬 |
| `flint restore --file f` | 백업 복구 (`--drill`로 훈련) |
| `flint status` | 스키마 버전 · 대기 건수 · hot p95 |
| `flint mcp` | MCP 서버 실행 |

**종료 코드:** 0 성공 · 1 오류 (또는 `read` 무결과 침묵) · 2 저장소 장애 · exit 3 — read 인자 규칙 위반 (값 없는 trailing 플래그, 잘못된 `--status`·`--as-of` 값).

`flint read`는 구문 검색이 무결과면 단어 단위 fallback으로 재시도합니다.

운영: [docs/RUNBOOK.md](docs/RUNBOOK.md) · 튜닝: [docs/TUNING.md](docs/TUNING.md)

---

## 에이전트 & 어댑터

| 에이전트 | 방법 |
| :--- | :--- |
| **Claude Code** | 플러그인: 훅 + `/flint` 커맨드 |
| **Codex** | MCP ([docs/adapters/codex.md](docs/adapters/codex.md)) |
| **Gemini** | MCP ([docs/adapters/gemini.md](docs/adapters/gemini.md)) |
| **Copilot** | MCP ([docs/adapters/copilot.md](docs/adapters/copilot.md)) |
| **Cursor** | MCP ([docs/adapters/cursor.md](docs/adapters/cursor.md)) |
| **OpenCode** | MCP ([docs/adapters/opencode.md](docs/adapters/opencode.md)) |
| **OpenClaw · Hermes** | 문서 어댑터 |

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

<p align="center">
  <em>사람의 노트는 Obsidian에, 에이전트의 기억은 flint에.</em>
  <br/><br/>
  <strong>F L I N T</strong>
</p>
