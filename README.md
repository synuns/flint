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
  <a href="https://github.com/synuns/flint/actions/workflows/ci.yml"><img src="https://github.com/synuns/flint/actions/workflows/ci.yml/badge.svg" alt="CI status"></a>
  <img src="https://img.shields.io/badge/release-v1.19.6-blue" alt="v1.19.6">
</p>
<p align="center">
  <sub>8 counts the auto-registration (tier A) harnesses — the full support list is in the <a href="#agents--adapters">adapter table</a> below.</sub>
</p>

<p align="center">
  <a href="#quick-start">Quick Start</a> ·
  <a href="#how-it-works">How It Works</a> ·
  <a href="#why-its-safe">Safety</a> ·
  <a href="#commands">Commands</a> ·
  <a href="#agents--adapters">Agents</a>
</p>

Every AI coding agent you've used forgets everything between sessions. It relearns your architecture, rehashes decisions you already settled, rediscovers pitfalls you documented months ago. A brilliant colleague with amnesia.

flint fixes this. When your agent learns something — a decision, a convention, a gotcha — flint stores it in Doltgres, a database where every change is a commit. Next time a session starts, flint injects that accumulated context back in. The agent picks up where it left off instead of starting from scratch.

It adds up. Week one, it learns the stack. By week four, it remembers why you chose gRPC over REST. Three months in, it knows the migration order, which tests are flaky, and the deployment checklist — because it wrote all of that down.

Humans take notes in Obsidian. Agents take notes in flint — the stone you strike to make fire and tools.

> v1.19.6 · observability-foundation patches — schema v13 (near_score · threshold snapshots · weekly rollups) · metering freshness & coverage · [full changelog](docs/HISTORY.md)
> v1.18.3 · R69 — grammar fix · [full changelog](docs/HISTORY.md)
> v1.17.2 · r65-dedup — correction hash origin exclusion · [full changelog](docs/HISTORY.md)
> v1.17.1 · r64-observability — origin tracking fix · [full changelog](docs/HISTORY.md)
> v1.17.0 · echo — write-time contradiction detection · observability keys · [full changelog](docs/HISTORY.md)
> v1.16.2 · R62 — highlight order contract tests (latest-first + EN/KO parity) · [full changelog](docs/HISTORY.md)
> v0.43.0 · auto-update · go install · end-to-end release pipeline · [full changelog](docs/HISTORY.md)
> v0.44.0 · PLAN rearm · surface contracts · command-table guard · [full changelog](docs/HISTORY.md)
> v1.7.2 · telemetry attribution · sync mitigation · probe heal (R38) · [full changelog](docs/HISTORY.md)
> v1.9.1 · R42 hardening — multi-device portability (jsonl recall replay) · recall event lifecycle · [full changelog](docs/HISTORY.md)
> v1.9.2 · R43 recovery — import recall-migration resilience · fresh-DB schema direct path · doc accuracy · [full changelog](docs/HISTORY.md)
> v1.9.3 · TUI & perf — low-height TUI chrome minimization · parallel report collection · opt-in tuning TUI · [full changelog](docs/HISTORY.md)
> v1.10.0 · Hygiene — comments in English across 424 files · slow suite 34.5% faster · test race fixes · [full changelog](docs/HISTORY.md)
> v1.10.1 · R47 — file-count corrections in v1.10.0 notes · [full changelog](docs/HISTORY.md)
> v1.10.2 · R48 — notes hygiene (dup heading · order) · [full changelog](docs/HISTORY.md)
> v1.11.0 · knowledge pipeline step 1 — user-correction channel (`--source correction`) · [full changelog](docs/HISTORY.md)
> v1.11.1 · R50 — dup_correction metering fix · [full changelog](docs/HISTORY.md)
> v1.11.2 · r52-reporting — dup_correction report exposure · [full changelog](docs/HISTORY.md)
> v1.12.0 · knowledge pipeline step 2 — recall-failure gap candidates · [full changelog](docs/HISTORY.md)
> v1.12.1 · r54-docs — RUNBOOK pipeline step 2 notes · NFKC truncation fix · [full changelog](docs/HISTORY.md)
> v1.13.0 · knowledge lifecycle — staleness scoring · volatility tiers · [full changelog](docs/HISTORY.md)
> v1.14.0 · knowledge pipeline step 3 — auto-detected corrections (A-plan hook) · survived decay relief · [full changelog](docs/HISTORY.md)
---

