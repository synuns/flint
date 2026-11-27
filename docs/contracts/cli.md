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
  · `engine` respond immediately without starting the DB. `setup` treats a
  down DB as its main scenario and only proceeds storeless when the
  connection fails.
- `setup` engine surface (v1.30.0): the compose definition is embedded in
  the binary (project name `flint`, engine image pinned by digest —
  ADR-0020) and staged to a temp file under the config dir, so `flint
  setup` no longer depends on a repository checkout. Host port defaults
  to 5434 (ADR-0010). `setup --port N` runs the managed path: an
  existing `flint`-project install is adopted (port reused); when the
  requested port is taken, setup probes upward for a free loopback
  port — 5499 excluded (v1.29.0 dedicated test-engine reservation) —
  and records the assigned port in `config.json` (`port` field, 0600).
  Port-occupant identification is scoped to this `--port` path.
  `setup --engine-url DSN` switches to external engine mode: the DSN is
  accepted only from the `FLINT_ENGINE_URL` environment variable or
  `--engine-url-file PATH` (inline values are rejected — shell history ·
  process listing hygiene), only loopback hosts (localhost · 127.0.0.1 ·
  [::1]; unix sockets and remote hosts are refused), and output masks
  the DSN. `config.json` records `engine.mode: external`, and
  `maintain --purge-dropped` stays managed-mode-only. A non-`flint`
  compose project already holding the engine stops setup with a warning
  (manual conversion — RUNBOOK §11); it is never migrated or recreated
  automatically.
