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
  <strong>Long-term memory for AI coding agents.</strong>
  <br/>
  <sub>Your agents get smarter with every session — Claude Code, Codex, Gemini, Copilot, Cursor, and more.</sub>
</p>

<p align="center">
  <a href="README.md">English</a> | <a href="README.ko.md">한국어</a>
</p>

<p align="center">
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26"></a>
  <img src="https://img.shields.io/badge/DoltgreSQL-versioned_data-2f7d6f" alt="DoltgreSQL">
  <img src="https://img.shields.io/badge/MCP-native-8b5cf6" alt="MCP">
  <img src="https://img.shields.io/badge/harnesses-8-10b981" alt="8 harnesses">
  <img src="https://img.shields.io/badge/release-v0.42.0-blue" alt="v0.40.0">
  <img src="https://img.shields.io/badge/release-v0.42.0-blue" alt="v0.41.1">
  <img src="https://img.shields.io/badge/release-v0.42.0-blue" alt="v0.42.0"></p>

<p align="center">
  <a href="#quick-start">Quick Start</a> ·
  <a href="#how-it-works">How It Works</a> ·
  <a href="#commands">Commands</a> ·
  <a href="#agents--adapters">Agents</a>
</p>

Every AI coding agent you've used forgets everything between sessions. It relearns your architecture, rehashes decisions you already settled, rediscovers pitfalls you documented months ago. A brilliant colleague with amnesia.

flint fixes this. When your agent learns something — a decision, a convention, a gotcha — flint stores it in Doltgres, a database where every change is a commit. Next time a session starts, flint injects that accumulated context back in. The agent picks up where it left off instead of starting from scratch.

It adds up. Week one, it learns the stack. By week four, it remembers why you chose gRPC over REST. Three months in, it knows the migration order, which tests are flaky, and the deployment checklist — because it wrote all of that down.

Humans take notes in Obsidian. Agents take notes in flint — the stone you strike to make fire and tools.

> v0.40.0 · auto-update · go install · end-to-end release pipeline · [full changelog](docs/PLAN.md)
> v0.41.1 · auto-update · go install · end-to-end release pipeline · [full changelog](docs/PLAN.md)
> v0.42.0 · auto-update · go install · end-to-end release pipeline · [full changelog](docs/PLAN.md)
---

## Quick Start

```bash
# Install (builds flint into GOPATH/bin)
git clone https://github.com/synuns/flint.git && cd flint
make install

# Start Doltgres, migrate the schema
flint setup

# Load demo data (seed target must be explicit)
make seed FLINT_SEED_DSN=postgresql://flint:flint-dev@localhost:5434/flint
```