## Quick Start

```bash
# Install the prebuilt binary (sha256-verified) to ~/.local/bin
curl -fsSL https://raw.githubusercontent.com/synuns/flint/main/scripts/install.sh | bash

# Start Doltgres, migrate the schema
flint setup

# Record your first memory
echo "The API gateway retries idempotent POSTs twice" | flint write knowledge
```

Optional, from a checkout: load the synthetic demo dataset — `make seed FLINT_SEED_DSN=postgresql://flint:flint-dev@localhost:5434/flint`.

> Every release artifact ships with SLSA build provenance — verify a downloaded file with `gh attestation verify <file> --repo synuns/flint`.

### Installing the binary

Four paths — pick one. `flint setup` (Doltgres on port 5434) is the common next step for all of them.

**1 · One-line installer** — prebuilt binary, checksum-verified:

```bash
curl -fsSL https://raw.githubusercontent.com/synuns/flint/main/scripts/install.sh | bash
```

Detects your platform (darwin/linux on arm64/amd64), downloads the matching release archive plus the release `checksums.txt`, verifies the sha256 of that one asset, and installs to `~/.local/bin` — override with `--bin-dir DIR`. Needs only `curl`, `tar`, and `shasum` or `sha256sum`; no Go toolchain. If the target directory is not on your `PATH`, the script prints the exact line to add.

**2 · go install**:

```sh
go install github.com/synuns/flint/cmd/flint@latest
```

Module path `github.com/synuns/flint` (v0.22.0+) — version comes from build info; `flint update` covers binary refresh afterwards.