- engine modes are opt-in (v1.31.0 — ADR-0021): `setup --engine
  managed-container|native|external` records `engine.mode` in `config.json`
  (idempotent; any other value exits 3). The native mode downloads the
  pinned engine binary (hash + `-version` verified, atomic swap under
  `engine/<version>/` with a `current` symlink), registers a platform
  service (macOS launchd · Linux systemd user, no secrets in the unit) and
  bootstraps credentials so the ADR-0010 default DSN connects — the mode is
  recorded only after that re-connect succeeds. `flint engine
  status|start|stop` operates on the recorded mode (managed-container
  drives the embedded compose · native drives the registered service ·
  external advises only; undefined pairs such as external start are
  rejected with guidance). `engine status` separates registered · running
  · version.
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
  [--out FILE] [--status active|all] [--tag X] [--since RFC3339]
  [--sign (pam only)]` — five
  formats; the `pam` row count equals the published rows actually
  written (v1.5.5) — pending rows are excluded by default and never
  counted; writing `--out` also creates a `.sha256` sidecar (sha256sum -c
  compatible); `--out -` means stdout (POSIX convention, same as omitting
  `--out` — no file, no sidecar); invalid flags ·
  formats are exit 3. `import [--format
  jsonl|pam] [FILE]` — the format is sniffed from the file extension
  when `--format` is omitted (v1.5.5; a mismatch suggests the matching
  migration command); a shared one-shot row cap for export · import
  (`export_import_max_rows`, default 100000); exceeding it returns an error
  JSON (with `max` · `count`) and exit 3 — the cap is decided by a single
  line-count scan before parsing (v1.6.0, no full-parse memory spike).
- PAM signing and intake verification (v1.6.0): `export --format pam
  --sign` attaches the PAM spec §18 signature block (payload
  {checksum, export_id, export_date, owner_id}, JCS-canonicalized →
  Ed25519; the signing key is auto-generated 0600 at
  `pam-signing-key` under the config dir — `FLINT_CONFIG_DIR` → wire
  `ConfigDir` → `os.UserConfigDir()/flint`, age conventions; `--sign`
  with any other format is a usage error, exit 3). `import --format pam`
  recomputes `integrity.checksum`
  — a mismatch is exit 3, never a silent pass — and verifies the
  signature when present; a failed signature prints one stderr warning
  and continues (spec SHOULD).
- `maintain --check` responses include a `next` key (v1.6.0) — the
  recommended action, the pending-import convention.
- `doctor --json` (v1.6.0) emits machine-readable output; the text
  output is unchanged and fail → exit 1 in both modes. `--json` is
  byte-identical to the default output — an explicit machine channel,
  not a second format; it diverges only under `--fix`, where it emits
  the `doctorOutput` JSON structure in place of the text fix report.
- TUI screens (v1.5.0, `lib/tui`; adaptive default since v1.5.2; flag
  paths fully preserved): `pending` · `export` · `report` are
  adaptive — a bare invocation in a terminal opens the TUI, while a
  piped/redirected run (a redirect such as to `/dev/null` is no longer
  mistaken for a TTY — v1.5.5 terminal detection fix) or any flag
  keeps the existing output (isTerminal fallback). Explicit `--tui`
  forces the TUI; non-terminal
  `--tui` invocations print one error line and exit 3 ("TUI requires a
  terminal — use flag-mode instead") — reported as `"error":"usage"`
  (v1.5.5 review: an invalid-argument class, closing the last gap in
  the usage-type sweep). `pending --tui` must stand alone
  and `report --tui` cannot combine with the view flags (`--html` ·
  `--text` · `--out`) — exit 3. `setup` behaves the same (unchanged):
  no args in a terminal starts the wizard, non-terminal runs the
  existing no-arg path. Escape hatch: setting `FLINT_NO_TUI` to a
  non-empty value blocks the adaptive TUI entry even in a terminal
  (agents · CI force the non-interactive path; an explicit `--tui`
  still wins). `maintain --tui` (v1.6.0) is **opt-in only** — no adaptive
  entry (the subcommand arguments stay authoritative): dup-suspects ·
  conflict-suspects · stale lists → a detail view with a what-survives
  merge preview → m merge · k keep · i invalidate · q quit, every action
  delegating to the existing maintain subcommands; non-terminal `--tui`
  keeps the one-error-line + exit 3 convention above.

- Knowledge sync (v1.7.0 — `flint sync`, a **human operator command** for
  machine moves · team sharing over a Doltgres remote; not exposed to
  agents — SKILL.md and the MCP tool map omit it by design):
  - **A single active device is recommended** — simultaneous multi-device
    use is not supported (a Doltgres engine limitation, not a flint
    policy). Move between machines sequentially: push from one, pull on
    the other.
  - `flint sync remote add <name> <url>` registers a remote; the url
    scheme whitelist is **https · http · file** (a `postgres://` url is
    rejected at add time with **exit 3**). A `file://` path is resolved
    **on the Doltgres server**, not the flint client;
    `flint sync remote list` shows names with the url **masked**;
    `flint sync remote remove <name>` deletes it. A duplicate name is an
    error.
  - **The remote url is never written to any output or log** — not in
    listings (masked), not in errors, not in docs or the repository. This
    is a privacy defense of the same rank as secret sanitization.
  - `flint sync push <remote>` pushes the `main` branch and reports
    `{status, hash}` — an up-to-date no-op reports `up-to-date`; no
    `--dry` (push is a single dolt_push call). `flint sync pull <remote>
    [--dry]` runs fetch+merge, where `--dry` (**pull-only**) stops after
    fetch with nothing applied; a diverged branch is reported with
    guidance to run `flint sync pull`.
  - Push and pull run under a **30s context timeout** (a hung remote
    fails fast instead of blocking the operator).
  - A merge conflict surfaces as `ErrMergeConflict` — an error JSON with
    **exit 1** plus guidance to resolve manually with the dolt CLI
    (`dolt_conflicts`) and re-run `flint sync pull`; the aborted
    transaction leaves the working set intact (no partial apply —
    measured). Automatic resolution is forbidden (destructive-change
    defense). An unregistered or unreachable remote keeps the cause in
    the error and never exposes the url.
  - A pull error matching `DoltgresHandler caught panic` (the engine's
    multi-device write path) prints dedicated guidance: use a single
    active device, and recover the local branch with
    `flint sync reset --to-remote <remote>` — a `dolt_reset --hard` of
    `origin/main` that **prints a data-loss warning and requires
    `--yes`** (the teardown convention). Local unpushed commits are
    discarded by the reset.
