<p align="center">
  <br/>
  ◯ ─────────── ◯
  <br/><br/>
  <img src="https://upload.wikimedia.org/wikipedia/commons/6/67/Flint.jpg" width="120" alt="Flint">
  <br/><br/>
  <strong>F L I N T</strong>
  <br/><br/>
  ◯ ─────────── ◯
  <br/>
</p>

<p align="center">
  <strong>Version-controlled memory for AI agents.</strong>
  <br/>
  <sub>A shared, Doltgres-backed second brain for Claude Code, OpenClaw, and Hermes.</sub>
</p>

<p align="center">
  <a href="README.md">English</a> | <a href="README.ko.md">한국어</a>
</p>

<p align="center">
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white" alt="Go 1.25"></a>
  <img src="https://img.shields.io/badge/DoltgreSQL-versioned_data-2f7d6f" alt="DoltgreSQL">
  <img src="https://img.shields.io/badge/MCP-native-8b5cf6" alt="MCP">
  <img src="https://img.shields.io/badge/release-v0.5-blue" alt="v0.5">
</p>

<p align="center">
  <a href="#quick-start">Quick Start</a> ·
  <a href="#why-flint">Why</a> ·
  <a href="#how-it-works">How It Works</a> ·
  <a href="#commands">Commands</a> ·
  <a href="#agents--adapters">Agents &amp; Adapters</a>
</p>

**flint gives AI coding agents persistent, version-controlled long-term memory.**

Agents are capable but forget everything between sessions. flint stores project decisions, session notes, and preferences in Doltgres — a Postgres-compatible database where every cell change is a commit — then injects the right context back at session start. Every write is auditable, every memory is recoverable.

Humans keep notes in Obsidian. Agents keep memory in flint — the stone you strike to make fire and tools.

> v0.5 shipped 2026-10-22 — reliability & search quality. Roadmap: [docs/PLAN.md](docs/PLAN.md)

---

## Why flint?

Most AI agents start every session with a blank slate.

| Problem | What Happens | flint Fix |
| :--- | :--- | :--- |
| **Amnesia** | The agent forgets last week's decisions | `flint hot` injects budgeted context at session start |
| **Lost Context** | You paste the same background over and over | `flint read` searches the active-knowledge view |
| **Invisible Edits** | Memory mutates with no trace | Doltgres cell-level history + `flint audit` |
| **Agent Silos** | Each agent forgets the others' work | One shared store via CLI, MCP, and plugin adapters |
| **Fragile Recovery** | Corruption or bad writes end in data loss | `flint restore --drill` rehearses recovery, backups rotate |

---

## Quick Start

```bash
# 1. Install — single Go binary, pgx/v5 only
git clone https://github.com/synuns/flint.git flint && cd flint
make install          # builds bin/flint

# 2. One-command setup — Doltgres up + migrate + guides
./bin/flint setup
make seed             # example data, idempotent (hash dedup)
```

Verify:

```bash
./bin/flint read dump        # search: active_knowledge view only
./bin/flint hot --session x  # session-start injection (default budget 2,000 chars)
./bin/flint audit --limit 5  # audit: last 5 commits from dolt_log
```

<details>
<summary><strong>What happens during setup?</strong></summary>

1. `docker compose up -d` starts Doltgres on host port 5433 (ADR-0001).
2. Schema migrates on first store command (auto, idempotent).
3. Seeding is guided — reruns are safe: identical payloads return `{"status":"duplicate"}` exit 0.
4. Plugin install is guided for `flint-plugin/`.

Default DSN: `postgresql://flint:flint-dev@localhost:5433/flint` — override with
`FLINT_DSN`. Tear down with `./bin/flint teardown` (`--purge` removes the volume too).
</details>

---

## How It Works

flint is the bridge between **agents**, the **memory store**, and its **git-like history**.

```text
[ Claude Code ] <----> [ flint ] <----> [ Doltgres ]
[ OpenClaw    ]          CLI + MCP        versioned
[ Hermes      ]          /plugin          memory store
```

| Phase | Action |
| :--- | :--- |
| **Inject** | Session start: a hook runs `flint hot` and streams a ≤2,000-char `[flint]` context block |
| **Work** | Agents read and write through the CLI or MCP |
| **Record** | `write` / `done` append JSONL with hash dedup — reruns stay idempotent |
| **Verify** | `audit` reads `dolt_log`; `restore --drill` rehearses recovery without touching data |

---

## Commands

| Command | Role | Description |
| :--- | :--- | :--- |
| `flint setup` | **Bootstrap** | Doltgres up (5433) + migrate + next-step guide |
| `flint write <kind>` | **Record** | Append JSONL from stdin; idempotent via hash dedup |
| `flint read <query>` | **Retrieve** | Search the active-knowledge view |
| `flint hot --session <id>` | **Inject** | Budgeted session-start context |
| `flint done --session <id>` | **Close** | Session wrap-up record |
| `flint approve --id n` | **Curate** | Promote pending knowledge to active |
| `flint maintain` | **Curate** | Curation session: `--check` thresholds, `--invalidate`, `--merge` |
| `flint audit --limit n` | **Verify** | Last n commits from `dolt_log` |
| `flint backup [--out f]` | **Backup** | Self-contained SQL dump (ADR-0002 format) |
| `flint restore --file f [--drill]` | **Recover** | Restore; `--drill` rehearses, source untouched |
| `flint status` | **Inspect** | Schema version, pending writes, hot p95 |
| `flint mcp` | **Serve** | Run the MCP server |

Also: `version`, `teardown`. Operations runbook: [docs/RUNBOOK.md](docs/RUNBOOK.md) · tuning: [docs/TUNING.md](docs/TUNING.md)

---

## Agents & Adapters

| Surface | How | Where |
| :--- | :--- | :--- |
| **Claude Code** | `/flint` command + SessionStart/Stop hooks (`hot` in, `done` out) | [flint-plugin/](flint-plugin/) |
| **MCP clients** | `flint mcp` server | `lib/mcp` |
| **OpenClaw** | Adapter | [docs/adapters/openclaw.md](docs/adapters/openclaw.md) |
| **Hermes** | Adapter | [docs/adapters/hermes.md](docs/adapters/hermes.md) |

---

## Repository Structure

```
flint/
├── bin/flint           # single Go binary
├── cmd/flint/          # main entry point
├── lib/                # store · query · db · cli · backup · sqlfs · mcp
├── flint-plugin/       # Claude Code plugin (commands · skills · hooks)
├── docs/               # PLAN · QUICKSTART · RUNBOOK · TUNING · ADR · adapters
├── data/               # seed.jsonl (tracked) + runtime data (ignored)
├── scripts/            # CI validation
└── docker-compose.yml  # Doltgres on host 5433
```

---

<p align="center">
  <em>"Human notes in Obsidian. Agent memory in flint."</em>
  <br/><br/>
  <strong>F L I N T</strong>
  <br/><br/>
  <code>v0.5 · DoltgreSQL · Go</code>
</p>