Or [download a release binary](https://github.com/synuns/flint/releases) — binaries, checksums, and the plugin zip are attached automatically.

### Claude Code

```text
/plugin marketplace add synuns/flint
/plugin install flint@flint
```

The plugin needs the `flint` binary on your `PATH`. Hooks and the MCP server both call it.

### Install via go install

```sh
go install github.com/synuns/flint/cmd/flint@latest
```

Module path `github.com/synuns/flint` (v0.22.0+) — version comes from build info; `flint update` covers binary refresh afterwards.

### Other agents (Codex, Gemini, Cursor, OpenCode)

```bash
flint setup --mcp
```

Detects installed harnesses (Codex, Gemini, OpenCode, Copilot, Cursor, Qwen, Devin, Cline) and registers the MCP server via each native CLI. File-based harnesses (Windsurf, Zed, Amp, VS Code, Continue, Goose, Trae) get a doctor check + adapter docs. Anything not installed gets skipped. Exit 0 regardless.

<details>
<summary>What <code>flint setup</code> does</summary>

Runs `docker compose up -d` to start Doltgres on port 5434, migrates the schema, and prints next steps. Default DSN is `postgresql://flint:flint-dev@localhost:5434/flint` — override with `FLINT_DSN`. Tear down with `flint teardown`.

</details>

<details>
<summary>Configuring exposure</summary>

`flint setup --exposure normal|minimal` controls how much context `hot` injects at session start. `minimal` keeps it to a single headline. Three hooks: SessionStart (`session-start`), PostCompact (`compact`), Stop (`session-end`).

</details>

Full walkthrough: [docs/QUICKSTART.md](docs/QUICKSTART.md)

---

## How It Works

<p align="center">
  <img src="assets/flint-architecture-en.svg" width="880" alt="flint architecture diagram">
</p>

Each session cycles through four phases:

| Phase | What happens |
| :--- | :--- |
| **Inject** | Hook fires `flint hot` at session start — streams a budgeted `[flint]` context block |
| **Work** | Agent reads and writes via CLI or MCP |
| **Record** | `flint write` appends JSONL with hash dedup |
| **Verify** | `flint audit` inspects `dolt_log`; `flint restore --drill` rehearses recovery |

<p align="center">
  <img src="assets/flint-lifecycle-en.svg" width="880" alt="flint session lifecycle: memory compounds">
</p>

---

## Beyond local agents — the bridge

flint's surfaces (hooks, stdio MCP, localhost Doltgres) assume the agent runs on your machine — that's deliberate ([ADR-0013](docs/adr/0013-threat-model.md)). For AIs that don't — a cloud assistant, a phone app — use the bridge instead of a connector:

```bash
flint brief [query]        # masked, read-only context block — paste it into any chat
```

`brief` searches active knowledge only (pending never leaves the machine), masks secret patterns (`[REDACTED:…]`), and carries the `[flint]` data-block marker so the receiving AI treats it as data, not instructions ([ADR-0015](docs/adr/0015-bridge-surface.md)). To bring an assistant's answer back, feed it to `flint write knowledge` with a confidence below `conf_floor` (low-confidence writes are classified pending), or use `flint import` (always pending) — it lands in the same review queue as any unreviewed writer, and `flint approve` promotes it. Remote live writes are off the table by design.

---

## Commands

| Command | What it does |
| :--- | :--- |
| `flint setup` | Start Doltgres, migrate, print seed/plugin next steps |
| `flint update` | Self-update to the latest release (checksum-verified, `--check` dry-run) |
| `flint tuning` | Inspect/override threshold keys (`list`/`get`/`set`/`reset`) |
| `flint write <kind>` | Record knowledge from stdin (idempotent) |
| `flint import [path]` | Convert rule files (CLAUDE.md etc.) to pending knowledge (`--commit`) |
| `flint read <query>` | Search active knowledge |
| `flint hot --session <id>` | Inject context at session start |
| `flint brief [query]` | Masked, read-only brief to paste into any external AI (the bridge) |
| `flint done --session <id>` | Wrap up a session |
| `flint approve --id n` | Promote pending to active |
| `flint maintain` | Curate: check thresholds, invalidate, merge |
| `flint audit --limit n` | Show last n commits |
| `flint backup` | Dump to SQL with sha256 checksum (`--encrypt` age at-rest, `--mask` redacted diagnostics) |
| `flint restore --file f` | Restore from backup (`--drill` to rehearse) |
| `flint profile` | Profile verdict: evidence + recommendation, `--apply normal\|minimal` |
| `flint status` | Schema version, pending count, hot p95 |
| `flint mcp` | MCP server (read·write·hot·pending·approve tools) |

**Exit codes:** 0 success · 1 error (or silent no-results on `read`) · 2 storage failure · exit 3 — invalid input (valueless trailing flags, bad `--status` / `--as-of` values, schema violations on `write`).

`flint read` uses word-by-word fallback search when the phrase query returns nothing.

Operations: [docs/RUNBOOK.md](docs/RUNBOOK.md) · Tuning: [docs/TUNING.md](docs/TUNING.md) · Metrics: [docs/METRICS.md](docs/METRICS.md)

---

## Agents & Adapters

Every adapter below runs locally — MCP support means MCP clients that can spawn the `flint` binary. AIs without a local runtime use the [bridge](#beyond-local-agents--the-bridge) instead.

| Agent | Method |
| :--- | :--- |
| **Claude Code** | Plugin: hooks + `/flint` command |
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
| **OpenClaw** | Adapter ([docs/adapters/openclaw.md](docs/adapters/openclaw.md)) |
| **Hermes** | Adapter ([docs/adapters/hermes.md](docs/adapters/hermes.md)) |

Adapter docs: [docs/adapters/](docs/adapters/)

---

```
flint/
├── cmd/flint/          # entry point
├── lib/                # store · query · db · cli · backup · mcp
├── flint-plugin/       # Claude Code plugin
├── docs/               # PLAN · RUNBOOK · TUNING · ADR · adapters
└── docker-compose.yml  # Doltgres on 5434
```

---

<p align="center">
  <em>Human notes in Obsidian. Agent memory in flint.</em>
  <br/><br/>
  <strong>F L I N T</strong>
</p>