- `flint audit` (v1.7.2) omits telemetry commits from the default list
  and `--stat` output (`NOT LIKE 'telemetry: %'`); the `--telemetry`
  flag includes them.
- Recall decay (v1.7.0 — schema v11, additive columns
  `knowledge.recall_score` (int, default 0) · `last_recalled_at`
  (timestamptz, NULL); v10 binaries remain read-compatible). A `read` hit
  reinforces the row (score+1 · last_recalled_at=now). The effective score
  is `recall_score − decay_rate × days since last recall` with a floor of 0
  (whole 24h days; rows never recalled keep their score — no new-entry
  penalty), and feeds `hot` · `brief` ranking (score DESC, ingested_at DESC
  stable sort — byte budget unchanged). `flint maintain --decay-audit`
  (read-only, exit 0, `--limit` default 20) reports decayed active rows as
  JSON — `{status, decay_rate, archive_below, count, items[]}` where each
  item carries `id · score · last_recalled · effective` and
  `recommend:"archive"` only when `archive_below` is set and the effective
  score falls below it; archiving itself is **approval-gated** via the
  existing `maintain --invalidate` (`--yes`/`--reason`) — never automatic.

- Work state (v1.38.0 — `flint state`, the intent surface): open work is
  recorded unconditionally — unlike knowledge, there is no claim gate or
  confidence review (a different ontology: intent fails by being carried
  out or abandoned, not by being true or false). `flint state open --task T
  [--detail D] [--next A] [--project N]` opens or updates a work_state row
  and prints one-line JSON `{"id":N,"status":"open|updated|noop"}` — the
  task is normalized and hashed server-side (NFKC · trim · lowercase ·
  symbol folding), so a re-reported open task with the same hash updates
  the existing row (detail · next_action · `updated_at`) instead of adding
  one. `flint state list [--all]` lists open rows project-scoped, latest
  `updated_at` first (`--all` includes resolved rows — resolved rows stay
  out of injection and are purged after
  `work_state_resolved_retention_days`, default 90, by
  `maintain --stale-state`). `flint state open --task T` requires a
  non-empty task (empty or whitespace-only is exit 3 — CLI and MCP are
  the same contract). `flint state resolve --id N
  [--by K]` closes an open row; `--by K` records the resolving knowledge id
  (a soft reference — NULL when omitted). An open row untouched for
  `state_stale_days` leaves the hot snapshot, and `maintain --stale-state`
  proposes its resolution (approval-gated, the decay-audit pattern).
- MCP `flint_work` — the same work-state surface as one tool with an
  `action` field: `open` · `update` · `resolve` · `list`.
- Read claim filter (v1.38.0): `flint read` defaults to the
  `recall_knowledge` view — knowledge rows with claim `event` are excluded
  from recall (event records are justification evidence, not recallable
  knowledge). `flint read --claim <value>` applies the claim filter, so
  `--claim event` reaches event rows through the explicit-lookup path;
  an unrecognized claim value is exit 3. The `--json --fields` allowlist
  gains `claim`. The default (recall) path projects all 10 view columns
  (status included); `read_count` is not part of the view — use
  `--claim event` to read event rows with the base-table shape.
- Approve claim gate (v1.38.0): `flint approve` is the single claim gate.
  Promoting a row whose claim is empty (or the migration backfill `event`)
  requires `--claim <decision|procedure|lesson|event>` — without it the
  promotion exits 3 with the usage embedded (self-correction convention).
  Symmetrically, a knowledge write without a claim lands `pending`
  regardless of confidence (trial period — the write path never rejects).
  MCP `flint_approve` follows the same rule.
