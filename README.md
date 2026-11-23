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
  <img src="https://img.shields.io/badge/release-v1.28.1-blue" alt="v1.28.1">
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

> v1.28.1 · R119 patch — T() gains an en fallback for missing ko keys — new keys no longer leak raw key strings (≤24h cache note from R119-2 declared accepted, self-resolving) · [full changelog](docs/HISTORY.md)
> v1.28.0 · surface cleanup — `flint read` / `flint tuning` with no arguments on a TTY open an interactive TUI (flagged·non-TTY·FLINT_NO_TUI invocations keep the flag mode unchanged; read instrumentation preserved) · i18n KO catalog (`FLINT_LANG`, usage surface first) · test consolidation (8 domains, 56 version-suffixed files into 8, suite count unchanged at 592) · BRAND sweep · BREW tap review · signature re-verification · [full changelog](docs/HISTORY.md)
> v1.29.2 · r117 patch — isolation polish: the doctor `engine-footprint` skip detail now names the cause (an unstarted dedicated engine — "no running container publishes 5499" — is distinguishable from docker being absent) · dedicated-engine DSN container selection pinned by test in both directions · [full changelog](docs/HISTORY.md)
> v1.29.1 · r116 patch — isolation audit fixes: the Makefile `FLINT_TEST_DSN` default now points at the dedicated engine (5499 — the ops-port default defeated local isolation) · docker-compose.test.yml pins the CI image digest (with an equivalence guard) · engine container selection is port-based (`EngineMemMB`·`restarts` never read the wrong engine) · `FLINT_REALDB_REQUIRED=1` is honored in tests/ helpers (skip-0 sentinel on every path) · [full changelog](docs/HISTORY.md)
> v1.29.0 · physical isolation — dedicated test engine (`docker-compose.test.yml` on 127.0.0.1:5499 · `internal/testengine` lazy ensure + scratch lifecycle + run-end purge · the ops engine is never touched by tests) · `maintain --purge-dropped` (dry-run list of restorable dropped databases → irreversible purge, loopback-only) · doctor `dropped_dbs` / `stray_dbs` warnings (thresholds `dropped_dbs_warn`·`stray_dbs_warn`, meta `stray_dbs_allow` exemption) · engine-footprint shows container restart count · [full changelog](docs/HISTORY.md)
> v1.26.3 · r-round patch — comment gate: `internal/commentlint` (any non-directive Go comment fails `go test ./...`), `scripts/csweep.go` (the AST sweep tool, in-repo), `scripts/check_comments.py` in `make ci` (git-tracked `*.sh`·`*.py`, quote/heredoc-aware), 227 script comments removed · [full changelog](docs/HISTORY.md)
> v1.26.2 · r113 patch — comment sweep: 556 files · 23,960 narrative comments removed via a go/parser AST pass (string literals untouched; `//go:*`·`//nolint`·`// +build` directives kept) · README highlight history restored (a bump-script bug had relabeled all 37 entries) · bump script no longer relabels highlight lines (badges only) · release-gate asserts highlight labels unique · [full changelog](docs/HISTORY.md)
> v1.26.1 · r111-r112 patch — explicit check refreshes the update cache (`flint update --check` / doctor `--check update` writes latest · ETag · checked_at — the doctor Fix line now does what it says) · keyword-form DSN derivation refused (exit 3 + FLINT_DEMO_DSN guidance instead of a corrupted `_demo` DSN) · FLINT_DEMO_DSN skips the base-server ensure · offline registry fallback (cached copy + age, still not a warn) · 42P04 duplicate_database tolerated (parallel first-run seeds) · [full changelog](docs/HISTORY.md)
> v1.26.0 · surface trust — update cache three principles (ETag conditional re-fetch: a 304 keeps the cached latest · contradiction detector: a cache older than the running binary re-fetches immediately · `checked_at` surfaced in `report --text`) · `flint seed --demo` (embedded synthetic dataset into an isolated `_demo` database · `--reset --yes` deletes only demo rows) · doctor `versions` (self · cached latest · PATH · mcp-registry side by side) · threshold `update_cache_age_h` · [full changelog](docs/HISTORY.md)
> v1.25.1 · r108-r109 patch — gc-pending JSON closing fix (the `maintain --gc` pre-run notice line now parses as JSON) · dialect probe relocation (the `dolt_log_depth` SQL is lib/db-owned — `db.DoltLogDepth`; doctor calls it) · depth probe numeric fix (`commit_order::int8` + `CellToInt` pgtype.Numeric — the depth read a silent 0 on the real engine) · `db_size` documented as a standing skip on Doltgres · engine-footprint timeout 2s→5s · bare `--shallow` wording · [full changelog](docs/HISTORY.md)
> v1.25.0 · engine ops — `maintain --gc --yes` (dolt garbage collection: disconnect notice → reconnect loop → schema recheck → meta history) · doctor `dolt_log_depth` (O(1)) / `db_size` warnings + `engine-footprint` display · RUNBOOK §10/§11 · thresholds `dolt_log_depth_warn`·`db_size_warn_mb` · ANALYZE retired (no-op evidenced) · audit_log replay spike succeeded (pure PG) · [full changelog](docs/HISTORY.md)
> v1.24.2 · r106 patch — race-recheck echo coverage (the race-recheck duplicate's echo accrual now has a fake-store regression) · quiet-failure regression (an `echo_candidates` INSERT failure never fails the write) · echo scan comment (pending rows documented as pre-existing candidates) · [full changelog](docs/HISTORY.md)
> v1.24.1 · r104-r105 patch — `echo_candidates` accrual wired (write-time echo candidates persist: fresh row id, duplicate resend keyed by the existing row) · merge-audit active-only fetch (pending rows excluded) · `--merge` joins the D4 disposed counter · int4 input guards (`--recall-audit --limit`, `read --related`) · [full changelog](docs/HISTORY.md)
> v1.24.0 · curation residual — schema v19 (`kind`·`source_path`, `echo_candidates`) · `maintain --merge-audit` (duplicate pair report, cost-estimated) · `--recall-audit` (bad-recall feedback report) · `read --related` (undirected neighbor links) / `--why` (token tier · RRF signal · zero-hit evidence) · [full changelog](docs/HISTORY.md)
> v1.23.2 · r102 patch — counter digit bound (18-digit guard on the shadow counter upsert) · v1.23.0 spec alignment (boundary fires, off-mode cost) · what-if p50/p90 domain filter · [full changelog](docs/HISTORY.md)
> v1.23.0 · autotune shadow — schema v18 `tuning_predictions` (predictions recorded, nothing applied) · `auto_mode` policy switch (default off, whitelist: `suggest_min_score`) · shadow rounds on the read tail (24h gate, streak rules) · `flint tuning what-if` / `status` (reclassification · p50/p90 · drift card) · [full changelog](docs/HISTORY.md)
> v1.22.1 · r96-r98 patch — rematch stamp preservation (`content_changed_at` kept on hash-identical re-applies) · `maintain --recheck --conditions` backfill · `flint distill` file cap (`distill_max_runes`) + no same-minute overwrite · id/min-score input guards · [full changelog](docs/HISTORY.md)
> v1.21.1 · r91-r94 patch — path redaction on scan errors · vault root guard (persist after success) · tuning-key wiring · scan hardening (retry · touch refresh · symlink skip · 60k+ delete) · [full changelog](docs/HISTORY.md)
> v1.21.0 · vault-bridge-index — schema v16 `vault_files` · `flint index` (snapshot-diff, one batch commit) · `read` vault fusion (`source:"vault"`, cap 5) · scan p95 metering · [full changelog](docs/HISTORY.md)
> v1.20.3 · r89 patch — merge & import-ref join the supersede lock domain (cycle-proof) · [full changelog](docs/HISTORY.md)
> v1.20.2 · r88 patch — advisory-lock single-connection supersede · PAM self-link skip · truncation test · [full changelog](docs/HISTORY.md)
> v1.20.1 · r84-r87 patches — link accrual completeness (mixed batch · PAM round trip) · supersede race hardening · schema v15 unique link index · [full changelog](docs/HISTORY.md)
> v1.20.0 · knowledge-schema-search — schema v14 knowledge links (refines · supports · contradicts · duplicates) · read `--min-confidence` / `--origin` filters · supersede cycle guard & chain view · [full changelog](docs/HISTORY.md)
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

Optional demo data: `flint seed --demo` loads the embedded synthetic dataset into an isolated demo database (the DSN's database name + `_demo`; idempotent, `--reset --yes` removes only the demo rows). From a checkout, `make seed FLINT_SEED_DSN=postgresql://flint:flint-dev@localhost:5434/flint` targets an explicit DSN.

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
| `flint seed --demo` | Load the embedded synthetic demo dataset into an isolated demo database (database name + `_demo`, `FLINT_DEMO_DSN` override; idempotent; `--reset --yes` deletes only `demo/` rows — mismatched owner exits 3) |
| `flint update` | Self-update to the latest release (checksum-verified, `--check` dry-run) |
| `flint tuning` | Inspect/override threshold keys (`list`/`get`/`set`/`reset`; autotune surfaces `auto off\|shadow` · `what-if` · `status`); bare invocation on a TTY opens the interactive form (subcommands, flags, non-TTY and FLINT_NO_TUI keep the flag mode; `--tui` forces it) |
| `flint write <kind>` | Record knowledge from stdin (idempotent) |
| `flint import [path]` | Convert rule files (CLAUDE.md etc.) to pending knowledge (`--commit`); `--format jsonl\|pam` migrates knowledge back in (round-trip re-import · PAM v1.0 intake) |
| `flint export [--format f]` | Export knowledge as jsonl·md·txt·csv·pam (PAM v1.0 portable memory-store; `--status`·`--tag`·`--since` filters, `--out` file with `.sha256` sidecar; PAM `--sign` Ed25519 signature, spec §18) |
| `flint index [--vault p]` | Index a markdown vault into `vault_files` (snapshot-diff, one batch commit per diff; `--vault` persists the path, `--limit` caps the scan) |
| `flint read <query>` | Search active knowledge (vault rows appended after the knowledge block when indexed); bare invocation on a TTY opens the interactive browser (query input · result list · detail toggle); opt-in `--related N` appends undirected knowledge-link neighbors (cap 5) and `--why` appends per-row evidence (token tier · RRF signals · zero-hit rationale) — the flagged-mode output is unchanged |
| `flint hot --session <id>` | Inject context at session start |
| `flint brief [query]` | Masked, read-only brief to paste into any external AI (the bridge) |
| `flint done --session <id>` | Wrap up a session |
| `flint approve --id n` | Promote pending to active |
| `flint maintain` | Curate: check thresholds, invalidate, merge, `--merge-audit` duplicate pair report (multiset Dice, cost-estimated, listing only), `--recall-audit` bad-recall feedback report (listing only — disposition always via the approval-required subcommands), `--age-audit` staleness report with `suggested_vol`, `--reaffirm` validity refresh, `--recheck` conditions review, `--gc [--shallow] --yes` dolt garbage collection (operator command — disconnects all sessions; reconnect + schema recheck in the same run), (`--tui` interactive review, opt-in) |
| `flint distill --out DIR` | Emit recall-scored unstamped insights as one aggregate markdown file (`--min-score` overrides `distill_min_score`; emitted rows stamp `distilled_at`, reruns yield the remainder — ADR-0018) |
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
| `flint doctor --check c` | Health checks (21 kinds, subset via `--check`, fail → exit 1) |
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
