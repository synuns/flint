# CLI Contract — `flint <command> [args]`

> English documentation. For a Korean overview, see [README.ko.md](../../README.ko.md).

Sources: `lib/wire/registry.go` · `lib/wire/run.go` · `lib/store/pipeline.go` (MapExit).

## Contract

- Subcommands register via `wire.Register` (implementation-package init —
  open-closed).
- The single source of the human-facing command list is the `usageOrder`
  slice in `lib/wire/registry.go`. Usage sentences (`flint` with no args ·
  `flint help`) assemble from this order.
- Output is one-line JSON on stdout. Errors are JSON too (an `error` key —
  structured, no raw concatenation).
- Exit codes, four kinds (`store.MapExit`):

| code | meaning |
|---|---|
| 0 | success (including duplicate) — and `read` eligible zero-hits since v1.5.3 (`--json` emits `{"hits":0}`; text mode prints nothing — zero-hit silence, a normal result of read) |
| 1 | general error — retries_exhausted · `doctor` fail (one or more checks fail) |
| 2 | server_down |
| 3 | invalid arguments and validation failures (`SchemaError` — field-level validation: invalid `--status` · `--limit`, valueless value-bearing flags, etc.) — and, since v1.5.3, unrecognized `-`-prefixed `read` args · unknown command · usage errors (`done` usage error · bare `maintain`) |

- exit unification (v1.5.3): unknown command · `done` usage error · bare
  `maintain` usage moved from 1 · 0 · 1 to a uniform exit 3. Usage/flag
  errors report `"error":"usage"` (ErrLine shape unchanged — value
  extension only; the message embeds an allowed-flags hint).
- the `"error":"usage"` type applies to **every** command since v1.5.4 —
  `maintain` · `doctor` · `report` · `audit` · `backup` · `import` ·
  `update` (v1.5.3 shipped it for `read` · `done` · bare `maintain`
  only): unknown-flag/argument errors report `"error":"usage"` with an
  allowed-flags hint. Exit codes are unchanged per command — unknown-flag
  paths stay exit 3, while `maintain` mode-combination errors (e.g.
  `--merge` without `--from`) keep their existing exit 1. Genuine
  internal failures keep their existing error types.
  v1.5.5 completes the sweep — `hot` · `brief` · `compact` · `nudge`
  unknown-flag/argument errors report `"error":"usage"` with an
  allowed-flags hint and exit 3 as well; commands left without the
  usage type: 0.
- `flint --version` and `flint -v` (v1.5.4) are aliases of `flint
  version` — same output, exit 0, store-free fast path.
- `-h` / `--help` (v1.5.5) are global aliases of `flint help` — same
  output (usage line + command summary), exit 0, dispatch-level
  handling before the command table (the `--version`/`-v` convention).
- `write --help` (v1.5.5) prints usage guidance instead of treating
  the flag as content input.
- usage guidance, three distinct shapes (bare `flint` wording corrected
  by v1.5.5 measurement): unknown command emits one JSON line with
  `message` · `usage` keys — no `error` key — and keeps exit 3
  (v1.5.4); `flint help` prints the usage line plus the command summary
  and exits 0; bare `flint` emits `{"usage":...}` alone — no `message`
  key — and exits 1.
- `read --limit` above the cap (50, default 10) is clamped; the clamp is
  surfaced (v1.5.4) in `--json` mode only — one additive line with
  `limit_requested` (the value the caller passed) and `limit_applied`
  (the clamped value), the same appended-line convention as `hits:0` and
  `as_of`. Text mode output is unchanged.
- `read` zero-hit silence no longer exits 1: eligible zero-hits are
  exit 0 — `--json` emits `{"hits":0}`, text mode stays silent.
  Exit 1 always carries one errLine (JSON) — the MCP tool path
  (`flint mcp`) decides `isError` by exit code (0 = normal, including
  zero-hit).