- Session-summary copy rejection (v1.38.0): a knowledge payload whose
  summary is identical to (or a prefix match of) the session summary is
  rejected with exit 3 — the mechanical refusal of raw session-dump
  deposits.
- Recall-audit recommend (v1.39.0): each `maintain --recall-audit` item
  carries `recommend` ∈ `supersede|expire|hold` — `hold` below the
  `recall_audit_recommend_min` floor (default 2, bad-recall count);
  at or above the floor, `supersede` when the row itself has recall
  history (`recall_events`) and `expire` when it does not. The field is
  report-only — disposition stays with the approval-required maintain
  subcommands (the decay-audit pattern; re-confirm is excluded and
  planned for v1.40.0).
- Read recall signal (v1.39.0): the `read` fallback re-ranking fuses
  each row's recall count as one additive RRF signal (the fifth, after
  match · read_count · jamo · topic — recall-proven rows rank higher on
  tied scores). The signal is read only when the fallback fires; rows
  without it keep the existing order, and the `--why` signal list gains
  `recall` only then. The RRF formula structure and the
  `:vol:immutable:` decay-exempt contract are unchanged.
- State session link (v1.39.0): `flint state open` accepts
  `--session LABEL` — the flag wins; without it the session-label file
  fallback (the same receive path `flint hot` uses) supplies the label.
  The recorded label lets `flint hot` resolve the current project from
  the latest work_state row and prioritize that project's rows in the
  snapshot; an unresolved project (no label, or a row with no project)
  keeps the existing output byte-identical.
- Vol suggestion defaults (v1.40.0): an `age-audit` row without an
  existing `:vol:x:` tag carries a `suggested_vol` proposal assembled
  from two default tables. The proposal is report-only — decay exemption
  is **never automatic** (the `EffectiveScoreFor` decay circuit is
  unchanged; exemption follows only approved tags). Override precedence:
  existing vol tag > claim default > source default > current keyword
  rules. Claim defaults (`ClaimDefaultVol`, untagged rows with a claim):

  | claim | default vol |
  |---|---|
  | `decision` | `vol:immutable` |
  | `procedure` | `vol:conditional` |
  | `lesson` | `vol:conditional` |
  | `event` | `vol:immutable` |

  Source defaults (`SuggestVolClassForSource`, untagged rows without a
  claim — the new function keeps `SuggestVolClass` byte-equivalent for
  other callers):

  | source | default vol |
  |---|---|
  | `human` | `vol:immutable` |
  | `correction` | `vol:immutable` |
  | `import` | `vol:conditional` |
  | `vault` | `vol:conditional` |

  `agent` and an empty source keep the current keyword rules (the
  `SuggestVolClass` behavior).
- Apply-vol path (v1.40.0): `maintain --age-audit --apply-vol [--yes]` —
  without `--yes` it is a dry-run listing of the pending vol proposals
  (`"applied":false`, exit 0); with `--yes` the proposals are applied as
  `:vol:x:` tag merges — the tag is appended to `tags`, existing tags are
  preserved (a merge, not a supersede) — through the repo write path with
  one dolt commit per run. Rows that already carry a vol tag are skipped,
  so a repeated run converges to a no-op.

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
flint read "migration decisions" --claim event
# → event rows included via the explicit claim filter, exit 0
flint state open --task "fix flaky seed check" --detail "retry loop races the prune" --next "serialize the loop"
# → {"id":7,"status":"open"}, exit 0
flint state open --task "Fix: flaky seed check!"
# → {"id":7,"status":"updated"}, exit 0 — same server-side hash, row updated
flint state list
# → open rows, one-line JSON each, exit 0
flint state resolve --id 7 --by 910
# → row 7 resolved with knowledge 910 as the soft reference, exit 0
flint nosuchcmd
# → {"message":"unknown command: nosuchcmd","usage":"flint <...|...> [args]"}, exit 3
```
