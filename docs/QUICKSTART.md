# flint Quick Start

> English documentation. For a Korean overview, see [README.ko.md](../README.ko.md).

Get a local flint running in two steps. Each step takes under a minute.

## 1. Install

```sh
git clone https://github.com/synuns/flint.git flint && cd flint
make install    # builds and installs GOPATH/bin/flint (Go 1.26+, deps pgx/v5 · x/text)
```

You can also grab a [release binary](https://github.com/synuns/flint/releases) and
put it on your PATH — binaries, checksums, and the plugin zip are attached
automatically.

## 2. One-command setup

```sh
flint setup     # ①compose up --wait(60s) ②migrate ③seed guide ④plugin guide
# Demo data: 3 projects + 5 decisions + 1 pending + 1 preference + 2 playbooks
make seed FLINT_SEED_DSN=postgresql://flint:flint-dev@localhost:5434/flint
```

`setup` starts Doltgres (host port 5434, ADR-0010), migrates the schema, then
prints step-by-step progress (a single JSON line with `--json` or on a
non-TTY — v0.17.0). Run with **no arguments on a terminal** it opens a huh
wizard first (v1.5.0): pick the exposure profile (normal | minimal) and read
the backup-key note, then the normal chain runs and records your choice.
Cancelling the wizard (ctrl+c) or running on a non-TTY falls back to the
plain chain with no side effects, and any flag (e.g. `--exposure`,
`--json`) skips the wizard entirely. Since v0.7 the compose wait
uses `up -d --wait --wait-timeout 60` — it waits up to 60 seconds for the
container health check to pass before moving on. A missing docker or a compose
failure is not a hard error: it stays a warning, prints one line with install
guidance (`https://docs.docker.com/get-docker/`), and continues — if the DB is
already up, the remaining steps (migrate and friends) complete normally.
Default DSN `postgresql://flint:flint-dev@localhost:5434/flint` — override with
the `FLINT_DSN` environment variable or a config file (section 4 below).
Tear down with `flint teardown` (`--purge` also removes volumes).

Seeding is safe to re-run — identical payloads resolve to hash dedup with
`{"status":"duplicate"}` exit 0 (idempotent). Requiring an explicit target DSN
is deliberate (a guard against polluting production databases). Verify:

```sh
flint read dump        # search: active_knowledge view only
flint hot --session x  # inject at session start (hot_budget default 2,000 chars)
flint audit --limit 5  # audit: last 5 entries of dolt_log
flint doctor --check db   # diagnostics: db subset of the 17 checks (fail → exit 1)
flint report --days 7     # report: daily intake · zero_pct · p95 · stale (default 7 days)
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

## 4. Relocate the config file (optional)

To avoid passing the DSN as an environment variable every time, put it in a
config file (v0.7).

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
retention procedures, see [docs/RUNBOOK.md](RUNBOOK.md).