- A schema-version mismatch (meta schema_version ↔ `db.LatestSchemaVersion`)
  is not exit 3 — it surfaces through the `doctor` schema check failing
  (exit 1) and `setup` migration (the promotion-gate registry).

- store-free fast path: `version` · `teardown` · `mcp` · `update` · `help`
  respond immediately without starting the DB. `setup` treats a down DB as
  its main scenario and only proceeds storeless when the connection fails.
- DB-down precedes argument validation (v1.5.5, one-line priority): for
  every store-connected command the store connection is attempted before
  the handler parses arguments, so a down DB surfaces first
  (server_down · exit 2) even when the arguments are also invalid.
- `teardown` is a dry-run by default (v1.5.3): it prints what would be
  removed and exits 0; actual removal requires `--yes` (the
  `maintain --invalidate` convention). The plan notice names what goes
  down. `--purge` also removes data volumes (unchanged).
- Hook-only entry points `session-start` · `session-end` are registered but
  hidden from usage (`hiddenFromUsage`).
- `approve` failure messages embed the usage — `--id N [--supersede OLD]`
  — so agents can self-correct without re-reading docs (v1.5.3).
- Data mobility (v1.3~v1.4): `export [--format jsonl|md|txt|csv|pam]
  [--out FILE] [--status active|all] [--tag X] [--since RFC3339]` — five
  formats; the `pam` row count equals the published rows actually
  written (v1.5.5) — pending rows are excluded by default and never
  counted; writing `--out` also creates a `.sha256` sidecar (sha256sum -c
  compatible); invalid flags · formats are exit 3. `import [--format
  jsonl|pam] [FILE]` — the format is sniffed from the file extension
  when `--format` is omitted (v1.5.5; a mismatch suggests the matching
  migration command); a shared one-shot row cap for export · import
  (`export_import_max_rows`, default 100000); exceeding it returns an error
  JSON (with `max` · `count`) and exit 3.
- TUI screens (v1.5.0, `lib/tui`; adaptive default since v1.5.2; flag
  paths fully preserved): `pending` · `export` · `report` are
  adaptive — a bare invocation in a terminal opens the TUI, while a
  piped/redirected run (a redirect such as to `/dev/null` is no longer
  mistaken for a TTY — v1.5.5 terminal detection fix) or any flag
  keeps the existing output (isTerminal fallback). Explicit `--tui`
  forces the TUI; non-terminal
  `--tui` invocations print one error line and exit 3 ("TUI requires a
  terminal — use flag-mode instead"). `pending --tui` must stand alone
  and `report --tui` cannot combine with the view flags (`--html` ·
  `--text` · `--out`) — exit 3. `setup` behaves the same (unchanged):
  no args in a terminal starts the wizard, non-terminal runs the
  existing no-arg path. Escape hatch: setting `FLINT_NO_TUI` to a
  non-empty value blocks the adaptive TUI entry even in a terminal
  (agents · CI force the non-interactive path; an explicit `--tui`
  still wins).

## Boundaries

- Non-interference of hook-entry commands: `compact` exits 0 silently when
  the DB is absent; `nudge` ends with an exit code only, no error output
  (server down = 2).

## Prohibitions

- Registering without adding to `usageOrder` (the command becomes invisible
  to humans — `TestUsageOrderMatchesRegistry` catches the drift).
- Hardcoded literal command names in usage sentences — always assemble from
  `usageOrder`.

## Example

```sh
flint read "migration decisions"
# → per-row "id|topic|summary" text (default fields, one row per knowledge), exit 0
flint read "migration decisions" --json
# → per-row JSONL ({"id":...,"summary":...,"topic":...}), exit 0
flint read "migration decisions" --json --nosuchflag
# → {"error":"usage","message":"...allowed flags..."}, exit 3
flint nosuchcmd
# → {"message":"unknown command: nosuchcmd","usage":"flint <...|...> [args]"}, exit 3
```
