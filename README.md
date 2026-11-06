<p align="center">
  <br/>
  ◯ ─────────── ◯
  <br/><br/>
  <img src="assets/flint-logo.svg" width="120" alt="flint logo">
  <br/><br/>
  <strong>F L I N T</strong>
  <br/><br/>
  ◯ ─────────── ◯
  <br/>
</p>

<p align="center">
  <strong>Version-controlled memory for AI agents.</strong>
  <br/>
  <sub>A shared, version-controlled memory store for any AI coding agent — Claude Code, Codex, Gemini, Copilot, Cursor, and more.</sub>
</p>

<p align="center">
  <a href="README.md">English</a> | <a href="README.ko.md">한국어</a>
</p>

<p align="center">
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white" alt="Go 1.25"></a>
  <img src="https://img.shields.io/badge/DoltgreSQL-versioned_data-2f7d6f" alt="DoltgreSQL">
  <img src="https://img.shields.io/badge/MCP-native-8b5cf6" alt="MCP">
  <img src="https://img.shields.io/badge/harnesses-8-10b981" alt="8 harnesses">
  <img src="https://img.shields.io/badge/release-v0.17.0-blue" alt="v0.16.5">
</p>

<p align="center">
  <a href="#quick-start">Quick Start</a> ·
  <a href="#why-flint">Why</a> ·
  <a href="#how-it-works">How It Works</a> ·
  <a href="#commands">Commands</a> ·
  <a href="#agents--adapters">Agents &amp; Adapters</a>
</p>

**flint gives AI coding agents persistent, version-controlled long-term memory — so they get smarter with every session.**

Every AI agent you've used starts each conversation with a blank slate. It re-learns your project structure, re-makes decisions you already settled, re-discovers pitfalls you already documented. It's a brilliant colleague with amnesia.

flint changes that. Each session, the agent records what it learned — decisions, conventions, pitfalls, preferences — into Doltgres, a version-controlled memory store. At the next session start, flint injects the accumulated context back. **The agent compounds knowledge instead of resetting to zero.**

Week 1: it learns your architecture. Week 4: it remembers why you chose gRPC over REST. Week 12: it knows the migration order, the flaky test workarounds, the deployment checklist — because it wrote them down, and now it reads them back.

Humans keep notes in Obsidian. Agents keep memory in flint — the stone you strike to make fire and tools.

> v0.16.5 shipped 2026-11-05 — automatic MCP harness registration (`setup --mcp`), MCP Registry distribution, multi-platform `.mcpb` bundles. Roadmap: [docs/PLAN.md](docs/PLAN.md)

---

## Why flint?

Most AI agents start every session with a blank slate. **Agents using flint start with everything they've ever learned.**

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

# Option A: download a release binary (v0.16.0+)
#   Pushing a tag creates the Release automatically — binaries, checksums,
#   and the plugin zip: https://github.com/synuns/flint/releases

# Option B: build from source
git clone https://github.com/synuns/flint.git flint && cd flint
make install          # builds bin/flint
make build            # production build; version derived from the latest git tag

# 2. One-command setup — Doltgres up + migrate + guides
flint setup           # or ./bin/flint setup from a source build
make seed             # example data, idempotent (hash dedup)
```

### Register with other MCP harnesses (v0.16.5+)

One command detects every installed harness and registers the `flint` MCP server
(Claude Code uses the plugin above; this covers the rest):

```bash
flint setup --mcp
```

Output is one line of JSON — `{"status":"ok","registered":[...],"skipped":[...]}`.
Harnesses that aren't installed are reported in `skipped`; already-registered
ones are skipped harmlessly. Exit code is 0 even when everything is skipped.

### Publish to the MCP Registry (maintainers)

`flint` ships as `.mcpb` bundles (one per platform) attached to every Release.
To publish `mcp-registry/server.json` to the
[MCP Registry](https://github.com/modelcontextprotocol/registry):

1. `brew install mcp-publisher`
2. `mcp-publisher login github`
3. `mcp-publisher publish mcp-registry/server.json` — replace the version
   URL and `fileSha256` placeholders with the actual Release values first
   (`shasum -a 256 dist/flint_<ver>_<os>_<arch>.mcpb`).

### Install the Claude Code plugin (v0.16.0+)

The plugin is published on the marketplace (`.claude-plugin/marketplace.json`).
Prerequisite: the `flint` binary on `PATH` — plugin hooks and the MCP server invoke `flint` from `PATH`.

```text
/plugin marketplace add synuns/flint
/plugin install flint@flint
```

Verify:

```bash
./bin/flint read dump        # search: active_knowledge view only
./bin/flint hot --session x  # session-start injection (default budget 2,000 chars)
./bin/flint audit --limit 5  # audit: last 5 commits from dolt_log
```

<details>
<summary><strong>What happens during setup?</strong></summary>

1. `docker compose up -d` starts Doltgres on host port 5434 (ADR-0010).
2. Schema migrates on first store command (auto, idempotent).
3. Seeding is guided — reruns are safe: identical payloads return `{"status":"duplicate"}` exit 0.
4. Plugin install is guided for `flint-plugin/`.

Default DSN: `postgresql://flint:flint-dev@localhost:5434/flint` — override with
`FLINT_DSN`. Tear down with `./bin/flint teardown` (`--purge` removes the volume too).
</details>