**3 · Manual download + checksum verification** — the [releases page](https://github.com/synuns/flint/releases) attaches binaries, checksums, and the plugin zip automatically:

```bash
ver=$(curl -fsSL https://api.github.com/repos/synuns/flint/releases/latest \
	| sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
asset="flint_${ver#v}_darwin_arm64.tar.gz"          # adjust: darwin|linux _ arm64|amd64
base="https://github.com/synuns/flint/releases/download/$ver"
curl -fsSLO "$base/checksums.txt" "$base/$asset"
line=$(grep " $asset\$" checksums.txt) && [ -n "$line" ] && echo "$line" | shasum -a 256 -c -  # no shasum? pipe to: sha256sum -c -
tar -xzf "$asset" && install flint ~/.local/bin
```

**4 · Homebrew (cask — tap in preparation)**: flint ships prebuilt binaries only, so the Homebrew path is a **cask, not a formula** — goreleaser deprecated formula-based distribution for binary releases (2026), and a cask installs the exact release artifact with sha256 verification, the same trust chain as the manual path. Once the tap is live the command will be `brew install --cask synuns/tap/flint`; preparing it means a `homebrew-tap` repository with one Cask per release (`url` = the release asset, `sha256` from `checksums.txt`). Until then, use path 1 or 2.

### Claude Code

```text
/plugin marketplace add synuns/flint
/plugin install flint@flint
```

The plugin needs the `flint` binary on your `PATH`. Hooks and the MCP server both call it.

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

In one picture:

```
session start ──hook──► flint hot ──budgeted [flint] block──► agent context
     │
     ├── flint write ──hash dedup──► Doltgres (every change is a commit)
     │                                    │
     └── flint read / MCP tools ◄── RRF search over the active view
                                          ▼
                            audit · backup · restore · report
```

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

## Why it's safe

flint sits inside your coding sessions, so its constraints are part of the product, not fine print ([ADR-0013 threat model](docs/adr/ADR-0013-threat-model.md)):

- **Local and offline.** flint, your harness, and Doltgres all run on your machine — no cloud service, no account, no telemetry. The maintenance paths (`doctor`, `report`, `maintain`) are offline CLIs.
- **Non-interference — five principles.** (1) Minimal injection, full search: flint never speaks first. (2) Hooks are non-blocking with timeouts — session end is never blocked. (3) A flint failure is a silent skip (exit 2) — your session is never delayed. (4) Warnings fire only on threshold breach. (5) flint data is data: the `[flint]` block is a data boundary that can never become an instruction.
- **You own the data.** Everything lives in your Doltgres and `~/.flint` — export any time (`flint export`, PAM with optional Ed25519 signature), backups with `age` encryption, secrets scanned on write and backup, remote live writes disabled by design ([ADR-0015](docs/adr/0015-bridge-surface.md)).

---

## Commands

| Command | What it does |
| :--- | :--- |
| `flint setup` | Start Doltgres, migrate, print seed/plugin next steps |
| `flint update` | Self-update to the latest release (checksum-verified, `--check` dry-run) |
| `flint tuning` | Inspect/override threshold keys (`list`/`get`/`set`/`reset`; opt-in `--tui` form) |
| `flint write <kind>` | Record knowledge from stdin (idempotent) |
| `flint import [path]` | Convert rule files (CLAUDE.md etc.) to pending knowledge (`--commit`); `--format jsonl\|pam` migrates knowledge back in (round-trip re-import · PAM v1.0 intake) |
| `flint export [--format f]` | Export knowledge as jsonl·md·txt·csv·pam (PAM v1.0 portable memory-store; `--status`·`--tag`·`--since` filters, `--out` file with `.sha256` sidecar; PAM `--sign` Ed25519 signature, spec §18) |
| `flint read <query>` | Search active knowledge |
| `flint hot --session <id>` | Inject context at session start |
| `flint brief [query]` | Masked, read-only brief to paste into any external AI (the bridge) |
| `flint done --session <id>` | Wrap up a session |
| `flint approve --id n` | Promote pending to active |
| `flint maintain` | Curate: check thresholds, invalidate, merge (`--tui` interactive review, opt-in) |
| `flint audit --limit n` | Show last n commits |
| `flint backup` | Dump to SQL with sha256 checksum (`--encrypt` age at-rest, `--mask` redacted diagnostics) |
| `flint restore --file f` | Restore from backup (`--drill` to rehearse) |
| `flint sync` | Sync knowledge with a Doltgres remote — `remote add/list/remove` (url masked), `push`/`pull [--dry]`; a merge conflict exits 1 with manual-resolution guidance; remote urls are never logged (human operator command) |
| `flint profile` | Profile verdict: evidence + recommendation, `--apply normal\|minimal` |
| `flint status` | Schema version, pending count, hot p95 |
| `flint mcp` | MCP server (read·write·hot·pending·approve tools) |
| `flint pending` | Pending review queue with verdicts (promote·merge·expire·hold, `--json`) |
| `flint compact` | Record a compact summary as pending knowledge (PostCompact hook entry) |
| `flint nudge` | Session-end distillation reminder for markers left by compact (`--clear` closes) |
| `flint doctor --check c` | Health checks (16 kinds, subset via `--check`, fail → exit 1) |
| `flint report --days n` | Daily intake·zero_pct·p95·stale report (`--html` static dashboard) |
| `flint diff <from> <to>` | Compare knowledge between two as-of points |
| `flint teardown` | Undo setup (hooks/config; `--purge` also removes data) |
| `flint version` | Print version |

**Exit codes:** 0 success (zero-hit `read` also exits 0 — silent in text mode, `{"hits":0}` with `--json`) · 1 error · 2 storage failure · exit 3 — invalid input (valueless trailing flags, bad `--status` / `--as-of` values, schema violations on `write`).

`flint read` uses word-by-word fallback search when the phrase query returns nothing.

Memory that decays like memory: every `read` hit reinforces the entry (`recall_score`), unused entries decay daily (floor 0), and `hot`/`brief` rank well-recalled knowledge first — `flint maintain --decay-audit` lists archive candidates, and archiving always requires your approval.

### Interactive TUI (v1.5.0)

Four screens built on bubbletea: `flint setup` (no arguments) opens the setup wizard, and `pending`, `export`, and `report` become interactive review, preview, and diagnostics views. Interactive by default in a terminal — a bare invocation opens the TUI (since v1.5.2). Piped/non-terminal runs and any flag keep the existing output untouched — same JSON on stdout. `flint maintain --tui` (v1.6.0) adds an opt-in curation screen: suspect lists → a what-survives merge preview → merge/keep/invalidate, delegating to the existing subcommands.

Operations: [docs/RUNBOOK.md](docs/RUNBOOK.md) · Tuning: [docs/TUNING.md](docs/TUNING.md) · Metrics: [docs/METRICS.md](docs/METRICS.md) · Performance: [docs/PERF.md](docs/PERF.md) · Docs index: [docs/README.md](docs/README.md)

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

## License

flint is open-source software released under the [MIT License](LICENSE).

---

<p align="center">
  <em>Human notes in Obsidian. Agent memory in flint.</em>
  <br/><br/>
  <strong>F L I N T</strong>
</p>
