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
| 0 | success (including duplicate) |
| 1 | general error — overloaded code: retries_exhausted · unknown command · usage · `doctor` fail (one or more fails) · zero-hit silence (eligible zero-hits exit 1 with no output — a normal result of read) |
| 2 | server_down |
| 3 | invalid arguments and validation failures (`SchemaError` — field-level validation: invalid `--status` · `--limit`, valueless value-bearing flags, etc.) |

- exit 1 overload disambiguation: `read` zero-hit silence produces no
  output; every other exit 1 carries one errLine (JSON) — the MCP tool path
  (`flint mcp`) decides `isError` by this output presence (empty-output
  exit 1 = normal, exit 1 with output = error).
- A schema-version mismatch (meta schema_version ↔ `db.LatestSchemaVersion`)
  is not exit 3 — it surfaces through the `doctor` schema check failing
  (exit 1) and `setup` migration (the promotion-gate registry).

- store-free fast path: `version` · `teardown` · `mcp` · `update` · `help`
  respond immediately without starting the DB. `setup` treats a down DB as
  its main scenario and only proceeds storeless when the connection fails.
- Hook-only entry points `session-start` · `session-end` are registered but
  hidden from usage (`hiddenFromUsage`).
- Data mobility (v1.3~v1.4): `export [--format jsonl|md|txt|csv|pam]
  [--out FILE] [--status active|all] [--tag X] [--since RFC3339]` — five
  formats; writing `--out` also creates a `.sha256` sidecar (sha256sum -c
  compatible); invalid flags · formats are exit 3. `import [--format
  jsonl|pam] [FILE]` — a shared one-shot row cap for export · import
  (`export_import_max_rows`, default 100000); exceeding it returns an error
  JSON (with `max` · `count`) and exit 3.

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
flint nosuchcmd
# → {"error":"unknown command: nosuchcmd"}, exit 1
```