Exposure profile: `flint setup --exposure normal|minimal` tunes how much `hot` injects.
Hooks install as 3 entries: SessionStart (`session-start`), PostCompact (`compact`), Stop (`session-end`).

Full walkthrough: [docs/QUICKSTART.md](docs/QUICKSTART.md)

---

## How It Works

flint is the bridge between **agents**, the **memory store**, and its **git-like history**.

```text
[ Claude Code ] <----> [ flint ] <----> [ Doltgres ]
[ OpenClaw    ]          CLI + MCP        versioned
[ Hermes      ]          /plugin          memory store
[ Codex · Gemini · Copilot · OpenCode · Cursor ]
        MCP + instruction-file adapters (v0.9)
```

<p align="center">
  <img src="assets/flint-architecture-en.svg" width="640" alt="flint architecture: agents, flint CLI+MCP, Doltgres memory store">
</p>

| Phase | Action |
| :--- | :--- |
| **Inject** | Session start: a hook runs `flint hot` and streams a ≤2,000-char `[flint]` context block |
| **Work** | Agents read and write through the CLI or MCP |
| **Record** | `write` / `done` append JSONL with hash dedup — reruns stay idempotent |
| **Verify** | `audit` reads `dolt_log`; `restore --drill` rehearses recovery without touching data |

The four phases form one session lifecycle:

<p align="center">
  <img src="assets/flint-lifecycle-en.svg" width="640" alt="flint session lifecycle: inject, work, record, verify — memory compounds">
</p>

---

## Commands

| Command | Role | Description |
| :--- | :--- | :--- |
| `flint setup` | **Bootstrap** | Doltgres up (5434) + migrate + next-step guide |
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

`flint read` retries word-by-word (fallback) when the phrase query hits nothing.
`flint read` prints nothing and exits 1 when even the fallback hits nothing (silence contract) — near-miss candidates are measured internally as the `zero_near` metric, never shown to the agent (v0.14.0). The former did-you-mean suggestion output was removed by this contract.
exit 1 with no output = no knowledge (normal silence; former did-you-mean suggestions removed by this contract); exit 1 with an error JSON line = actual failure (storage error via MapExit).
`flint read --status pending` lists knowledge items awaiting approval.
`flint read` breaks ranking ties by adoption (`read_count` DESC) — popularity only as the final tiebreaker (v0.13.0).
`flint maintain --conflict-suspects` lists suspected contradicting knowledge (same topic, diverging summaries) (v0.13.0).
Backup dumps are integrity-checked via a sha256 sidecar — including `flint done` auto-backups (v0.10.0).

Exit codes (v0.15.0 — de-facto behavior made explicit):

| exit | Meaning |
| :--- | :--- |
| `exit 0` | success |
| `exit 1` | command or validation error — one error JSON line (a silent no-knowledge `read` also exits 1 with no output: silence contract above) |
| `exit 2` | storage failure (`MapExit`) |
| `exit 3` | invalid `read` arguments — valueless trailing flags, invalid `--status` / `--as-of` values |

Also: `version`, `teardown`. Operations runbook: [docs/RUNBOOK.md](docs/RUNBOOK.md) · tuning: [docs/TUNING.md](docs/TUNING.md)

---

## Agents & Adapters

| Surface | How | Where |
| :--- | :--- | :--- |
| **Claude Code** | `/flint` command + SessionStart/Stop hooks (`hot` in, `done` out) | [flint-plugin/](flint-plugin/) |
| **MCP clients** | `flint mcp` server | `lib/mcp` |
| **OpenClaw** | Adapter | [docs/adapters/openclaw.md](docs/adapters/openclaw.md) |
| **Hermes** | Adapter | [docs/adapters/hermes.md](docs/adapters/hermes.md) |
| **Codex CLI** | MCP + AGENTS.md adapter | [docs/adapters/codex.md](docs/adapters/codex.md) |
| **Gemini CLI** | MCP + GEMINI.md adapter | [docs/adapters/gemini.md](docs/adapters/gemini.md) |
| **Copilot CLI** | MCP + instructions.md adapter | [docs/adapters/copilot.md](docs/adapters/copilot.md) |
| **OpenCode** | MCP + AGENTS.md adapter | [docs/adapters/opencode.md](docs/adapters/opencode.md) |
| **Cursor** | MCP + AGENTS.md adapter | [docs/adapters/cursor.md](docs/adapters/cursor.md) |

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
└── docker-compose.yml  # Doltgres on host 5434
```

---

<p align="center">
  <em>"Human notes in Obsidian. Agent memory in flint."</em>
  <br/><br/>
  <strong>F L I N T</strong>
  <br/><br/>
  <code>v0.9.7 · DoltgreSQL · Go</code>
</p>
