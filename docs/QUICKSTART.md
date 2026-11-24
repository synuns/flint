# flint Quick Start

> English documentation. For a Korean overview, see [README.ko.md](../README.ko.md).

Get a local flint running without a repository checkout. Each step takes
under a minute.

## 1. Install the binary

```sh
curl -fsSL https://raw.githubusercontent.com/synuns/flint/main/scripts/install.sh | bash
```

Prebuilt binary, sha256-verified against the release `checksums.txt`,
installed to `~/.local/bin` (override with `--bin-dir DIR`). Alternatives
— `go install github.com/synuns/flint/cmd/flint@latest`, a manual
[release](https://github.com/synuns/flint/releases) download, or Homebrew
(paths 2–4 in [README.md](../README.md)). The only runtime prerequisite
is Docker: flint ships the engine compose definition inside the binary.

## 2. One-command setup

```sh
flint setup           # ①embedded compose up --wait ②migrate (stdout closes with one JSON status: {"status":"ok","mode":"managed","port":N})
flint seed --demo     # synthetic demo data (isolated demo database)
flint doctor          # diagnostics: verify the install (29 checks; fail → exit 1)
```

`setup` writes its embedded compose file — project name `flint`, engine
image pinned by digest (ADR-0020) — to a temp path under the config dir
and starts Doltgres with `--project-name flint`, so it works from any
directory: no git clone, no compose file on disk. Host port defaults to
5434 (ADR-0010). The managed path runs for `--port N` and equally for a
bare first install (no recorded DSN): an existing `flint`
-project install is adopted, and when the requested port is taken setup
probes upward for a free loopback port (5499 excluded — reserved for
the v1.29.0 dedicated test engine), records the assigned port in
`config.json`, retries ping (30 × 200ms code constants) and finishes
ping + migrate in the same run.

External engine mode (v1.30.0): `flint setup --engine-url` skips engine
install entirely and records a Doltgres you run yourself — loopback
hosts only (localhost · 127.0.0.1 · [::1]). Supply the DSN via the
`FLINT_ENGINE_URL` environment variable or `--engine-url-file PATH`;
inline values are rejected so the DSN never lands in shell history or
process listings. Output always masks the DSN. `config.json` records
`engine.mode: external`, and purge operations stay managed-mode-only.
Tear down with `flint teardown` (`--purge` also removes volumes).

Native engine mode (v1.31.0, opt-in — ADR-0021): `flint setup --engine
native` records the mode and runs the native path in one go — the
pinned engine binary is downloaded (platform hash + `-version`
verified, atomically swapped under `engine/<version>/` with a
`current` symlink), a platform service is registered (macOS launchd ·
Linux systemd user) and first-boot credentials are initialized so the
ADR-0010 default DSN connects. `flint engine status|start|stop` then
controls the recorded mode (external advises only). Managed container
stays the default for fresh installs; container→native transition and
the native upgrade/rollback procedure live in RUNBOOK §11.

Seeding is safe to re-run — identical payloads resolve to hash dedup with
`{"status":"duplicate"}` exit 0 (idempotent). `flint seed --demo` writes only
to the derived `_demo` database (never the running one; `FLINT_DEMO_DSN`
overrides the derivation); `flint seed --demo --reset --yes` deletes only the
seeded `demo/` rows. From a checkout, `make seed FLINT_SEED_DSN=<dsn>` targets
an explicit DSN (that explicit-DSN guard is deliberate — a protection against
polluting production databases). Verify:

```sh
flint read dump        # search: active_knowledge view only
flint hot --session x  # inject at session start (hot_budget default 2,000 chars)
flint audit --limit 5  # audit: last 5 entries of dolt_log
flint doctor --check db   # diagnostics: db subset of the 29 checks (fail → exit 1)
flint report --days 7     # report: daily intake · zero_pct · p95 · stale (default 7 days)
flint report --serve      # dashboard: localhost-only HTTP view of the same diagnose source (Ctrl-C to stop)
flint restore --file <dump> --drill  # recovery drill (source untouched, row-count compare only)
```

## 3. Connect your harness

**Claude Code** — plugin (3 hooks + the `/flint` command):

```text
/plugin marketplace add synuns/flint
/plugin install flint@flint
```

**Codex · Gemini · Cursor · OpenCode · Copilot** — automatic registration:

```sh
flint setup --mcp
```

Detects installed harnesses and registers the MCP server in each one's native
config. Environments where Copilot auto-registration is impossible use the
manual procedure ([adapters/copilot.md](adapters/copilot.md)); OpenClaw and
Hermes use documentation adapters ([adapters/](adapters/)).

## 4. The config file (optional)

To avoid passing the DSN as an environment variable every time, put it in a
config file (v0.7). Since v1.30.0 the same file also records what setup
decided: `port` (the assigned engine port, `--port` path) and
`engine.mode: external` (external engine mode).

```sh
# Linux: ~/.config/flint/config.json · macOS: ~/Library/Application Support/flint/config.json
mkdir -p ~/.config/flint        # macOS: mkdir -p "$HOME/Library/Application Support/flint"
printf '{"dsn":"postgresql://flint:flint-dev@localhost:5434/flint"}' \
  > ~/.config/flint/config.json
```

- Default location is `UserConfigDir()/flint/config.json` (macOS
  `~/Library/Application Support/flint/config.json`,
  Linux `~/.config/flint/config.json`).
- Relocate the directory with the `FLINT_CONFIG_DIR` environment variable
  (the gh `GH_CONFIG_DIR` model).
- Precedence: `FLINT_DSN` env > `dsn` in `config.json` > default
  `localhost:5434`. A corrupt or empty file falls back to the default (not an
  error).
- Details in [docs/TUNING.md](TUNING.md) §2.

For audit response, contamination handling, recovery drills, and backup
retention procedures, see [docs/RUNBOOK.md](RUNBOOK.md) — §11 also covers
the engine image upgrade procedure and the manual conversion path for
pre-v1.30.0 installs.

## Environment variables

- `FLINT_COMMAND_TIMEOUT` — per-command deadline in seconds (default 300;
  `0` disables). Hooks (session-start · session-end · done · compact ·
  nudge) and interactive TUI commands (read · tuning · pending · maintain ·
  export · report · setup) are never capped when interactive — they wait on
  the user. The dual-mode commands (read · pending) invoked with `--json`
  (MCP `flint_read` · skill scripts) keep the cap; the other exempt
  commands keep the exemption on every route (setup by design — its
  `--json` mode is one-shot; tuning · maintain · export · report have
  no `--json` flag). Applies to one-shot commands (`doctor`,
  `write`, `hot`, ...) so a hung engine cannot block a session
  indefinitely.
