# flint Threshold Tuning Guide

> English documentation. For a Korean overview, see [README.ko.md](../README.ko.md).

flint thresholds and budgets are overridden through the meta table under the
no-hardcoding principle. Defaults live in code (`lib/store/thresholds.go`);
meta keys take precedence. Invalid values fall back to the default with a
warning (not an error).

## 0. Tuning guide (v0.24.0 — threshold design review)

**When to tune** — start from defaults and override **only when a warning
pattern repeats after an observation window (14 days)**. Tuning without
observation means picking numbers while ignorant of the distribution
(observation-first principle — the zero-near sample floor is the existing
implementation of this principle).

**Retuning triggers** — adjust a key only when its pattern repeats:

| Repeated observation | Key to adjust | Direction |
|---|---|---|
| Out-of-vocab warnings persist (not typos) | vocab_tags · vocab_domains | add personal vocabulary |
| zero-near warns repeatedly | suggest_min_score | lower (suggestion threshold) |
| zero-near 'judgment deferred' persists | — | no adjustment — wait for samples |
| pending-age warns repeatedly, unattended | pending_max or promotion flow | process before values |
| dup_susp warns repeatedly | dup_susp_max | raise (accept normal diversity) |
| Recall absence accumulates (doctor recall-starved) | recall_warn_writes | lower (warning threshold) |
| Fallback rate keeps climbing | (search-quality area — PLAN §15) | not a key |

**Warnings must be actionable** — if you cannot decide on an action, it is not
a tuning target. Every warning ships with fix guidance (doctor). Trigger
surfaces: mostly manual doctor and maintain; session-start guidance covers
pending and updates only (non-interference §13).

## 1. Tuning the hot budget (injection cap)

Procedure (closing §15 deferred decision "injection budget cap: confirm with
2-week hot_view measured p95"):

1. **Observe (2 weeks)** — record `hot_calls` and `hot_p95` from
   `flint status` each session (v0.3 instrumentation — in-process stats,
   observation is per session).
2. **Update the budget** — the hot cap is confirmed via the `hot_budget` meta
   key (since v0.4 `flint hot` reads this key on every run — unset or invalid
   falls back to the default of 2,000):
   ```sh
   psql "$FLINT_DSN" -c "INSERT INTO meta (key, value) VALUES ('hot_budget','2400')
     ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value"
   ```
3. **Confirm the effect** — check that `flint hot --session <id>` output is
   clipped at the new cap and that `hot_p95` in `flint status` stays below it.
4. **Measured surveillance (v1.1.0)** — `effect.bytes` in `flint report`
   (hot/brief `avg_bytes` · `bytes_14d`) measures actual injected and emitted
   bytes daily against the `hot_budget` and `brief_budget` caps — budget
   rationale comes from these measured values.

## 2. Threshold overrides (§15 deferred decision "adjust threshold values during operation" — closed)

Every tuning value is adjusted only via code defaults (single source:
`lib/store/thresholds.go`) + the meta-key overrides in this table — never
commit personal tuning values to code or docs (repository rule). Constants
that were hardcoded until v0.16.3 (suggest · conflict · backup-age ·
zero-near · pending-age) are consolidated into this table too.

Override management is `flint tuning` (v0.23.0) — `list`, `get <key>`,
`set <key> <value>`, `reset <key>`. set validates through the real parsing
path (rejects invalid values and 0 on fallback keys); personal tuning values
live only in the operational DB via this route.

Day-count overrides clamp at the 106751-day ceiling (≈292 years — the
largest day count a time.Duration holds): a value past it is treated as
effectively indefinite retention, not an error (maintain's
--days·--expire-pending N·--prune-invalidated N reject past-ceiling
values up front instead; report --days clamps — the safe-cutoff path).

`flint tuning --tui` (v1.9.3) is an opt-in interactive form (key list →
value edit → set/reset confirm) for humans. It must run alone (combining
it with other arguments exits 3) and it is the only TUI entry — the
no-arg and subcommand JSON paths are unchanged, so agents and scripts
are unaffected. On a non-terminal, or with FLINT_NO_TUI set, it exits 3
before the form starts. Form confirmations validate and emit their JSON
line through the existing set/reset standard path above.

| meta key | default | meaning | warning trigger |
|---|---|---|---|
| `pending_max` | 50 | cap on unrecorded (pending) decisions | maintain --check |
| `dup_susp_max` | 20 | cap on duplicate-suspect decisions | maintain |
| `conf_floor` | 0.8 | confidence floor (0–1) | maintain |
| `stale_sessions` | 3 | consecutive sessions without a record | done |
| `stale_days` | 90 | staleness criterion in days (unreferenced · unupdated) — Confluence stale-scan standard · quarterly-review convention | maintain --stale |
| `stall_hot_h` | 48 | hot-absence warning grace (hours, modeled on Healthchecks.io nodata) | doctor · maintain --check |
| `stall_write_h` | 72 | write-absence warning grace (hours) | doctor · maintain --check |
| `suggest_min_score` | 0.15 | similarity floor for zero_near classification (0–1) — rows below are excluded as unrelated candidates. Did-you-mean suggestion output was removed in v0.14.0; this only classifies metrics | read (zero-hit scan) |
| `suggest_scan_cap` | 5000 | row cap for the suggestion scan — defends against unbounded scans on large intakes | read |
| `conflict_dup_threshold` | 0.6 | similarity criterion for contradiction/duplicate classification (0–1, at-or-above = duplicate · below = contradiction). doctor conflicts uses the same default source but the override applies via maintain | maintain --conflict-suspects |
| `backup_age_warn_h` | 72 | backup freshness warning hours (h) — warn when exceeded | doctor backup-age |
| `zero_near_min_read` | 10 | sample floor for zero_near judgment (7-day read sum) — below it, judgment is deferred | doctor zero-near |
| `zero_near_warn_pct` | 30 | zero_near warning ratio (%) — at or above, suspect vocabulary mismatch and warn | doctor zero-near |
| `pending_age_days` | 7 | oldest-pending age guidance in days — warn + expiry guidance when exceeded | doctor pending-age |
| `pending_promote_days` | 3 | pending promotion suggestion window (recent N days) — deposits inside this window count as promotion candidates (v0.29.0 R1) | pending review verdict |
| `pending_promote_min_runes` | 40 | pending promotion suggestion minimum body runes — shorter bodies are held back (significance proxy) | pending review verdict |
| `recall_warn_writes` | 3 | record-count threshold for recall-absence warning — warn on done exit and next SessionStart when this many writes or more happen with zero reads today. **-1 disables** (1 warns on every record). 0 behaves like unset and falls back to the default | done · nudge |
| `dead_days` | 30 | dead_pct criterion days — share of knowledge with 0 recalls and this much elapsed (distillation · dedup cycles run weekly) | report effect |
| `pair_window_minutes` | 30 | read→write pairing window (minutes) — proximity bound within the same session assignment | report effect |
| `chain_window_hours` | 24 | usage-chain window (hours) — range in which returned-id re-returns and same-topic writes count | report effect |
| `events_retention_days` | 14 | retention days for read_events · session_starts (daily-counter policy inherited) | instrumentation pruning |
| `hot_events_retention_days` | 30 | retention days for hot_events (aligned with the dead_pct exposure guard window) | instrumentation pruning |
| `events_volume_warn` | 50000 | **defensive cap** on the combined instrumentation tables (a safety valve for mass intake — not assumed reachable in normal operation, wording corrected v0.24.0) | doctor event-volume |
| `backup_interval_h` | 24 | minimum auto-backup interval (hours) — one set with backup_keep (v0.24.0) | done auto-backup |
| `vocab_tags` | (empty) | personal tag vocabulary (comma list) — **union** with the 7 defaults; lives in the operational DB, not the repo (v0.24.0 R4) | write vocabulary warning |
| `vocab_domains` | (empty) | personal domain vocabulary (comma list) — union with the 4 defaults | write vocabulary warning |
| `session_knowledge_days` | 1 | minimum interval in days for session knowledge conversion (session-end summary deposited as pending) — **-1 disables** · 0 treated as unset (default fallback) | done · compact session knowledge conversion |
| `invalidated_prune_days` | 90 | invalidated pruning criterion days — `maintain --prune-invalidated N [--commit]` (dry-run by default) | maintain knowledge hygiene |
| `recall_event_retention_days` | 90 | retention days for `recall_events` (schema v12 append-only recall source) — `maintain --prune-invalidated [--commit]` deletes expired events alongside knowledge hygiene; this prune is the structural growth cap for the derived ranking (v1.9.1 R42-D). Ranking note: at the default `decay_rate` 1.0, only rows whose count is at most the days since their last recall drop to an effective score of 0 — high-frequency rows (count above elapsed days) retain a positive effective score even past the window, so pruning those rows can move ranking (windowed-count bound applies to rows with recent recalls, intended). The same run also removes orphaned recall events of pruned knowledge | maintain knowledge hygiene · ranking growth cap |
| `secrets_scan_row_limit` | 10000 | per-scan row cap for doctor secrets (resource defense on large DBs — partial scan is stated explicitly when reached, v0.26.1) | doctor secrets |
| `rrf_k` | 60 | RRF rank-softening coefficient for search fallback — lower gives top signals more dominance (v0.25.0) | read fallback re-ranking |
| `daily_retention_days` | 14 | retention · windowing cap for daily:* counters — single source for report/status windows and PruneDaily (v0.24.0 — consolidates 3 hardcodes) | retention · window |
| `drill_max_days` | 30 | backup-drill age reporting criterion — beyond it, `,"drill":"stale"` | maintain --check |
| `diag_hot_budget_high` | 0.8 | diagnostics (v1.2.0) layer-A self-reference — 'chronic clipping' trigger when average hot injection / `hot_budget` is at or above this (0–1) | report diagnosis |
| `diag_hot_budget_low` | 0.2 | diagnostics layer A — informativeness (headroom) floor for the same ratio (0–1, triggers at or below) | report diagnosis |
| `diag_trend_pct` | 50 | diagnostics layer A — abrupt-change criterion % for hot injection byte trend (first-half vs second-half sums over the report window, absolute value) | report diagnosis |
| `diag_dead_warn` | 10 | diagnostics layer B — dead_pct warning % (triggers at or above · see `dead_days`) | report diagnosis |
| `diag_recall_led_goal` | 50 | diagnostics layer C value default — 'default goal' % for recall-led sessions (triggers below, habit-goal label) | report diagnosis |
| `knowledge_reads_retention_days` | 14 | retention days for knowledge_reads (per-item recall history — separately overridable from read_events retention, registered v1.2.0 T5) | instrumentation pruning |
| `zero_owned_floor` | 1 | zero-hit decomposition threshold (v1.2.0 §2.2) — if active-knowledge count at the zero verdict is below this, 'unowned' (effectively not held); at or above, 'owned_miss' (held but search missed). Direct per-key read; only n>0 accepted. **Not managed by `flint tuning`** — override via the meta table directly (not in the tuning registry) | read zero_reason |
| `export_import_max_rows` | 100000 | shared row cap for export · import (excess → exit 3 — memory defense against bulk document intake, v1.4.0 T3) | export · import |
| `decay_rate` | 1 | recall decay per day (v1.7.0) — effective score = `recall_score − decay_rate × days since last_recall`, floored at 0 (whole 24h days; never-recalled rows keep their score). Ranking only, never deletes. 0/invalid falls back to 1 — decay-off tuning is not provided | hot · brief ranking · maintain --decay-audit |
| `archive_below` | 0 | archive-recommendation floor on the effective score (v1.7.0) — `maintain --decay-audit` lists decayed rows regardless; `recommend:"archive"` appears only when this is > 0 and the effective score is below it. **0 disables** recommendations (listing still works) | maintain --decay-audit |
| `survived_decay_relief` | 0 | survived decay relief (v1.14.0) — points of decay offset per survived session (a session with 2+ recalls of the item, from knowledge_reads) in the effective score `count − max(0, decay_rate × days − survived × relief)`. The relief never adds growth beyond offsetting decay (inner clamp), and the score still floors at 0. **0 disables** the relief entirely — no survived lookup is issued and output is byte-identical (opt-in; release-time invariance). With it > 0, ranking does one batched survived lookup per run | hot · brief ranking · maintain --decay-audit |
| `correction_confidence` | 0.7 | default confidence of a `write --source correction` payload that omits it (v1.11.0) — below the `conf_floor` default 0.8, so a correction lands `pending` for review; an explicitly given confidence (including 0) is always respected. An explicit `approve` promotes the pending row | write correction source default |
| `correction_daily_max` | 20 | daily cap of committed `write --source correction` accruals per UTC day (v1.11.0) — lines past the cap are quietly skipped with one warning line (exit 0, independent of the duplicate mechanism); the count source is the `daily:correction_write:<date>` flat key, bumped only by lines that actually committed | write correction daily cap |
| `gap_query_threshold` | 3 | recall-failure count of the same normalized query that accrues a gap-candidate pending row (v1.12.0) — the counter is the `daily:gapq:<hash>:<date>` flat key, so the count resets daily: 3 same-day failures = a strong signal, spread-out failures stay below. At or above the threshold on bump, one candidate accrues (existing pending same-hash rows get their summary count updated instead) | gap candidate accrual threshold |
| `gap_daily_max` | 10 | daily cap of new gap-candidate ADDs per UTC day (v1.12.0) — only new ADDs consume the cap (same-hash summary updates don't, the `correction_daily_max` convention); past the cap the accrual is quietly skipped with one warning line | gap candidate daily cap |
| `stale_score_threshold` | 70 | over-threshold marking line of the `maintain --age-audit` stale-score report (v1.13.0) — score = `round(100 × (0.7 × min(1, ttl_days / stale_ttl_days) + 0.3 × min(1, same-topic :correction: count / 3)))` (weights and the correction saturation 3 are code constants, part of the formula — only this marking line and `stale_ttl_days` are tunable). At or above, an item is marked `over_threshold`; disposition is delegated to `--invalidate` (approval required) | maintain --age-audit marking |
| `stale_ttl_days` | 180 | TTL saturation days of the stale-score formula's dominant signal (v1.13.0) — `ttl_days` is the integer days since `COALESCE(last recall, ingested_at)`; at or above this the TTL component (weight 0.7) is fully lit. Lowering it makes the score age-faster, raising it age-slower | maintain --age-audit score TTL saturation |
| `echo_top_k` | 5 | max echo candidates in a `write` response (v1.17.0) — the echo scan surfaces semantically near existing knowledge as a suggestion-only `echo` field; the cap bounds response size (the per-write scan cost is bounded by `suggest_scan_cap`, not by this key). Echo is suggestion-only — disposition is always delegated to review (no auto-supersede) | write echo top-k cap |
| `echo_min_score` | 0.3 | admission score floor of an echo candidate (v1.17.0) — deliberately above `suggest_min_score` 0.15: the write path pays a per-write scan cost, so the bar is raised to admit only strong near-matches and keep weak candidates out of every write response | write echo admission floor |
| `echo_topic_match` | 0.4 | topic-overlap floor of an echo candidate (v1.17.0) — a candidate must share at least this much topic overlap with the new write in addition to clearing `echo_min_score`, preventing cross-topic noise in the echo field | write echo topic-overlap floor |
| `weekly_rollups_retention_days` | 90 | retention days for `weekly_rollups` (schema v13, v1.19.0 — the weekly num/den counters that extend the zero_pct·fallback_pct·pair_pct baseline past the 14d daily-counter window; whole-week delete at the Monday boundary). **Interdependency**: the rollup pass aggregates the fully-elapsed week from raw `read_events` once a day (up to 1 day after the week closes, so events up to 8 days old), and it runs before the prune — an `events_*_retention_days` override below **8 days** can delete a week's raw events before the pass aggregates them, and weekly rollup completeness is then not guaranteed | rollup retention prune |
| `threshold_snapshots_retention_days` | 90 | retention days for `threshold_snapshots` (schema v13, v1.19.0 — append-only threshold-change history, changed keys only; the first expiry on an empty table seeds the full key set as the baseline, old_value empty) | snapshot retention prune |
| `metering_freshness_warn_h` | 336 | metering-health freshness warning hours (doctor metering-freshness, v1.19.0) — each of the 7 sources (5 event-table recency maxes + the 2 rollover gate stamps `last_threshold_snapshot_at`·`last_rollup_at`) older than this warns; never-recorded counts as stale, a failed probe skips quietly. 336h = 14 days, aligned with the events retention horizon (default) — a source older than the horizon means its history rolled over without one new event. Warn only, never blocks | doctor metering-freshness |

Diagnostics layer-B rules inherit reads from existing doctor keys (avoiding
duplicate keys — see the `zero_near_warn_pct` · `pending_age_days` rows).
Diagnostics only produce items when something is wrong (healthy silence —
empty array).

The diagnose `metering_silent` judgment window (30 days, v1.19.0) is a
**code constant** — no thresholds-registry key was registered for it
(the curation·maintain paths fire at most once a day, so a shorter
overridable window would invite false positives).

Update (unset keys use the default, reapplied immediately — no restart needed):

```sh
psql "$FLINT_DSN" -c "INSERT INTO meta (key, value) VALUES ('pending_max','40')
  ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value"
```

Verify: the Counts vs Thresholds comparison in the `flint maintain` report
follows the new value, and the warning criteria of `flint done --session <id>`
change accordingly.

### v1.6.2 hard intake limits (R33 G1–G5 — code constants, not meta keys)

Alongside the row cap above, v1.6.2 adds hard limits that are code
constants (not overridable via meta — they defend memory, so raising
them is a code change):

| limit | value | scope |
|---|---|---|
| per-payload byte cap | 8MiB | a single write payload field over this is rejected (exit 3) |
| import file size cap | 512MB | `import --format jsonl` files over this are rejected before read (exit 3, with max/size) |
| read suggest query truncate | 4096 runes | oversized read queries are truncated for the suggestion scan (body search unaffected) |

The stdin line cap for `write` reuses `export_import_max_rows` (import
symmetry — excess is exit 3 with max/count).

### DSN config file (config.json) — v0.7

Thresholds live in the meta table, but the DSN is overridden via a config file
(`UserConfigDir()/flint/config.json`, format `{"dsn": "..."}`). Relocate it by
setting the `FLINT_CONFIG_DIR` environment variable (the gh `GH_CONFIG_DIR` /
Dolt `DOLT_ROOT_PATH` model — the marker file lives in the same directory).

Precedence (highest first):

1. `FLINT_DSN` environment variable
2. the `dsn` key in `config.json`
3. default `postgresql://flint:flint-dev@localhost:5434/flint`

A corrupt file, empty value, or non-string `dsn` falls back to the default
(not an error — same principle as the invalid-meta-value fallback). If another
container claims port 5434 and a move is required (`flint doctor --check port`
reports the occupier), update `FLINT_DSN` or `config.json` per this precedence
and adjust the compose port mapping together.

## 3. Worked example (hypothetical numbers)

| week | hot_calls | hot_p95 | action |
|---|---|---|---|
| week 1 | 38 | 1,780 chars | observe only |
| week 2 | 41 | 1,960 chars | near the 2,000-char cap — update `hot_budget` to 2,400 |
| after update | 12 | 2,310 chars | zero clips — budget adequate |

## 4. Backup rotation (backup_keep) · encryption (backup_encrypt)

On a successful `flint backup --out`, dumps in the same directory are rotated
(v0.5 T54):

- Targets are `.sql` (plaintext) and `.sql.age` (ciphertext, v0.28.0) files;
  keep the `backup_keep` most recent **by mtime**.
- The rest are deleted, and a matching `.sha256` sidecar is removed with them
  (files without a sidecar are deleted alone). The count appears in the
  summary JSON's `removed`.
- Default **14**. If `backup_keep` is below 1 or invalid, fall back to the
  default; rotation itself guarantees a minimum keep of 1 (never deletes
  everything).
- Update (reapplied immediately — read on every backup run):

```sh
psql "$FLINT_DSN" -c "INSERT INTO meta (key, value) VALUES ('backup_keep','7')
  ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value"
```

### Backup encryption — `backup_encrypt` (v0.28.0, default false)

Opt-in. When `true`, automatic and manual file backups are age-encrypted
(X25519 key file `backup.key` under the infrastructure data root FLINT_HOME
(default ~/.flint, FLINT_HOME env override · `flint doctor --check backup-key`
shows the actual path), 0600 — auto-generated on first encryption). One-off:
`flint backup --out <path> --encrypt`.

- The key file is a secret that stays on the machine — **if lost, encrypted
  backups cannot be decrypted** (keep a separate copy in a password manager
  or similar). Threat model: preventing leaks when backup files leave the
  machine (sync · copy accidents). Whole-device theft is out of scope.
- The `.sha256` sidecar of a ciphertext is **over the ciphertext** (storage
  integrity — the plaintext is covered by age AEAD). `flint restore` detects
  the format, decrypts and replays automatically; `--drill` uses the same
  path.
- A masked dump (`flint backup --mask --out <path>`) replaces secret patterns
  with `[REDACTED:<pattern>]` — a **diagnostics/sharing-only** artifact, with
  no sidecar, so restore is blocked at the source.
- Update:

```sh
flint tuning set backup_encrypt true
```

## 5. Other meta keys

- `exposure_profile` = `normal` | `minimal` (default normal) — the
  agent-exposure reduction switch: minimal removes recall hints from the hot
  headline and suppresses nudge output entirely (markers auto-close). Set via
  `flint setup --exposure minimal` or `flint tuning set exposure_profile
  minimal` (covered by tuning since v0.24).

- `schema_version` — CLI compatibility check only (never update manually).
- `last_backup` — auto-backup decisioning only (updated by `flint backup`).
- `hot_stats` — persistent hot instrumentation accumulator (read by
  `flint status`, updated on exit).
- `brief_budget` — total output rune cap for `flint brief` (external-AI
  brief, v0.34). Unset or invalid falls back to 3,000 (same contract as
  hot_budget — lookup failure or invalid is not an error).
  **Not managed by `flint tuning`** — set it in the meta table directly
  (not in the tuning registry).

## 6. Deferred-decision recovery summary

Of the four §15 deferrals, this document recovers ① the injection budget cap
(hot_budget) and ④ threshold values. ② retention/permanent deletion and
③ backup format are decided separately after data accumulation and pg_dump
verification.

## 7. Time travel · schema v5 (v0.8)

### as-of spec forms (`flint read --as-of` · `flint status --as-of`)

| form | example | normalization |
|---|---|---|
| ISO timestamp (RFC3339 · `2006-01-02T15:04`) | `2026-10-24T09:00` | latest commit just before that time |
| relative | `72h` · `7d` | latest commit just before now-N (d = h×24) |
| hex commit-hash prefix (3+ chars) | `abc12` | the full hash that resolves to exactly one entry in dolt_log (0 or 2+ matches is an error) |

- Invalid specs (e.g. `2 days ago` · 2-char prefixes) and timestamps before
  the first commit are **exit 3** (the error message includes the first
  commit time).
- Time-travel queries are always normalized to a commit hash before running.

### Retention — keep everything up to the first commit

- as-of queries depend on dolt commits. If Dolt's GC or tablefile cleanup
  removes a referenced commit, queries at that point become impossible.
- We therefore recommend **keeping everything up to the first commit
  (initial deposit)**; when shrinking retention, confirm the first commit
  hash with `flint audit` and target only commits before it.

### Schema v5 — `superseded_by`

- One column added to the knowledge table: `superseded_by int`
  (`meta.schema_version`='5'). Promotion is performed automatically by the
  schema-gate chain that runs on every store-connected command — existing 4
  or lower promotes on the first command (downgrades are blocked by the
  gate).
- `flint maintain --supersede OLD --by NEW` records NEW's id on OLD
  (invalidation time = NEW's valid_at). Not for manual updates.

## 8. v0.10.1 constants — registry-ized as §2 meta keys since v0.16.3

Defaults and rationale for the three constants (adjust via the §2 meta-key
overrides — editing code defaults directly is an architecture violation,
corrected in the 2026-11-07 patch loop, round 3):

| constant | default | rationale |
|---|---|---|
| `backupAgeWarn` | `72h` | three backup cycles of headroom over the 24h auto-backup cadence — it does not warn through two consecutive failures; the third consecutive failure (age reaching 72h) warns |
| `suggestMinScore` | `0.15` | blocks irrelevant top suggestions — calibrated on a 2026-10-30 smoke test where the query "how to reset preferences" got "flint v0.9.6 direction" as the top suggestion |
| `conflictDupThreshold` | `0.6` | branch threshold between contradiction/duplicate candidates — same-topic summary similarity (BigramDiceMultiset · bigram-multiset Dice, lib/query — a set-based variant distinct from the BigramDice used for suggestion scores) at 0.6 or above is an existing-duplicate candidate, below is a contradiction candidate (maintain --conflict-suspects) |

Adjustment guidance:

- `backupAgeWarn` — when changing the backup cadence, keep cadence×3. If
  false positives are frequent, increase the headroom; if detection latency
  hurts, decrease it.
- `suggestMinScore` — lower it if valid suggestions go missing; raise it if
  irrelevant suggestions reappear. The suggestion line was removed (§9), so
  top suggestions cannot be inspected directly — observe via `zero_near_pct`
  in `flint report`: if zero-hit queries that have near-miss knowledge do not
  land in zero_near, the threshold is too high (lower it); if irrelevant
  knowledge lands there, it is too low (raise it).

## 9. zero_near — measuring the zero-hit silence contract (v0.14.0)

When `flint read` gets zero hits even after fallback re-search, it stays
silent with no output and exit 0 (v1.5.3; `--json` emits a single
`{"hits":0}` line — suggestion output removed: near candidates are not
exposed to the agent). Instead, the presence of near candidates is
measured internally as the `zero_near` metric and surfaced by report.

- exit 0 zero-hit — zero hits are a normal response, not an error: text
  mode is silent, `--json` returns `{"hits":0}`. exit 1 accompanied by a
  one-line error JSON is a failure.
- classification threshold — reuses the suggestion-scan `suggestMinScore`
  `0.15` as-is (no separate constant). Zero-hits whose best near score is
  0.15 or above are classified zero_near.
- interpretation — `zero_near_pct` = the share of search-quality problems
  (knowledge exists but the query misses it); `zero_pct − zero_near_pct` =
  the share of missing knowledge (knowledge that should be added). High
  zero_near_pct → inspect thresholds and indexing; low → accumulating
  knowledge is the priority.

## 10. Windowed metrics · doctor thresholds · instrumentation round-trip contract (v0.15.0)

### Windowed metrics — computed as Σ daily sums

`zero_pct` and `zero_near_pct` in `flint report` are computed from the sum of
`daily:YYYY-MM-DD` rows over the `--days` window (Σread_zero ·
Σread_zero_near / Σread, floor-truncated integer %), not from cumulative
`read_stats`. Cumulative `read_stats` can be contaminated by threshold-change
history and schema switches, so it is not used as a metric source
(insulation) — daily keys are retained by PruneDaily for only 14 days, so the
window is valid only within that range.

### doctor zero-near thresholds

`flint doctor`'s zero-near check judges from the last 7 days of daily sums:

| constant | default | meaning |
|---|---|---|
| sample floor | `10` | if Σread in the window is under 10, ok due to insufficient sample (judgment deferred) |
| warning threshold | `30`% | if Σread_zero_near×100/Σread ≥ 30, warn — suspected vocabulary mismatch, room for search-quality improvement |

Warnings never block operation (warn — not fail). The sample floor and
warning threshold are adjusted with the §2 meta keys (`zero_near_min_read` ·
`zero_near_warn_pct`).

### Instrumentation round-trip contract — single writer · merged upsert

Read-path instrumentation respects the round-trip budget (≤ 5 queries
excluding the main one):

- **Single writer** — `read_stats` is updated once by BumpReadStats alone in
  a single read-modify-write covering calls · zero · zero_near (no partial
  updates).
- **Merged upsert** — the daily row and `last_read_at` are written together
  in a single 2-row `INSERT ... ON CONFLICT DO UPDATE` (saves one round
  trip).
