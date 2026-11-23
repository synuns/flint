# flint 버전 이력 (아카이브)

flint의 전체 버전 이력(v0.1~v1.2.0) — v0.21.2(마일스톤 C③)부터
PLAN.md §14의 과거 엔트리를 여기에 보관하기 시작했고, v0.44.0(PLAN
재무장)부터 §14 전체 엔트리가 이관돼 PLAN.md는 현행 계약 2페이지만
유지한다. 이관분(v0.19.0+)은 시간순(오름차순)으로 재배열했다. 엔트리
본문은 이동 시점 그대로(무수정).

### v1.24.0 minor (2026-11-23) — curation-residual: schema v19 (kind · source_path · echo_candidates) · merge-audit · recall-audit · read --related · --why

(spec: docs/plans/2026-11-23-flint-v1240.md). The knowledge-curation
residual milestone — listing-only audits, opt-in read surfaces, and the
`kind` column landing together with its consumers (the v1.20.0
carry-over condition "consumer secured" met by the D2 counters and the
write vocabulary validation). Additive schema v19:

- **schema v19**: knowledge `kind`·`source_path` (text NULL, no default
  — old-binary compatible) and the new `echo_candidates` table
  (write-time echo candidates persisted: candidate_id·echo_id·relation·
  score·ts, indexes on candidate_id and ts — the D1 retrospective-
  measurement premise; this version accrues only, the id→supersede-
  target query is a later carry-over ruling).
- **`maintain --merge-audit [--limit M]`**: lists active-row PAIRS
  whose combined-text multiset Dice is at or above
  `conflict_dup_threshold` (the EchoScan measure, row-to-row),
  score-ordered, scan capped by `suggest_scan_cap`. A pre-run cost
  estimate is published first — pairs = min(cap,n)×(min(cap,n)−1)/2 at
  the measured µs/pair — and an over-budget estimate truncates the
  scan to the largest affordable row prefix with a reduce-the-cap
  advice (the O(n²) defense, A6 p95 observed). Listing only: disposal
  is delegated to `maintain --merge`/`--supersede` (approval required)
  — the decay-audit convention. Counter `merge_audit_pairs`.
- **`maintain --recall-audit [--limit M]`**: an ACTIVE row lists when
  its same-topic bad-recall count — invalidated·superseded rows with
  recall history (`recall_events` EXISTS, the age-audit precedent) —
  is at or above the new threshold `recall_audit_min_bad` (default 2),
  ordered bad-recall-descending. Listing only, disposal delegated to
  the approval circuit; a qualified target disposed by
  --invalidate/--supersede/--reaffirm feeds `recall_audit_disposed`
  (D4), the listing count feeds `recall_audit_listed`. No new queue —
  the criterion is query-time.
- **`read --related N`**: neighbor rows via the knowledge_links
  undirected UNION (relation label + reverse-direction marker),
  appended after the knowledge block (no interleave),
  source:"related", `--json` related_hits, cap 5, missing target
  exit 3, zero links an empty response. The default read output stays
  byte-identical (sealed regression test).
- **`read --why`**: opt-in per-row evidence appendix — the token tier
  as a "tier_name×weight" listing (the ReorderByTokenScore internals),
  the RRF 4-signal values (match·read_count·jamo·topic), rank·
  contribution, and zero-hit rows' zero_reason+near_score; one line
  per row under a total cap. Default response sealed.
- **kind·source_path writes**: `flint write` stdin takes the optional
  `knowledge_kind` (preference|fact, unset NULL — vocabulary-verified;
  the input key is separate from the rec["kind"] discriminator, the
  key-separation rule) and `source_path` (sanitized path string); both
  are excluded from content_hash, so duplicate resends stay idempotent.
  `maintain --recheck N` now also clears content_changed_at on
  matching vault_files rows (the row-connection promotion). D2
  counters `correction_preference`·`correction_fact` accrue from
  source=="correction" writes by kind (the curation group, 29 keys).
- **survived minimal (the v1.22.0 carry-over)**: `flint distill`
  listing and output rows now carry the `survived_sessions` count —
  the score switch stays a post-observation decision.
- **`survived_decay_relief` ruling**: the default stays 0 — activation
  is a meta override only, with the observational evidence recorded in
  the docs (the code is unchanged; the personal-values-in-meta
  principle).
- version sync: VERSION · plugin.json · README EN/KO badges · doclint
  01240.

### v1.23.2 patch (2026-11-23) — r102: counter digit bound · spec alignment · what-if domain filter

The v1.23.0 audit (R102) follow-ups — 3 small findings, no schema change:

- **counter digit bound (r102-1, low)**: the guarded counter upsert's
  numeric test is now `^[0-9]{1,18}$` — an oversized numeric meta text
  (26 digits, beyond int8) is the same pollution class as non-numeric
  text: it resets to '1' inside the CASE instead of overflowing
  `::bigint`, failing the batch, and unstamping the 24h gate.
- **spec alignment (r102-2, info)**: the v1.23.0 spec's two stale lines
  — a boundary fire bumps the fired counter only (prediction rows stay
  proposal-only), and the off-mode cost reads "0 queries steady-state,
  1 batched gate read per hour cold".
- **what-if p50/p90 domain filter (r102-3, info)**: the distribution
  collection skips out-of-domain `near_score` cells (outside [0,1])
  alongside NaN/Inf — a stored 5.0 no longer rides into p50/p90.

### v1.23.1 patch (2026-11-23) — r100-r101: round counter split · boundary fire bump · near_score domain guard

The v1.23.0 audit (R100·R101) fixes — 10 findings, no schema change:

- **rounds/fires counter split (r100-1, med)**: new meta
  `auto_rounds_<key>` bumps on every stamped round — fired or not —
  and `tuning status` / the accumulation-gate input read it (the old
  rounds source was the tuning_predictions count, which only sees
  fired rounds carrying a proposal — starving the rounds ≥ 14
  condition whenever fires ran ahead of proposals). Rounds ≥ 14 AND
  fires ≥ 5 are now independent inputs, as the gate intends;
  `auto_rounds_` joins metaKnownPrefixes (doctor self-reference
  hygiene).
- **boundary fire bumps fired (r100-3, low)**: a trigger fire whose
  current value already sits at the directional bound proposes no move
  and appends no prediction row (rows stay proposal-only per the
  spec), yet now bumps `fired_auto_<signal>` — the fired counter is
  defined by the trigger verdict, not by the proposal.
- **what-if NaN defense (r101-1, low-med)**: `tuning what-if` skips
  NaN/Inf cells when collecting the zero rows' near_score
  distribution, so a stored numeric NaN can no longer fail the whole
  JSON output.
- **near_score domain guard (r101-2, low)**: the registry verdict
  query's reclassification arm requires `near_score BETWEEN 0 AND 1` —
  a manually injected out-of-domain score never enters the trigger
  verdict or the reclass pool.
- **counter cast guard (r101-3, low)**: the fired/rounds bumps reset
  a non-numeric existing counter to '1' inside the upsert CASE instead
  of failing the cast, the batch, and the 24h gate stamp (a polluted
  counter previously blocked every later round permanently).
- **off-cost wording corrected (r100-2, low)**: TUNING now states off
  = steady-state 0 queries + 1 batched gate read per hour at cold
  start (the v1.23.0 "zero shadow cost" claim held only for an
  unregistered circuit); the code comment matches.
- **spec cache wording corrected (r100-4, info)**: the v1230 spec's
  "no process cache" becomes the actual 1h in-process gate cache (the
  existing hook convention).
- **success-pair directionality noted (r100-5, info)**: the TUNING
  whitelist notes a transient near_pct rise right after a downward
  apply is expected (the decrease reads from post-change events); the
  success-pair measurement definition is a v1.24.0 design item.
- **README tuning rows (r101-4, low)**: the EN/KO `flint tuning`
  command rows now list the autotune surfaces (`auto` · `what-if` ·
  `status`).
- **scope status (r101-5, low)**: release-scope §5 marks v1.23.0
  released with the R100-R101 audit in progress.
- version sync: VERSION · plugin.json · README EN/KO badges · doclint
  01231.

### v1.23.0 minor (2026-11-23) — autotune-shadow: schema v18 tuning_predictions · auto_mode policy · shadow rounds · tuning what-if/status

(spec: docs/plans/2026-11-23-flint-v1230.md). The autonomous-tuning I
milestone — **shadow only: predictions are recorded, no value is ever
applied** (the apply phase is the v1.24.0 gate). Additive schema v18:

- schema v18 (additive — v17 DBs promote on the first command): the
  `tuning_predictions` table (append-only: ts · trigger · key · cur ·
  new · pred_zero_near_pct · pred_reclass_n · sample_n · rationale,
  indexed on (ts)) plus the three `threshold_snapshots` columns
  `origin`·`trigger`·`rationale` (the B6 tuning-log follow-through —
  v1.23.0 records zero auto applies, so no origin=auto snapshot row
  exists yet; the schema and stamp path are what landed).
- B1 policy: the master switch is meta `auto_mode` (enum `off|shadow`,
  **default off** — an unconfigured deployment pays zero shadow cost;
  set via `flint tuning auto off|shadow`, the SetMetaKV idempotent
  upsert). The whitelist is the threshold-registry Auto grade
  (register-in-1-place + guard test): the initial whitelist is
  **`suggest_min_score` only** — the one classification key with an
  exact-causality source (`read_events.near_score`, schema v13); echo ·
  conflict scores are not stored, diagnostic keys are a masking vector,
  behavioral keys are a later phase — all manual.
- B3 registry (`lib/ops/autotune.go` — a code registry, deliberately
  NOT the diagnosis engine): initial row `auto_zero_near_high`
  (the auto_ prefix keeps it distinct from the diagnosis signal) →
  lowers `suggest_min_score`, safe range [0.05, 0.30], success pair
  "zero_near_pct decreases with hit_pct and the read count unchanged",
  observation window 14d. The weekly cap is carried but not executed
  (the v1.24.0 apply phase reads it).
- B2 shadow circuit (`lib/db/autotune_shadow.go`): the 4th
  RecordReadEvent tail hook (order rollup → prune → snapshot → shadow).
  Per 24h (meta `last_shadow_at`, the same gate pattern — a failure is
  silent and leaves the gate unstamped): trigger verdict (sample floor
  `zero_near_min_read`, warn ratio `zero_near_warn_pct` — thresholds
  single source) → fired counter bump (`fired_auto_<signal>`) →
  reclassification count → `tuning_predictions` INSERT → streak update
  (`auto_streak_<key>`), batched into 5 round trips on a fired round.
  Streak rules: first fire = 1; a proposal value change still hits
  while the direction holds; a non-fired round with an adequate sample
  maintains; reset only on a direction reversal or a sample-floor miss.
  Switching `auto_mode` off **keeps** streaks, fired counters and the
  gate stamp (resuming continues — nothing is discarded).
- tuning surfaces: `flint tuning what-if <key> <value>` (the
  counterfactual: reclassification count, new near_pct, p50/p90 of the
  zero rows' near_score distribution, sample count — a manual-only key
  exits 3) and `flint tuning status` (the list skeleton plus drift =
  current − default, the auto grade, and the per-rule fired · streak ·
  rounds accumulation card — the v1.24.0 apply-gate progress).
- observation-accumulation gate (the v1.24.0 start condition,
  release-scope §2 principle 3): shadow rounds ≥ 14 AND trigger fires ≥ 5 AND
  consecutive-hit streak ≥ 3 — unmet swaps the v1.24.0 order with
  v1.25.0. **A hit is round-over-round stability of the
  reclassification direction at the minimum sample — not causality**
  (the mandatory footnote; TUNING §2 · METRICS §4.10).

### v1.22.1 patch (2026-11-23) — r96-r98: rematch stamp preservation · conditions backfill · distill file cap · input guards

The v1.22.0 three-angle audit (R96·R97·R98) fixes — 12 findings, no
schema change:

- **concurrent rematch stamp erasure (r96-1, med)**: the apply's
  hash-equal CASE arm returned `content_changed_at` to NULL, so a
  parallel index loser's retry erased the winner's stamp on any
  hash-identical re-apply. The arm now keeps the stored stamp
  (`THEN vault_files.content_changed_at`); the normal flow (a
  hash-identical no-change rescan writes nothing) is unchanged.
- **"hash revert restores NULL" corrected (r96-2, med)**: A→B→A
  re-stamps — the single-column design keeps no pre-change-hash memory
  (the v1.22.0 spec contradicted itself); the recheck queue is
  append-only until the v1.25.0 link, where stamp clearing (disposition
  erase) lands. HISTORY v1.22.0 and the v1220 spec corrected in place.
- **`maintain --recheck N --conditions S --yes` (r96-3, med)**: the
  conditions backfill write path — updates the target active row's
  `knowledge.conditions` (the write-path 500-rune cap, one definition;
  empty clears the tag), records `maintenance_runs` (kind=recheck), and
  commits. conditions stays hash-excluded (hashVolatile): the row's
  content_hash never moves. Without `--conditions` the read-only report
  is unchanged, and the empty-conditions guidance now names the flag
  (`--recheck --conditions`). `--yes` is valid under `--recheck` only
  alongside `--conditions`.
- **distill file rune cap (r97-1, med)**: the aggregate insights file
  is bounded by threshold `distill_max_runes` (default 20000,
  registered in the 5 threshold spots + TUNING §2). Rows past the cap
  stay unstamped and defer to the next run ("N rows deferred" stderr
  notice); only the emitted rows stamp. ADR-0018 updated (the spec's
  파일당 rune 상한, finally wired).
- **reaffirm precheck `invalid_at IS NULL` (r96-4, low)**: the target
  precheck SELECT now carries the active single definition, so an
  active-status row with invalid_at set is named "nonexistent or not
  active" up front instead of surfacing as a rows-affected race (the
  UPDATE guard already enforced it — this is diagnostic accuracy).
- **wording corrections (r96-5/r97-2, low)**: `distill_min_score`
  comments name a raw recall count (ADR-0018 decision 3 — not an
  "effective recall score"), and 0 on the meta key is the unset
  sentinel — no-floor exists only through the `--min-score 0` flag.
- **scope carry-over noted (r97-3, low)**: release-scope §2's v1.25.0
  row now carries the v1.22.0 rulings (source_path revisit · survived
  score revisit · stamp clearing link) with a v1.22.0→v1.25.0
  dependency memo.
- **input parse guards (r98, med/low)**: `--ids` rejects duplicate
  tokens (a duplicate degenerated the reaffirm/invalidate writes into a
  rows-affected race — SQL ANY() dedups, the Go slice does not —
  re-applying and re-recording through every retry); `--id`·`--ids`·
  `--recheck` reject values past the int4 ceiling 2147483647 (pgx
  encoding of an over-range id surfaced as an internal error);
  `--min-score` rejects values above 1e9 (a MaxFloat-scale floor broke
  the count comparison's parameter encoding).
- **distill same-minute files never overwritten (r98, low)**: a rerun
  within the same minute writes `insights-<ts>-2.md`, `-3`, ... instead
  of overwriting — the first run's rows are already stamped and cannot
  be re-emitted, so the overwrite destroyed the earlier file's content
  permanently. One collision notice names both files (ADR-0018
  decision 2 updated).
- **typos (r97-4/r97-5, nit)**: ADR-0018 "쓰는 일는"→"쓰는 일은" ·
  CONTENT "≤500런"→"≤500 룬".

### v1.22.0 minor (2026-11-23) — vault-staleness: schema v17 staleness columns · age-audit source reference · reaffirm/recheck · volatility suggestions · distill

(spec: docs/plans/2026-11-23-flint-v1220.md). The vault bridge II ·
staleness-remainder milestone — additive schema v17, the age-audit
source-change reference, the reaffirm/recheck dispositions, the
volatility auto-class suggestion, the conditions tag, and the distill
insight extraction ([ADR-0018](adr/0018-distill-output.md)):

- schema v17 (additive — v16 DBs promote on the first command): three
  nullable columns, no index, no default-data change —
  `vault_files.content_changed_at` (the scan-diff change stamp), and
  `knowledge.distilled_at` + `knowledge.conditions` (the distill stamp
  and the manual recheck tag). One threshold key registers alongside
  (TUNING §2): `distill_min_score` 1.0.
- age-audit source reference (signal ⑤, v1.13.0 non-goal ruling
  lifted — reference-only, **the score itself is unchanged**: the
  distribution-seal test guards byte-identical scoring): the scan
  diff stamps changed vault rows `content_changed_at = now()` (a hash
  revert re-stamps — corrected v1.22.1 r96: the single column cannot
  detect A→B→A, and stamp clearing is deferred to the v1.25.0 link),
  and `maintain --age-audit` reports the
  stamped-row count as a `source_changed` reference column with the
  "vault source changed (recheck advised)" guidance line. The direct
  knowledge↔vault path link is deferred to v1.25.0 (no accrual
  source).
- `maintain --reaffirm (--id N | --ids 1,2) --yes`: resets
  `valid_at = now()` on the target active rows and records a
  `maintenance_runs` entry (kind=reaffirm) — the previously
  unrealized third disposition of the release-scope §5 set (approval
  regime: `--yes` required).
- `maintain --recheck N`: a read-only re-review report of one
  knowledge row — its `conditions` string and vol:* tier together,
  the disposition queue entry for conditional knowledge.
- volatility auto-class suggestion (T4): a deterministic
  topic/tags keyword mapping proposes one of the 3 dictionary tiers —
  immutable keywords (adr · decision · history — Korean 결정 · 역사
  too) → `vol:immutable`, else volatile keywords (version · path ·
  command · tool) → `vol:volatile`, else `vol:conditional`;
  multi-match resolves by the fixed priority immutable > volatile >
  conditional. `--age-audit` rows carry it as `suggested_vol`.
  Proposal-only by ruling — granting the immutable decay exemption
  automatically would collide with the approval regime, so adoption
  goes through the existing write approval path (the 3-entry
  dictionary itself is unchanged).
- conditions tag (T5): `flint write` stdin accepts an optional
  `conditions` string (≤500 runes, schema-validated) stored verbatim
  in `knowledge.conditions`. It joins `hashVolatile` (the relations
  precedent) — the same body ±conditions hashes identically, so
  duplicate resubmission stays idempotent.
- `flint distill --out DIR [--min-score N]` (T6, ADR-0018): emits
  never-distilled active rows with `recall_score >= distill_min_score`
  as ONE aggregate markdown file per run (`insights-<timestamp>.md`,
  id-sorted; `--out` is mandatory — missing/empty is exit 3). Only
  rows whose output succeeded are stamped `distilled_at = now()`
  (write-then-stamp, single-direction atomicity), so a rerun yields
  only the remainder. All emitted strings pass render sanitization
  ([flint] marker neutralized); output stays local-folder-only
  (ADR-0015 remote-write prohibition intact).

### v1.21.1 patch (2026-11-23) — r91-r94: path redaction · vault root guard · tuning wiring · scan hardening

The v1.21.0 four-angle audit (R91~R94) fixes — 15 findings
(deduplicated), no schema change:

- **scan error paths leaked the vault root (r91/r92/r93, high)**: the
  walk/read error lines embedded the absolute root and note rel paths
  (`vault scan: walk <root>` · `read <rel>`), and the underlying
  `fs.PathError` messages carried the full paths through `ErrLine`.
  Both sites now wrap path-free sentinels (`vault scan: walk: %w` /
  `read: %w` with `fs.ErrNotExist`/`fs.ErrPermission` classification
  preserved — the §3 contract restored: no absolute path in stdout,
  logs, or error lines).
- **`--vault` file/typo paths and failure persistence (r91/r93/r94,
  high)**: a pre-scan `Stat`+`IsDir` guard rejects a file (or
  nonexistent path) as usage exit 3, and the meta `vault_path` persist
  moved AFTER a successful scan — a failed or mistyped run no longer
  poisons the stored root.
- **threshold overrides were never wired (r93, med)**: `flint index`
  now loads the three v1.21.0 keys (`vault_scan_max_files` ·
  `vault_summary_max_runes` · `vault_skip_dirs`) through
  `LoadThresholds` into `vault.Options` (unset/failed loads keep the
  code-default fallback — the TUNING §2 contract).
- **NOT IN 60,000-placeholder ceiling (r93, med)**: the vanish delete
  is now `DELETE ... WHERE path NOT IN (SELECT unnest($1::text[]))` —
  one array bind parameter, no walked-set ceiling (60,001-file vaults
  previously failed the whole diff).
- **symlink escapes/dangling links indexed (r93, med)**: the walk
  skips entries with the `ModeSymlink` bit (symmetric with directories,
  which WalkDir never follows).
- **parallel index losers exited 1 (r94, med)**: `ApplyDiff` retries
  40001 serialization failures and the Doltgres XX000 constraint
  rollback (the `gateRetryBackoff` convention) — the rematch wins via
  ON CONFLICT idempotency.
- **A6 instrumentation completion (r91/r93/r94, med)**: the
  write/read exits persist `scan_stats` (a one-shot CLI process
  previously lost its samples; gated on pending samples — zero added
  round trips without a scan), the load path sanitizes negative
  calls/samples, `status` surfaces the cumulative scan row
  (`scan_echo_calls`/`scan_echo_p95_us`/`scan_suggest_calls`/
  `scan_suggest_p95_us`, appended after the vault row), and the
  `scan_stats` meta comment names the real `{calls,samples}` shape.
- **touch-only mtime refresh (r91/r94, low)**: a same-hash touch now
  updates the stored mtime/size (a dedicated touch UPDATE — excluded
  from `changed`), so rescans stop re-hashing untouched files.
- **`path.Base`/`path.Ext` on the rel path (r91, low)**: the title
  stem derivation uses the forward-slash `path` package (rel is always
  slash-normalized; `filepath` would miscut on Windows).
- **skipped counter split (r91, low)**: limit overflow (`skipped`) and
  walk→read vanish (`vanished`, a new internal counter) are separate —
  the "rerun with a higher --limit" note fires only on a real limit
  overflow.
- **title truncation (r93, low)**: the index caps the title at 200
  runes and the read output truncates identically (json and text).
- **text-mode pipe escape (r93, low)**: vault line cells replace `|`
  with a space (the 4-field `vault|path|title|summary` protocol cannot
  be forged by cell content; the SanitizeText convention).
- **vault_path change notice (r94, low)**: an explicit `--vault`
  differing from the stored root emits a path-free
  `notice` ("vault path changed — the previous snapshot is replaced")
  in the output JSON (guidance only).
- **release-scope §5 (r92, minor)**: the v1.21.0 row is
  `released` (2026-11-23) with the R91~R94 audit noted; PERF §2b A6
  records the exposure surface (status + both persist points).

### v1.21.0 minor (2026-11-23) — vault-bridge-index: schema v16 vault_files · `flint index` · read vault fusion · scan p95 metering

(spec: docs/plans/2026-11-22-flint-v1210.md). The vault bridge I
milestone — additive schema v16, the index command, the read fusion,
and the scan-latency metering:

- schema v16 (additive — v15 DBs promote on the first command,
  migration 0 for everything read-side): the `vault_files` pointer-
  snapshot table (one row per vault .md file — the original text is
  never copied; `path` UNIQUE is the scan-diff UPSERT axis;
  `content_hash` is the sha256 of the raw bytes, deliberately distinct
  from the knowledge normalized hash) with its (content_hash) index —
  the duplicate-note detection axis. An audit staging target (the
  recall_events ruling precedent — the vault index is a recall-domain
  source).
- `flint index [--vault PATH] [--limit N]`: one snapshot-diff scan of
  a markdown vault and ONE batch dolt commit per diff (a diff-free
  rerun commits nothing). The vault path is user-declared (no auto
  detection): an explicit `--vault` persists to meta `vault_path`;
  without the flag the stored path is reused, and its absence is usage
  exit 3 guidance. Scan stats round-trip through meta `vault_last_scan`
  JSON for the status vault row (`vault_indexed_at`·`vault_files`·
  `vault_changed`, appended to the non-as-of output). The vault root
  path never appears in output, commit messages, or error lines.
- read vault fusion: `flint read` appends vault rows AFTER the
  knowledge block (never interleaved — the knowledge result stays
  byte-identical with and without vault data; `hits` and the zero-hit
  `{"hits":0}` line stay on the knowledge domain). Cap 5 (SQL LIMIT +
  app-side defense in depth), `source:"vault"` + `path` identify a row
  (text mode `vault|path|title|summary`), and `--json` discloses the
  count as a separate additive `{"vault_hits":n}` line. The knowledge
  filters do not apply — the vault query is q-based only. A zero-hit
  WITH vault hits is no longer silent (the knowledge silence itself is
  unchanged). The MCP `flint_read` tool is unchanged — the fusion
  rides the existing tool.
- A6 scan p95 metering: EchoScan·SuggestSimilarWithScanMax
  entry-to-return duration samples accumulate in meta `scan_stats`
  JSON {echo,suggest}{calls,samples} (cap 500 samples per kind, p95
  derived on read — nearest-rank ceil(0.95n)-1, the HotStats pattern),
  committed at the existing hot_stats persist point (zero added
  queries on the echo/suggest paths). PERF §2b carries the synthetic
  2k-row figures.
- three threshold keys (TUNING §2): `vault_scan_max_files` 10000 (per-scan
  .md file cap), `vault_summary_max_runes` 200 (summary truncation),
  `vault_skip_dirs` `".obsidian,.trash"` (comma-separated skip-dir
  list — the first string-valued key family; unset keeps "" and the
  consumer falls back to DefaultThresholds).
- R90 (v1.20.0 carry-over): `Merge` now defers its advisory-lock
  release — a panic in the lock-held body would have leaked the lock
  (1 line; the SetSupersededRef convention; the unlock is idempotent).

### v1.20.3 patch (2026-11-22) — r89: merge/setref join the supersede lock domain

The v1.20.2 follow-up audit (r89) fix — 1 finding:

- **Merge·SetSupersededRef outside the supersede lock domain (r89,
  high)**: v1.20.2 serialized Supersede under one advisory lock, but
  the two other superseded_by writers stayed outside the domain —
  `Merge` (maintain --merge) ran its WouldCreateCycle precheck and a
  bare UPDATE through the pool, and `SetSupersededRef` (import
  --format jsonl lineage restore) did the same. A crossed pair
  `Supersede(A,B) ∥ Merge(B,A)` therefore let BOTH sides pass their
  guards and BOTH land, planting a 2-cycle in the lineage
  (deterministically reproducible, 20/20 rounds). Both now acquire the
  same supersede advisory lock (the whole supersede family is ONE
  serialization domain), execute every statement — precheck, guarded
  UPDATE, steward lookup, Merge's marker transaction — on the
  lock-holding connection itself (the r88 one-connection rule; a pool
  hop under the lock is the exhaustion deadlock), and carry the same
  NOT EXISTS cycle predicate embedded in the UPDATE as
  supersedeGuardedSQL (defense in depth). Regressions: Supersede∥Merge
  crossed 20 rounds exactly 1 winner·0 cycles, Merge↔Merge crossed,
  SetSupersededRef∥SetSupersededRef crossed, and the r87/r88 suites
  (cross-race·14-parallel-beyond-pool·500-hop depth cap) re-measured
  green on a real DB.

### v1.20.2 patch (2026-11-22) — r88: advisory-lock single-connection supersede · pam self-link skip · truncation test

The v1.20.1 follow-up audit (r88) fix batch — 3 findings:

- **advisory-lock pool exhaustion (r88, high)**: the v1.20.1 supersede
  serialization held one pooled connection for the advisory lock while
  the guarded UPDATE demanded a SECOND pool slot — N parallel
  supersedes at N ≥ pool size deadlocked (every connection held by a
  lock waiter, every UPDATE blocked on Acquire; all timed out). The
  guarded UPDATE now executes on the lock-holding connection itself
  (supersedeLockFn returns a conn-scoped exec): one connection per
  supersede, regression-verified with 14 parallel supersedes against a
  4-connection pool (all complete, zero timeouts; the cross-race and
  depth-cap contracts unchanged).
- **PAM self-referential relation (r88, med)**: a from==to pair in the
  4-vocabulary relations hit the write path's hard self-link refusal
  and failed the WHOLE `import --format pam` document (exit 1) —
  violating the partial-document contract. RestorePAMLinks now skips
  the pair with one stderr warning (the endpoint-missing convention);
  the remaining relations accrue, exit stays 0, no residual staging.
- **dead truncation defense (r88, low)**: CheckRelations' 200-rune
  rejection-echo cap was unreachable from the write path (the 64-byte
  parseLinkSpecs cap fires first). Kept as a defense layer for other
  callers and pinned by a direct-call unit test (multibyte input
  proves rune-based truncation).

### v1.20.1 patch (2026-11-22) — r84-r87: link accrual completeness · pam roundtrip · cycle race hardening · schema v15 unique link index

The v1.20.0 four-angle audit (r84–r87) fix batch — 13 findings
(deduped), link-accrual completeness first:

- **mixed-batch duplicate-link loss (r84)**: a write batch holding both
  new lines and duplicate resends accrued only the new rows'
  relations — the duplicate-path links were silently dropped (the
  flush ran only on the all-duplicate paths). Both families now ride
  the same batch commit.
- **PAM round trip (r84)**: `import --format pam` parsed the
  4-vocabulary relations but never accrued them — RestorePAMLinks now
  runs over the accrual id map, and the summary JSON carries the
  applied count (`links`). PAM temporal `created_at` (RFC3339) is also
  preserved into `ingested_at` (the jsonl-path symmetry).
- **int4 overflow·precheck silence (r86)**: a relation target id ≥ 2^31
  (legal bigserial range) died inside the `ANY($1)` array encoding —
  Doltgres describes the bare `$1` as `_int4` even against the bigint
  id column (measured) — and the failed lookup was silently treated as
  "every target exists". The lookup now sends `[]int64` with an
  explicit `::bigint[]` cast, a lookup failure propagates (no quiet
  pass), and a `to` ≥ 2^63 is refused at parse time (exit 3).
- **supersede race·depth (r87)**: the precheck→UPDATE window let two
  parallel cross supersedes (A→B ∥ B→A) both land, planting a 2-cycle
  (measured — Doltgres has no SELECT FOR UPDATE, and same-row
  transaction serialization did not conflict). Supersede is now
  serialized under one `pg_advisory_lock` with the cycle predicate
  embedded in the UPDATE itself (20-round cross race: exactly one
  winner per round). A lineage walk reaching the depth cap is a
  conservative refusal (error) — the old "safe no-cycle" verdict is
  abolished (500-hop chain tail→head: refused). `import --format
  jsonl` lineage restoration runs the same precheck (a loop-shaped
  document no longer plants its cycle through the ErrNotFound
  fallback).
- **schema v15 (additive — one index)**: `uq_knowledge_links_triple`
  on (from_id, to_id, relation) — the NOT EXISTS insert guard left a
  check-then-insert window that accrued the same link twice under
  parallel writers. v15 promotion collapses pre-seal duplicates (the
  NOT IN / MIN(id) form — Doltgres has no DELETE ... USING) and the
  INSERT is `ON CONFLICT DO NOTHING` (5 parallel writers → 1 row).
  Self-referencing links (`to == from`) and relation values over 64
  bytes are refused (exit 3), `--min-confidence NaN/Inf` is exit 3,
  and the MCP contract doc's flint_read row now names the
  query/tag/origin filters and the confidence floor it already
  shipped.
- parallel duplicate supersede double-report (r87-5): informational
  only — the data stays correct; recorded here, no code change.

(spec: docs/plans/2026-11-22-flint-v1200.spec.md — approved). Schema
v14 DBs promote to v15 on the first command (additive index, migration
0 for everything read-side).

### v1.20.0 minor (2026-11-22) — knowledge-schema-search: schema v14 knowledge links · read filters · supersede cycle guard

(spec: docs/plans/2026-11-22-flint-v1200.md). The knowledge schema and
search milestone — additive schema v14, two read filters, a supersede
cycle guard, and link portability:

- schema v14 (additive — v13 DBs promote on the first command, migration
  0 for everything read-side): the `knowledge_links` table (id·from_id·
  to_id·relation·created_at, (from_id)·(to_id) indexes) with a fixed
  4-value relation vocabulary — `refines`·`supports`·`contradicts`·
  `duplicates` (an unregistered value is exit 3; `supersedes` is
  deliberately excluded — the `superseded_by` column stays the single
  source for supersession, no dual origin) — plus a `superseded_by`
  index (CREATE IF NOT EXISTS) and `relations` registered hashVolatile
  so a links-only change never rewrites the content hash (dedup
  survives).
- read filters (migration 0): `--min-confidence N` (0..1 inclusive,
  `confidence >= N`) and `--origin agent|human` (`origin = v`) — explicit
  values only, composable with the existing filters, exposed on the CLI
  command table·usage and as MCP flint_read properties. **No thresholds
  key is consulted** (TUNING §2).
- supersede cycle guard: `maintain --supersede OLD --by NEW`·
  `--merge` and the import path (`SetSupersededRef`) trace NEW's
  superseded_by chain recursively (one WITH RECURSIVE round-trip, depth
  cap 64) and reject a supersession that would reach OLD (exit 1 with a
  diagnostic) — A→B→A is now impossible at every write site.
- `supersede_chains` view (root_id·id·depth): the supersede lineage
  unfolded per root with a path-array guard terminating the UNION ALL
  recursion (Doltgres recursion terminates on the guard, not an
  iteration-limit error); reapplyViews drops and recreates it
  (views_hash idempotence). Not exposed as-of (asOfTables unchanged).
- export_md chain notation: the 1-hop "superseded by" note now shows
  the terminal depth via the chain view (consumes the view — no
  consumer-less schema).
- PAM links portability: export carries the 4 relation kinds alongside
  the derived `supersedes`; import re-deposits them — write stdin JSON
  accepts an optional `relations` array (vocabulary-validated, missing
  target exit 3, same from·to·relation idempotent-skip, and the
  duplicate path deposits links onto the existing DupMeta.ID row so
  they are never lost).

Ruling: the `kind` column is deferred to v1.25.0 — no consumer yet, a
3-way column-name clash (rec["kind"] selector·hot_view.kind·read
--kind), and a hash change; the release-scope table is updated to
carry it forward (release-scope §2).

### v1.19.6 patch (2026-11-22) — r82: pre-flint detection schema probe · real-mode test skip

The v1.19.5 patch re-audit (R82) — a false rejection in the new pre-flint
diagnostic, and a test-infrastructure defect:

- pre-flint detection schema probe (R82-1 [mid]): v1.19.5 rejected on the
  commit-message match alone, so a user's manual dolt_commit reusing the
  "CREATE DATABASE" label after flint writes (the flint schema existing at
  that commit) was falsely rejected exit 3 — the diagnostic claimed "no
  flint schema exists there" while the schema existed. Binding the
  committer was measured infeasible: the auto "Initialize data repository"
  commit carries "Dolt System Account", but the auto "CREATE DATABASE"
  commit carries the CONNECTING USER — the same value a manual commit
  gets. The verdict is now a 1-round-trip schema-existence probe
  (knowledge AS OF the resolved hash): the 2-label exact match stays as
  the probe trigger only, the measured absence shapes ("table not found"
  base shape · the view "references invalid table(s)" shape) map to the
  exit 3 diagnostic, a probe success adopts normally (the manual
  same-label case), and any other probe error propagates as-is. The as-of
  path is user-explicit and label commits are rare — a normal flint commit
  never probes (the no-extra-round-trip claim survives for them).
- real-mode test skip (R82-2 [low]): wire thresholds_test's thresholdsStore
  swapped the fake for the real store under SUPERLITE_REAL=1, silently
  discarding the fixture's meta overrides — TestLoadThresholdsFallback was
  permanently red in real mode. The fake-meta-map + injected-failure
  fixtures are fake-only; the test now skips in real mode.

### v1.19.5 patch (2026-11-22) — r81: as-of pre-flint commit diagnostic (exit 3)

The patch-loop closeout finding (R81 [minor]) — a fresh-DB as-of resolved to
a Doltgres auto-initial commit:

- as-of pre-flint commit diagnostic (R81 [minor]): on a fresh DB (before
  the first flint write) the only commits are the 2 Doltgres automatic
  initial ones ("Initialize data repository"·"CREATE DATABASE"), and an
  as-of spec resolving to them (0h, an initial-commit prefix, or an ISO
  after them) reached the as-of view query at a point where no flint
  schema/views exist — the raw `View 'DB.active_knowledge' references
  invalid table(s)` error exited 1. Both normalization paths (time and
  prefix) now reject a resolved auto-initial commit up front with the
  pre-flint diagnostic (ErrInvalidAsOf, exit 3 — "use an as-of at or after
  the first flint write"). The check reads the message of rows LogEntries
  already returns: the Prefix·BeforeOrAt dolt_log queries widened from
  hash-only to the 4 columns — no extra round-trip. Exact-match on the 2
  observed labels only (an unrecognized manual commit keeps the normal
  path — no new false rejections). [Superseded in v1.19.6 R82: the label
  match alone falsely rejected a manual commit reusing a label — the
  verdict now requires the schema-existence probe.]

### v1.19.4 patch (2026-11-22) — r79-r80: asof day pre-multiply guard · ceiling hint reachability · gate meta key registration

The v1.19.3 re-audit (R79·R80) closeout — the last asof wrap hole, the
ceiling hint's reachability, and a doctor self-reference (no schema
change):

- asof day pre-multiply guard (R79-1 [med]): ParseRelative's `v *= 24`
  could wrap int64, and the wrapped value slipped past every later check
  (a wrapped negative is not > ceiling) — `--as-of
  9223372036854775807d` wrapped to a −24h FUTURE time (exit 0, newest
  commit) and `768614336404564651d` to +8h. A day count past
  maxRelativeAsOfHours/24 (106751d) is now rejected BEFORE the multiply
  (ErrInvalidAsOf, exit 3).
- ceiling hint reachability (R79-2=R80-2 [low]): a past-ceiling Nh spec
  (4000000h) that failed relative parsing fell through to the hash-prefix
  step (lowercase-alphanumeric) and misreported the ceiling rejection as
  "no commit matches prefix". A [0-9]+[hd]-shaped spec now returns the
  relative error itself — the ceiling wording reaches the CLI (exit 3).
- gate meta key registration (R80-1 [minor]): doctor's meta-hygiene
  warned its own gate stamps as unknown keys. last_events_prune_at (the
  db read-cleanup 24h gate — a pre-existing false positive) is
  registered, and last_threshold_snapshot_at·last_rollup_at are derived
  from meteringFreshnessGateKeys (one list, two consumers — no duplicate
  to drift).
- HISTORY precision (R79-3 [info]): the v1.19.3 hour/minute span clamp
  covered 7 sites across 6 keys (the unification site included — not
  "6 sites").
- RUNBOOK rollback constraint (R80-4 [info]): a pre-v1.19.0 binary
  rejects `status` on a v13 schema DB (read/write work) — the schema
  upper-bound check; a downgrade needs a schema-matched binary.

### v1.19.3 patch (2026-11-22) — r77-r78: asof ceiling rejection · hour/minute span clamp (7 sites, 6 keys) · history precision

The v1.19.2 re-audit (R77·R78) closeout — the overflow class cleared
from the remaining hour/minute-unit sites, asof included (no schema
change, override-space defenses only):

- asof relative-spec ceiling rejection (R77-1=R78-1 [med]): `--as-of
  Nh/Nd` past the Duration ceiling (2,562,047h) wrapped
  `time.Duration(v)*time.Hour` negative → a FUTURE as-of time, which
  commitAtOrBefore silently resolved to the newest commit (exit 0
  with a meaningless verdict). ParseRelative now rejects past-ceiling
  values as ErrInvalidAsof (exit 3 — the asof error convention);
  106751d is the largest day form that still fits.
- hour/minute span clamp — db.HoursSpan/MinutesSpan (R78 [low]): the
  6 hour/minute override keys (stall_hot_h·stall_write_h — maintain's
  stall warnings and doctor's stall grace, backup_age_warn_h,
  chain_window_h·pair_window_min, backup_interval_h) carried the same
  wrap risk — an override past the ceiling flipped every elapsed
  verdict (always-stall, always-due, never-chain). All now share
  db.HoursSpan/MinutesSpan (lib/db/retention.go — [0, 87600h] clamp,
  the v1.19.1 metering_freshness_warn_h precedent, whose inline clamp
  is unified here too).
- TUNING §2 precision (R77-2 [low]): the past-ceiling rejection list
  is maintain's --days·--expire-pending N·--prune-invalidated N —
  report --days clamps (the safe-cutoff path).
- HISTORY precision (R77-3·R77-4 [low]): the v1.19.2 sweep cleared
  every remaining day-count site (13, not 12 — recounted); the
  v1.19.1 latest-per-key read is footnoted as superseded by the
  v1.19.2 JOIN (the index path was missed).

### v1.19.2 patch (2026-11-22) — r75-r76: cutoff overflow sweep (13 sites) · snapshot latest-per-key join · doctor corrupt display

The v1.19.1 re-audit (R75·R76) hardening — the same overflow class
cleared from every remaining day-count site, plus the measured
snapshot-query regression (no schema change, all override-space
defenses):

- cutoff overflow sweep, read side (R75 [high]): the v1.19.1 clamp
  covered the 3 prune circuits only; the same naive
  `time.Duration(days)*24h` arithmetic survived at 13 day-count
  sites (stale listing·invalidation, expire-pending listing, report
  stale·dead·window filters, drill staleness, doctor pending age,
  pending verdicts, session-label max-age, session-knowledge gate) —
  a days override past the ceiling wrapped the cutoff into the
  future (a full active-knowledge sweep). All now share the exported
  `db.DaysCutoff`/`DaysSpan` (the retention-cutoff clamp —
  [0, 106751], AddDate calendar arithmetic), and maintain's value
  flags (--days·--expire-pending N·--prune-invalidated N) reject
  past-ceiling values up front (usage-class exits).
- snapshot latest-per-key JOIN (R76 [med]): the v1.19.1 correlated
  max(ts) subquery missed the (key, ts) index path on Doltgres — the
  5,000-row history ran at seconds (2,800x the full read it had
  replaced; every row re-ran the per-key max). One GROUP BY max +
  join-back per key — measured linear (milliseconds at 5,000 rows).
- doctor gate-key parse failure (R76 [low]): a present-but-unparseable
  gate value (a leftover claim token, a corrupted stamp) reports
  `(corrupt)` — distinct from `(never)`, which claimed an observation
  gap where an observation exists.
- doctor detail clamped value (R76 [low]): the metering-freshness
  ok/warn detail reports the clamped warnH (87600h), not the
  pre-clamp override — the printed limit is the limit applied.
- TUNING §2 documents the 106751-day retention ceiling.

### v1.19.1 patch (2026-11-22) — r71-r74: retention overflow clamp · future-stamp guard · seeding pre-claim · snapshot latest-per-key

The v1.19.0 four-angle audit (R71·R73·R74) hardening — 7 fixes, no
schema change, all override-space defenses (defaults and tuning
guidance untouched):

- retention-days overflow clamp (R71 [high]): a retention-days meta
  override past the int64 Duration ceiling (106751 days) wrapped the
  naive `time.Duration(days)*24h` cutoff into the FUTURE — deleting
  the whole table on the next pass. All three circuits (snapshot·
  rollup·event prune) now share `retentionCutoffDays` (lib/db/
  retention.go): days clamped to [0, 106751] (0·negative → default),
  cutoff via `AddDate` — calendar arithmetic, no Duration wrap.
- future-dated gate stamp guard (R71 [med]): a future-dated
  last_*_at stamp (a clock defect) held the 24h gate closed for its
  whole forward span (`now.Sub(t)` ran negative). All three gate
  circuits (snapshot·rollup·event prune) treat a future stamp as
  immediately due (`gateStampDue`), and doctor's metering-freshness
  reports a future source as stale (`<label> future`) instead of
  masking it.
- concurrent first-seeding pre-claim (R73 [med]): the stamp-last
  design let N processes passing an open gate together each seed the
  empty table's baseline — a doubled full key set. The gate stamp now
  runs BEFORE the pass as a commit-then-verify claim: each passer
  writes a unique claim token, settles briefly (the once-a-day path),
  then a token-survival SELECT admits only the burst's last committer
  — Doltgres commits same-row concurrent claims LWW and reports
  RowsAffected=1 to every passer (`INSERT ... DO NOTHING` included),
  so an affected-row check is not a verdict. The winner finalizes
  token→stamp; a failed pass rolls back so the retry stays open
  (stamp-only-on-success kept); a leftover token self-heals
  (unparseable → due).
- snapshot latest-per-key read (R73 [low]): lastThresholdSnapshots
  now selects each key's max-ts row in the DB (a correlated max
  subquery — Doltgres lacks DISTINCT ON; the (key, ts DESC) index
  serves the path) instead of reading the whole append-only history
  ts-ascending into the map (superseded by the v1.19.2 JOIN — the
  index path was missed).
- metering_freshness_warn_h clamp (R74 [low]): a warn_h override past
  the Duration ceiling (~2562047h) wrapped the limit negative and
  flagged every source stale; it now clamps to 87600h (10 years).
- RecordUsageCount bulk cap (R74 [low]): a count past 2^31 is quietly
  ignored (no error propagation, counter untouched — the quiet path).
- near_score clamp (R74 [low]): RecordReadEvent clamps the score into
  [0,1] on the write boundary (NaN·±Inf → 0, negative → 0, >1 → 1) —
  PostgreSQL would otherwise store NaN and the infinities, poisoning
  the min_score boundary analysis.

### v1.19.0 minor (2026-11-22) — observability-foundation: schema v13 metering layer

(spec: docs/plans/2026-11-22-flint-v1190.md). The observation-based
metering layer — additive schema v13, three independent circuits, and
two health surfaces. Metric definitions live in METRICS.md (the single
documentation source); tuning keys in TUNING.md §2.

- schema v13 (additive — v12 DBs promote on the first command): the
  read_events.near_score column (the scan-best bigramDice over ALL
  zero-hit-path candidates, recorded before the min_score filter — hit
  rows stay 0, scan failure keeps 0), the threshold_snapshots table
  (append-only threshold-change history: ts·key·old_value·new_value,
  a (key, ts DESC) index, changed keys only), and the weekly_rollups
  table (weekly num/den counter pairs, PK (week_start, metric), metrics
  zero_pct·fallback_pct·pair_pct — no derived ratios stored, computed
  at consumption).
- independent rollover circuits (both once-a-day: a 24h meta gate +
  1h in-process cache, stamped only on success): the threshold
  snapshot pass (the first expiry on an empty table seeds the full key
  set as the baseline, old_value empty) and the weekly rollup pass
  (the fully-elapsed UTC Monday week only, never backfilled; an empty
  week writes no row; re-aggregation is an idempotent UPSERT; runs
  BEFORE the event prune so a week's raw events are never cleaned
  first).
- doctor metering-freshness (7 sources): 5 event-table recency maxes
  (read_events·recall_events[recalled_at]·hot_events·knowledge_reads·
  session_starts) + the 2 rollover gate stamps
  (last_threshold_snapshot_at·last_rollup_at), each judged against
  metering_freshness_warn_h (default 336h = 14 days, aligned with the
  events retention horizon; never-recorded counts as stale; a failed
  probe skips quietly — warn only).
- diagnose metering_silent (coverage): the unified observation-kind
  registry (24 keys, 3 groups — usage 10·curation 11·bytes 3) minus
  the report window sums; registered kinds with a zero total over the
  30d window (a code constant, no thresholds key) are dead-wiring
  candidates — informational, group-qualified names.
- thresholds registry +3 keys: weekly_rollups_retention_days (90) ·
  threshold_snapshots_retention_days (90) · metering_freshness_warn_h
  (336). Interdependency: an events_*_retention_days override below
  8 days prunes a week's raw events before the once-a-day rollup pass
  can aggregate them — weekly rollup completeness is then not
  guaranteed (TUNING §2).

### v1.18.3 patch (2026-11-21) — r69-grammar: comment article fix

### v1.18.2 patch (2026-11-21) — r68-hygiene: stale comments · HISTORY body · test panic guard

### v1.18.1 patch (2026-11-21) — r67-schema: flint_read outputSchema object wrapper (results) fix

- [x] readOutputSchema array→object{results,hits,error} — structuredContent 실조립({results:[...]} 래핑·zero-hit {hits:0}·에러 {error})과 정확 부합. MCP 사양 MUST 준수(검증 클라이언트 전면 거부 해소). 테스트 갱신 동반.

### v1.18.0 minor (2026-11-21) — mcp-structured: structured tool outputs · output schemas

(spec: docs/plans/2026-11-21-flint-v1180.md). The MCP server speaks the
2025-06-18+ structured result convention while keeping the plain-text
content for old clients.

- protocol version negotiation: the supported set is
  {2024-11-05, 2025-06-18, 2025-11-25} — a request inside the set is
  echoed back, outside it the server answers with its latest
  (2025-11-25, spec MUST). An absent or non-string protocolVersion keeps
  the legacy 2024-11-05 fallback (backward compatibility).
- outputSchema (T1): all five tools declare an output schema (JSON
  Schema 2020-12) against the ACTUAL outputs — write
  {status,hash,state,id}, approve {status,id}, pending
  {status,items,counts,next}, read an array of row objects
  {id,topic,summary}, hot minimal meta only (non-JSON text).
- structuredContent (T2): tools/call keeps the content text (spec
  SHOULD) and adds a structuredContent object when the output parses as
  a single JSON object (write/approve/pending responses, the read
  zero-hit {"hits":0}); JSONL outputs are parsed per line and wrapped as
  {results:[...]}. Non-JSON outputs (hot) and parse failures omit the
  field; the isError mapping is unchanged.
- read --json (T3): the flint_read tool assembles --json internally —
  the MCP read path always returns machine-readable rows, structured as
  {results:[rows]} (a single row is wrapped too; error lines stay
  bare objects).
- rulings: elicitation excluded (single local user, no user-query flow
  inside tools/call — revisit when a use case appears); Doltgres read
  LIKE stays (pg_trgm still unsupported in the 1.0 release, confirmed
  2026-11 — PERF.md §3).

### v1.17.2 patch (2026-11-21) — r65-dedup: correction resend dedup restored

(R65 audit finding). The v1.17.1 origin default injected "correction"
into the payload BEFORE HashCanonical — the changed hash input made an
identical correction payload resend (against a row stored before
v1.17.1, whose hash carries the Normalize "agent" fallback) judge new
(ADD) instead of duplicate.

- the correction origin stamp moved AFTER the content-hash computation
  (applyCorrectionOrigin — the gapCandidateHash convention): the hash
  input keeps the pre-v1.17.1 shape while the stored origin stays
  "correction" (the approve promotion counter key). An explicitly given
  origin is respected.
- slow e2e additions: the correction resend duplicate ruling (against a
  pre-v1.17.1-shaped row) and the gap accrue → approve → gap_approved
  counter funnel.

### v1.17.1 patch (2026-11-21) — r64-observability: write-source origin tracking fix · echo doc/cost cleanup

(R64 audit findings). The v1.17.0 promotion counters were dead on their
primary path: correction·gap accruals never set `origin`, so the
Normalize "agent" default won and approve keyed nothing.

- origin tracking fix: `write --source correction` defaults a
  missing/empty origin to "correction" (an explicit origin is
  respected — the confidence convention) and the gap-candidate accrual
  stores origin "gap". The approve-time correction_approved·
  gap_approved promotion counters now fire on the live flow
  (write → pending → approve → daily counter). The gap candidate hash
  input stays origin-free — existing pending rows keep succeeding
  (content_hash unchanged).
- echo scan cost: the write-path echo scan rides the already-loaded
  thresholds via a scan-bound seam (SuggestScanCap·
  ConflictDupThreshold) — the per-write duplicate meta LoadThresholds
  is abolished; standalone EchoScan callers keep the meta→default
  derivation.
- docs: TUNING §2 echo_top_k rationale corrected — the key bounds
  response size; the per-write scan cost is bounded by
  suggest_scan_cap.

### v1.17.0 minor (2026-11-21) — echo: write-time contradiction detection · observability keys

(spec: docs/plans/2026-11-21-flint-v1170.md). Pipeline step 4 — the
write path reviews its own consistency: every new knowledge write
reports the near active rows back to the writing agent.

- echo scan engine (T1): `EchoScan` scores the incoming topic+summary
  (4096-rune cap) against the active knowledge (invalid_at IS NULL,
  suggest_scan_cap row cap) with the existing measures — multiset Dice
  (BigramDiceMultiset) for the duplicates band (>=
  conflict_dup_threshold 0.6), set Dice (BigramDice) plus topic Dice
  for the contradicts band (>= echo_min_score 0.3 and >=
  echo_topic_match 0.4). Top echo_top_k (5), descending, ties by id.
- write response integration (T2): the single-line committed output
  gains `"echo":[{id,topic,score,relation}]` (legacy fields verbatim —
  additive, agents ignoring it are unaffected). Exact-hash duplicates
  keep the DuplicateMeta response (no double report); the multi-line
  aggregate keeps its shape; a scan failure quietly omits the field
  (§13 — the write never breaks). The verdict stays with the agent:
  no auto-supersede (§5 approval circuit).
- threshold keys (T3): echo_top_k 5 · echo_min_score 0.3 ·
  echo_topic_match 0.4 registered (46→49 rows, TUNING §2) —
  echo_min_score sits above suggest_min_score 0.15 (the write path
  tightens the gate to save scan cost).
- observability keys (T4): correction_approved · gap_approved
  (approve-time promotion counters by origin) · survived_sessions (the
  maintain --decay-audit snapshot of ids with a survived recall —
  rides the relief-active SurvivedCounts lookup).
- registry & docs (T5): the pipeline case registry grows to 8 cases
  (echo — observation/judgment support, no metering source); the
  RUNBOOK gains the pipeline-4 echo section and the derived matrix;
  version sync VERSION·plugin.json·README EN/KO·contract row.

### v1.16.2 patch (2026-11-21) — r62-guard: highlight latest-first + EN/KO parity contract tests

### v1.16.1 patch (2026-11-21) — r61-parity: README.ko checksum guard parity · highlight latest-first order · version sync

### v1.16.0 minor (2026-11-21) — search-perf: token-scored read ranking · parallel profile collection

(spec: docs/plans/archive/2026-11/2026-11-21-flint-v1160.md). Two performance-shaped
changes on one spine: `read` results improve (not byte-identical —
spec-mandated) while `profile` output stays byte-identical to the
sequential oracle.

- read token rerank (T1): the phrase-hit rows are reranked in-memory
  before rendering by per-token tier scores — topic prefix 4 > summary
  prefix 3 > topic contains 2 > summary contains 1, plus a +1 cover
  bonus per matched token (covering more tokens beats one lower-tier
  hit); both sides are NFKC·lower·whitespace normalized. Ties keep the
  SQL order (`sort.SliceStable`). The SQL LIMIT is untouched — the
  reorder happens inside the returned window only, and the fallback
  path (`ReorderByFallbackCount`) is never re-reordered
  (double-reorder prohibition; FirstExactRank eligibility unchanged).
- parallel profile collection (T2): `collectEffect` runs in two
  errgroup phases — phase 1 issues the 5 non-dependent SELECTs
  concurrently (exposed set·active/reused count·dead ids·read
  events·kreads), phase 2 runs the post-join collectors with disjoint
  out-field ownership (cost vs recall-fate vs the two query locals);
  phase 3 (recall lead·hint·pairing·chaining) stays sequential. Error
  priority stays deterministic (count > dead > read-events) and the
  JSON output is byte-identical to the sequential oracle (regression
  test in the owning package); `-race` green.
- scratch self-check (T3): `lib/db` gains a `TestMain` that collects
  scratch DB names whose cleanup DROP failed and prints one stderr
  warning after the run — a server-residue alert, never a failure
  (exit code unchanged; the no-DSN skip contract is untouched).

Rulings: token scoring applies to `read` only — hot/brief keep their
decay-rank + budget contracts (improvement deferred). The rerank
reorders within the SQL LIMIT window only — window widening is a
non-goal (an extreme case can still miss a top row outside the
window).

### v1.15.0 minor (2026-11-21) — distribution: one-line installer · provenance attestation · security policy

(spec: docs/plans/archive/2026-11/2026-11-21-flint-v1150.md). Distribution and trust
foundations — how flint reaches users and how users verify what they got.

- One-line installer (T1): `scripts/install.sh` follows the zoxide
  pattern — `uname -s/-m` platform detection (darwin/linux × arm64/amd64),
  asset selection from `releases/latest` (tar.gz archive only — .mcpb and
  plugin zip share the name prefix), `checksums.txt` companion download,
  sha256 verification of the single downloaded asset's line (shasum
  preferred, sha256sum fallback on exit 127 only — mismatch, missing
  line, or missing tooling is fatal, no bypass path), `~/.local/bin`
  default with `--bin-dir` override, PATH guidance. Offline shell
  contract suite (fake curl/uname/shasum seam) runs in `make ci`.
- Provenance attestation (T2): `actions/attest-build-provenance` on the
  deploy job binaries (`attestations: write` permission added) with the
  existing key-signing regime kept (ADR-0014) — verification guide:
  `gh attestation verify <file> --repo synuns/flint`.
- Docs set (T3): SECURITY.md (advisory reporting path · latest-minor
  support · non-interference and data-ownership posture) ·
  CONTRIBUTING.md (setup · `make ci` local gates · docs/plans
  convention) · issue templates (bug/feature) · README install section
  rebuilt around four paths (one-liner · go install · manual checksum
  verification · Homebrew cask guide) with a text architecture diagram
  and a "why it's safe" section (EN/KO) · RUNBOOK install link.
- Rulings: Homebrew ships as **cask, not formula** (goreleaser 2026
  formula deprecation for binary releases — same sha256 trust chain as
  the manual path; tap in preparation); cosign keyless dual-signing
  rejected as redundant — the existing key signing stays.


### v1.14.1 patch (2026-11-21) — r57-hookfix: correction hook precision — the bare "다시" marker fired on casual reuse ("내일 다시 얘기하자"), so it now requires a follow-up verb (해/줘/보자/알려, optional space) · the snippet jq pass switched from per-line `-R` to whole-input `-Rs`, restoring the first-match-only fragment contract on multi-line prompts (two matched lines no longer accrue both) with the ±80-codepoint context now spanning newlines (the topic slice took the same fix)

### v1.14.0 minor (2026-11-21) — pipeline-complete: knowledge pipeline step 3 (A-plan hook-detected corrections · survived decay relief · the seven-case registry)

(spec: docs/plans/archive/2026-11/2026-11-21-flint-v1140.md). The A-plan go/no-go ruling
landed on **go**: on the synthetic reproduction suite the local filter matched
10/10 correction prompts with 0 false positives — the v1.12.0 appendix
precision criterion is met, so the hook path ships (the v1.11.0 B-plan
agent-contract path stays untouched at 0.7).

- A-plan correction hook (T2): `UserPromptSubmit` →
  `flint-plugin/hooks/correction_filter.sh` — a local filter matches the
  minimal correction-marker set (Korean·English) and accrues only the matched
  fragment (marker ± 80 codepoints, codepoint-safe slicing) via `flint write
  knowledge --source correction` at an explicit 0.4 (below conf_floor — always
  pending, same approve gate as the 0.7 path). Non-interference: a non-matching
  prompt never calls flint (zero DB writes)·any failure exits 0 silently (the
  prompt always proceeds). Kill switch `FLINT_CORRECTION_HOOK=off` (default
  on, checked before any parsing). The hook's shell contract test runs in
  `make ci` (Makefile line).
- survived signal (T3): `SurvivedCounts(ctx, st, ids)` — per-knowledge count
  of sessions with 2+ recalls of the same knowledge_id (knowledge_reads
  derivation, kind-agnostic: read·hot both count — recall exposure is the
  signal).
- survived decay relief (T4): the pure `EventEffectiveScore` extends to
  `count − max(0, rate×days − survived×relief)` (the inner clamp means relief
  only offsets decay, never adds growth; the outer max(0,·) floor stays).
  Threshold key `survived_decay_relief` (TUNING §2, thresholds 45→46) —
  **default 0 = disabled** (release-time invariance ruling; opt-in via meta
  override). At 0 the consumers skip the survived lookup entirely
  (byte-identical output); with it > 0, hot·brief ranking and
  `maintain --decay-audit` do one batched SurvivedCounts lookup per run.
  `:vol:immutable:` exemption takes precedence (an exempt row never decays,
  so survived is meaningless there).
- case registry (T1): the seven intake cases live as a single Go registry
  (lib/knowledge/pipeline.go — case·signal·source·tag·confidence·version);
  the RUNBOOK matrix is its derived rendering (doc-code drift is
  test-blocked) and source-bearing cases stay 1:1-covered against the
  sourceRegistry.
- R56 absorbed: the decay-audit row-length guard is now `len(r) < 4` — the
  v1.13.0 tags tail made column 3 load-bearing, and a 3-cell row (old-shape
  or fake row) panicked on the r[3] index under the old `len(r) < 3` guard
  (regression: TestDecayAuditShortRowGuard).
### v1.13.0 minor (2026-11-21) — age-audit: stale-knowledge scoring (2 weighted signals) · volatility tiers (`:vol:immutable:` decay exemption)

Knowledge lifecycle management (spec: docs/plans/archive/2026-11/2026-11-21-flint-v1130.spec.md).
`flint maintain --age-audit` scores active knowledge for staleness — disposition
stays approval-gated (the no-SQL-auto-verdict principle).

- Score formula (weights are code constants, part of the formula): `round(100 ×
  (0.7 × min(1, ttl_days / stale_ttl_days) + 0.3 × min(1, same-topic
  :correction: count / 3)))`. Gate ruling ① — the spec's 5 signals shrink to 2
  weighted ones (TTL elapsed days since `COALESCE(last recall, ingested_at)` ·
  same-topic correction-tag count) to prevent structural zeros on active
  candidates; bad-recall (invalidated/superseded after recall) and same-topic
  supersede history ride along as non-weighted reference columns. `:vol:immutable:`
  rows are excluded server-side (strpos).
- Gate ruling ② — no new execution flag: the report is read-only (dry-run by
  default) and its summary delegates disposition to the existing
  `maintain --invalidate` approval system. Gate ruling ③ — signal ⑤ (source
  change, origin-document hash) stays a non-goal (no source URL/path field).
- Volatility tier tags (CONTENT §4): `vol:immutable` (decay exemption —
  effective = count maintained)·`vol:conditional`·`vol:volatile` (existing
  decay). The colon format is dictionary-registered only — a user tag token
  containing a colon is refused at write (exit 3, the schema-error channel);
  near-misses like `vol:immutable2` are not exempted (no coincidental
  exemption). Decay consumers (hot·brief ranking, maintain decay-audit) go
  through `EffectiveScoreFor` — the pure `EventEffectiveScore` formula is
  unchanged.
- Thresholds 2 keys (TUNING §2): `stale_score_threshold` (default 70 —
  over-threshold marking)·`stale_ttl_days` (default 180 — TTL saturation).
  Code defaults + meta override.
### v1.12.1 patch (2026-11-21) — r54-docs: RUNBOOK knowledge pipeline step 2 section (owned_miss-only trigger·normalized-hash daily counter·thresholds·pending→approve promotion) · pipeline follow-up freshness fix (recall-failure accrual shipped in v1.12.0) · NormalizeGapQuery truncation moved after NFKC (the 80-rune bound is defined on the normalized form — a decomposed spelling no longer truncates mid-sequence and hashes apart from its composed twin) · gapq flat-key PruneDaily pruning demonstrated on the real DB (15-day-old key expired·yesterday's retained)

### v1.12.0 minor (2026-11-21) — gap-candidates: recall-failure gap candidates enter long-term memory as pending rows (knowledge pipeline step 2)

Spec requirements 1·2 merged into one mechanism (planning ruling) — recall
failures and repeated queries are the same gap signal, so the two thresholds
collapse into one (`gap_query_threshold`, default 3): a normalized-query
recall-failure count reaching the threshold accrues a pending gap candidate
(confidence 0.4, tag `gap-candidate`, source `gap` — T1–T3).

- Trigger limited to `zero_reason='owned_miss'` — unowned hits are DB-void
  noise, not a knowledge gap (research ruling).
- The counter is the `daily:gapq:<hash>:<date>` meta flat key — reset daily
  (3 same-day failures = a strong signal; spread-out failures stay below)
  and expired automatically by PruneDaily. Normalization: NFKC via
  lib/query/suggest.go, sanitized·truncated — the raw query body is never
  persisted (bump-time truncation only).
- Dedup by content_hash: existing pending rows get the failure count updated
  (no supersede); active rows are never touched (promoted knowledge stays
  intact). `gap_daily_max` (default 10) caps new ADDs per UTC day — updates
  don't consume the cap (the correction convention). As-of reads (past-snapshot
  inspection) are not a present gap — excluded from the count.
- Research basis (v112-research 2026-11-20): no external precedent found
  (clustering simplified to a normalized-query count at personal scale);
  the pending→approve promotion gate is mandatory (the Letta self-reinforcing
  trap).
- Plan A investigation (hook-based correction auto-detection — no
  implementation): recorded as the pipeline appendix in docs/RUNBOOK.md
  (v1.14 decision material).

### v1.11.2 patch (2026-11-21) — r52-reporting: dup_correction daily counter exposed on report days (dailyFlatFields·ReportDay dup_* pattern) · RUNBOOK pipeline heading restored · channel-addition checklist 4th item (report fields) · hygiene 3 (RecordDups comment·meta value=1 test assertion·README blank lines)

### v1.11.1 patch (2026-11-20) — r50-metering: register dup_correction usage kind (per-source counter was silently dropped) · channel-addition checklist

### v1.11.0 minor (2026-11-20) — correction-channel: user corrections enter long-term memory (`--source correction`)

Knowledge pipeline step 1 (draft: docs/plans/2026-11-19-flint-knowledge-pipelines.md)
— the top-priority matrix case (direct user correction) only.

- [x] T1 — source registry adds `correction` (single registry in
  lib/knowledge/write.go; usage hint·ErrInvalidSource message derived —
  no duplicate hardcoded lists).
- [x] T2 — `flint write knowledge --source correction`: payload without
  confidence defaults to 0.7 (human signal — between hook 0.5 and
  explicit manual 0.8); the `correction` tag is auto-attached (existing
  tags preserved). The 4 ops·content_hash dedup·pending→approve circuit
  are reused unchanged — corrections also start pending (no auto-active).
- [x] T3 — thresholds 2 keys: `correction_confidence` (default 0.7)·
  `correction_daily_max` (default 20 — over-limit quietly skips with a
  one-line warning). Code defaults + meta override.
- [x] T4 — plugin skill guidance (immediate deposit on noticing a
  correction — minimal extraction, no guessing, no full-utterance
  capture)·RUNBOOK pipeline step 1 section.
- Detection-path ruling: Plan B (agent contract guidance via the skill)
  — harness-agnostic·minimal implementation; quality ceiling is agent
  compliance. Plan A (harness-hook auto-detection) is a follow-up
  investigation item (privacy surface).

### v1.10.2 patch (2026-11-20) — r48-notes: remove duplicated v1.10.1 heading · highlight order · count footnote

### v1.10.1 patch (2026-11-20) — r47-counts: fix file counts in v1.10.0 notes (424, not 394; per-package counts corrected)

### v1.10.0 minor (2026-11-20) — hygiene: comments in English across 424 files · slow suite 34.5% faster · test race fixes

- [x] T1 — R46 precision fixes: HISTORY date ordering, README highlight
  ordering, delegatingFake mutex unification (+ regression), 4 vet unkeyed
  fields.
- [x] T2–T7 — comment translation to English across lib/ (424 files):
  cli 149, knowledge 58, ops 45, db+sqlfs 56, wire+query+store 68,
  render 4, tui 16, backup 17, mcp 13 (package sum 426; unique files 424
  — two files span two task waves). Meaning-preserving only — string
  literals, test data, t.Errorf messages, identifiers, and SQL untouched;
  diffs are comment-line symmetric with zero code deletions.
- [x] T8 — lib/db slow suite 34.5% faster: 72 test bodies hoisted, shared
  seeding, inheritance gates; dual_device flock leak fixed.
- [x] T9 ruling (sequence cleanup, deferred): the 143 leftover suffixed
  sequences in flint_verify are orphaned and harmless (R42 audit — no
  derived LEFT JOIN exposure); Doltgres lacks cascading sequence drops, so
  recovery is upstream-dependent (backlog; new leakage already blocked by
  v1.9.x scratch-DB isolation).
- [x] T10 ruling (dialect-gate scope, deferred): the gate keeps its
  history-query dialect scope (dolt_log, dolt_diff,
  dolt_commit_diff_knowledge, AS OF); write dialects are already
  lib/db-owned (R40-A) with zero violations today — future write-dialect
  leaks would need a gate re-check (backlog).
- [x] T11 — comment-language sealing gate
  (lib/wire/comment_language_gate_test.go): go/scanner inspects COMMENT
  tokens only, so Hangul inside string literals cannot trip it; any
  residual Hangul comment in lib/**.go fails with file:line. First run
  surfaced 155 leftovers outside the T2–T7 directories (export_pam_test
  115 et al.) — all translated, gate green.

### v1.9.4 patch (2026-11-20) — r45-hardening: test race hygiene · tuning --tui docs

- [x] pty test buffer race (R45): TestTuningTUIPtyRealRun shared one
  bytes.Buffer between the exec copy goroutines (Write) and the main
  polling loop (String) — an actual data race (go test -race,
  reproducible). Replaced with a mutex-guarded buffer (Write and String
  both under Lock).
- [x] cli fakeStore race (R45): the v1.9.3 parallel report collection
  calls Query concurrently, but the cli test fakeStore appended to its
  recording slices without a lock (TestReport* failed 19 tests under
  -race, even standalone). Query·InsertBatch·Commit recording is now
  mutex-guarded; Query runs queryFn under the same lock so test-local
  closure recording is serialized too.
- [x] tuning --tui docs (R45): TUNING.md §2 now describes the opt-in
  form — must run alone (combined args exit 3), non-terminal or
  FLINT_NO_TUI exits 3 before the form, the no-arg and subcommand JSON
  paths are unchanged (agents/scripts unaffected), and confirmations go
  through the existing set/reset standard path.
- [x] README command tables (R45): the `flint tuning` row (EN/KO) now
  notes the opt-in `--tui` form.
- [x] HISTORY wording fix (R45): v1.9.3's "threshold
  `lowHeightThreshold`" corrected to "layout constant" — a tui.go
  constant, not a tuning-key override.

### v1.9.3 minor (2026-11-20) — c-tui-perf: low-height TUI chrome · parallel report collection · opt-in tuning TUI

- [x] Low-height TUI chrome minimization (T1): on terminals under 12
  rows the pending/maintain review screens drop the selective chrome
  (non-interactive hint, separator blanks, status line) and hand the
  freed rows to the list (layout constant `lowHeightThreshold` in tui.go —
  not a tuning key);
  the q/ESC escape hint survives even at 8 rows, and layouts at 12
  rows or more are byte-identical.
- [x] Parallel report collection (T2): the four independent collect
  passes (curation·stats·counts·effect) run concurrently via errgroup
  after the daily pass; error selection keeps the sequential priority
  and JSON output is byte-identical. Synthetic wall-clock demo
  (isolated scratch DSN, 3-run median): sequential 121ms → parallel
  83ms (local Doltgres synthetic seed — environment-specific).
- [x] Opt-in tuning TUI (T3): `flint tuning --tui` opens a huh form
  (key list → value edit → set/reset) that validates through the real
  loadThresholds→ParseThresholds path and confirms via the existing
  set/reset JSON line. Adaptive entry is intentionally absent — the
  no-arg JSON path for agents and scripts stays unchanged.

### v1.9.2 patch (2026-11-19) — r43-recovery: import replay recovery · fresh-DB direct path · tuning note scope

- [x] Import replay recovery (R43): when an import run committed its
  knowledge batch but failed the recall-history replay, re-running the
  import skipped the committed rows via dedup and the recall migration
  was silently lost forever. The import now collects score-bearing
  dedup-skipped rows and replays only those with zero `recall_events`
  in the target DB — recovery without double replay (idempotent across
  chunked partial replay).
- [x] Fresh-DB schema direct path (R43): a newly created DB (schema.sql
  v12 end state) no longer takes the v11 gate detour of adding the two
  dropped columns just to drop them again in the v12 gate — the v11
  gate skips the add when `recall_events` already exists; existing v11
  DBs still get the columns (v12 backfill source preserved).
- [x] Tuning note scope fix (R43): the `recall_event_retention_days`
  ranking note over-stated the extinction condition — only rows whose
  count is at most the days since the last recall drop to an effective
  score of 0; high-frequency rows retain a positive score (so pruning
  them can move ranking). TUNING.md and the PruneRecallEvents comment
  corrected to the bounded wording.

### v1.9.1 patch (2026-11-19) — r42-hardening: jsonl recall replay · event retention · orphan cleanup

- [x] Fatal import fix (R42): rows with recall history failed the jsonl
  round-trip — the assembler still INSERTed the dropped v11 columns
  (`recall_score` · `last_recalled_at`), error 42703 rolling back the whole
  batch. The INSERT is now assembled without the dropped columns, and after
  the batch commit the recall history is replayed into `recall_events` as
  synthetic events (one per score point, same backfill formula as the v12
  migration) — recall history now travels across devices via jsonl
  export/import.
- [x] Event lifecycle management (R42): `maintain --prune-invalidated`
  now also enforces `recall_event_retention_days` (default 90, tuning
  override) — the append-only `recall_events` source gets a structural
  growth bound, independent of knowledge invalidation.
- [x] Orphan cleanup (R42): pruning invalidated knowledge deletes its
  `recall_events` · `knowledge_reads` orphans in the same transaction —
  no growth multiplication from deleted knowledge.
- [x] Small hygiene pass (no behavior change): duplicate
  v0.19.0/v1.9.0 contract-row name (`/MultiDevice` subname), fresh-DB
  schema straight to the v12 end state (no dropped-column detour), single
  `RecallAggregateSQL` definition, comment accuracy fixes.

### v1.9.0 minor (2026-11-18) — multi-device: recall append-only (schema v12)

- [x] Root-cause fix for the dual-device fatal scenarios (R38): recall
  reinforcement was an in-place `UPDATE` on the `knowledge` row
  (`recall_score` · `last_recalled_at`), so two devices recalling the
  same item produced row conflicts that surfaced as sync panics and
  last-write-wins recall loss. Recall history now lives in a dedicated
  append-only `recall_events` table (one row per recall, schema v12) —
  inserts from any device merge without touching the same row, and the
  two dropped v11 columns are the migration's whole destructive surface.
- [x] Derived ranking: hot · brief · read ranking and
  `maintain --decay-audit` · `status` derive the effective score from
  the event aggregate (`count(*)` · `max(recalled_at)` joined per
  knowledge, single definition `EventEffectiveScore`) instead of reading
  score columns — output schemas and tuning overrides (decay_rate ·
  archive_below) are unchanged.
- [x] Lossless migration (EnsureSchemaV12): existing scores are backfilled
  as **synthetic** events (one per score point, `recalled_at` =
  last-recalled or now) to preserve score continuity — these are
  continuity placeholders, not real history; the event source is exact
  from v12 onward. `DROP COLUMN` runs behind an information_schema guard
  (Doltgres lacks `DROP COLUMN IF EXISTS`).
- [x] Recall events are absorbed by the read audit commit
  (`read: recall bump`) — telemetry separation is unchanged, and events
  propagate across devices via push/pull. Dual-device regression
  (isolated DBs A·B: independent reads and writes, then push/pull) is
  exercised in the test suite as the acceptance criterion — no panics,
  event union on both sides, identical ranking.

### v1.8.1 patch (2026-11-18) — r40-hardening: QueryAsOf fallback removal · session escaping

- [x] QueryAsOf view fallback removed (R40 High): the base-table retry on
  view-incompatible errors is abolished — the original guided error is
  surfaced unchanged (parity with pre-severance behavior) and invalid-row
  exposure via silent fallback is gone. The `FallbackTable` field is
  dropped from `wire.AsOfQuery`.
- [x] `--session` LIKE escaping: literal values inlined into LIKE patterns
  (`\`, `%`, `_` metacharacters) are escaped by a shared `EscapeLike`
  helper — the read assembler's rule is reused by audit
  (backslash default ESCAPE, consistent with the dialect SQL).
- [x] Comment-only cleanups (no behavior change): dialect gate scope
  (history-query dialect only; diagnostic/write dialects out of scope),
  LogQuery Hash-mode assembly note (4-column rebuild vs the legacy
  date-only query — value-equivalent, assembly differs), AsOfQuery Where
  two-form contract (raw tail clause vs conditions-only).

### v1.8.0 minor (2026-11-18) — db-severance: Store history interface seals the Doltgres dialect

- [x] The Store interface gains the five history methods — `LogEntries` ·
  `QueryAsOf` · `DiffRows` · `DiffStat` · `DiffCommits` — with their query
  and result types in lib/wire. Every raw `dolt_log` · `dolt_diff` ·
  `dolt_commit_diff_knowledge` · `AS OF '` query moved behind those methods
  into lib/db: the Doltgres dialect is now owned by a single package and
  the core is engine-swappable. Callers (asof · read · status · audit ·
  diff) consume the interface only; HistoryStubs keeps embedded fakes
  compiling (ErrHistoryUnsupported).
- [x] Dialect closure gate (lib/wire/dialect_gate_test.go): any dialect
  string outside lib/db fails the test with a file:line report — the
  severance is permanently verified, not a one-time sweep.
- [x] Byte-equivalence refactoring: outputs, JSON schemas, exit codes,
  clamps and the telemetry commit filter are unchanged (machine channels
  frozen) — callers were switched, never rewritten.

Design detail: docs/plans/archive/2026-11/2026-11-18-flint-v1800.md

### v1.7.2 patch (2026-11-17) — R38 audit fixes: telemetry sweep · sync mitigations

- [x] Telemetry commit regression (R38 C1): CommitTelemetry's `dolt_add('-A')`
  sweep is abolished — the instrumentation sequences
  (read_events_id_seq and 3 more, existence-checked) are staged
  explicitly. Knowledge changes from audit-path tables now land in the
  audit commit (dolt_diff-verified), keeping the audit-purity contract
  of v1.7.0.
- [x] `flint audit` filters telemetry commits out of the default list
  (`NOT LIKE 'telemetry: %'`); the new `--telemetry` flag includes them
  (`--stat` follows the same rule).
- [x] sync mitigations (R38 S-series): a pull error matching
  "DoltgresHandler caught panic" prints dedicated guidance (single
  active device recommended — simultaneous multi-device use is not
  supported by the Doltgres engine; recovery via reset); `sync reset
  --to-remote <remote>` wraps `dolt_reset --hard origin/main` with a
  data-loss warning and requires `--yes`; SyncPush/SyncPull get a 30s
  context timeout; `remote add` accepts only https·http·file schemes
  (postgres:// is rejected with exit 3); a no-op push reports
  "up-to-date"; a diverged pull points to `flint sync pull`; `file://`
  urls are Doltgres-server-side paths.
- [x] Entry probe heal (R38 L1): a 42P01 failure of the schemaVersion
  probe re-syncs the session catalog with one `SELECT 1` — the first
  command on a fresh DB exits 0.
- [x] MCP stdio responses flush immediately after write (EOF-wait
  deadlock abolished).
- [x] scripts/release_gate.sh — 태그 전 VERSION·plugin.json·배지(EN/KO)·HISTORY
  항목·versions_test 계약행·docs-sync 검사. make release가 선행 의존. 첫
  실행에서 v1.7.1 항목/계약행 부재(R38-C4)를 즉시 포착 — 게이트 자체 실증.

### v1.7.1 patch (2026-11-17) — decay ranking partial-sort fix

- [x] rankRowsByEffectiveScore's sort.SliceStable swapped only rows while
  the precomputed eff array drifted — decay ranking ended partially sorted
  (effective-0 rows above effective-1; both hot/brief). Replaced with
  index sort + reorder; brief caller now assigns the returned slice
  (accompanying ignored-return bug). R36 reproduction regression included.

### v1.7.0 minor (2026-11-17) — knowledge sync · recall decay · telemetry commit split

- [x] Knowledge sync (`flint sync` — a human operator command, not agent-facing:
  machine moves · team sharing): `sync remote add <name> <url>` · `remote
  list` (the url is masked — remote endpoints are never written to any
  output or log) · `remote remove`, wrapping the Doltgres remote functions;
  `sync push <remote>` reports {status, hash}; `sync pull <remote>` runs
  fetch+merge, `--dry` stops after fetch with nothing applied. A merge
  conflict is an error JSON with exit 1 plus manual-resolution guidance
  (the dolt CLI) — automatic resolution is forbidden (destructive-change
  defense); an unregistered or unreachable remote keeps the cause and never
  exposes the url. Design detail: docs/plans/archive/2026-11/2026-11-17-flint-v1700.md.
- [x] Recall decay / reinforcement (schema v11 — additive columns
  `knowledge.recall_score` (int, default 0) · `last_recalled_at`
  (timestamptz, NULL), backward-compatible with v10 binaries): a read hit
  reinforces the row (score+1, last_recalled_at=now — bound to the
  read_events recording path); decay is scored as
  `score − days_since_last_recall` with a floor of 0 (not continuously
  recomputed); `hot` · `brief` headline ranking reflects the score with the
  existing stable sort (score DESC, ingested_at DESC — same-recency
  precedence for well-recalled entries, byte budget unchanged).
  `maintain --decay-audit` lists archive candidates as JSON (score · last
  recall · recommendation) — archiving itself stays approval-gated via the
  existing `--invalidate` (no automatic archiving).
- [x] Telemetry commit split (R33 E2): audit-path commits stage
  knowledge · projects · meta only, while the instrumentation tables
  (read_events · hot_events · knowledge_reads · session_starts) get their
  own `telemetry` commit — verified in dolt log that a knowledge commit's
  diff contains zero telemetry rows (audit purity). Telemetry commit
  failures stay quiet (the data is already committed).
- [x] Docs & CLI surface: usageOrder + sync (usage-exposed 27 + hook-only
  2), HelpSummary sync/decay-audit lines; cli.md gains the sync section
  (remote add/list/remove · masking · --dry · conflict guidance · the
  never-log-url contract) and the decay-audit · recall_score schema notes;
  TUNING.md §2 adds `decay_rate` (default 1/day) · `archive_below`
  (default 0 = disabled); README EN/KO introduce sync · decay. SKILL.md
  deliberately omits sync (human operator command — the plugin skill stays
  agent-facing).

### v1.6.2 patch (2026-11-17) — R33 four-angle audit fixes: mcp wrapper delegation · write race survival · intake limits · doc truth

- [x] MCP wrapper delegation (R33 F1–F4): the injectedStore wrapper now
  forwards optional store interfaces (Approve · DupIDByHash · usage ·
  read-event recording) so `flint_approve` works end-to-end through MCP,
  duplicate responses carry id/topic/state, and read_events source=mcp
  instrumentation is back; the manifest advertises all 5 tools; pool
  ConnectTimeout 5s fails fast on unreachable remotes (exit 2 within
  hook budgets).
- [x] Write race survival + limits (R33 E1·G1·G2): unique-constraint
  violations are classified so the losing writer of concurrent identical
  content survives with its distinct rows (parallel-write regression);
  stdin line count reuses `export_import_max_rows` and a per-payload
  byte cap (1MB) rejects oversized payloads — both exit 3.
- [x] Import/read boundaries (R33 G3–G5·F5): import file size cap
  (512MB, exit 3 with max/size), oversized read queries drop the
  suggestion scan instead of running it, import id collisions
  auto-reassign ids (round-trip idempotency), and the project `note`
  column round-trips through write/import.
- [x] Documentation truth (R33 D1–D8·F6·E3·E4·G9): restore/drill flag
  order, cron paths via `$(go env GOPATH)/bin/flint`, PERF RealSlow
  reproduction command, QUICKSTART setup wizard wording, TUNING
  non-registry key labels and the new hard limits, README badge
  footnote, SKILL topic `session[...]`, corrupt-jsonl exit 3 labeling,
  and accepted-contract notes for parallel-write empty commits (the
  reported hash references the last commit — no data loss) and
  flat-key concurrent-increment LWW loss.

### v1.6.1 patch (2026-11-17) — R31 audit fixes: maintain TUI list correctness · key atomicity · PAM roundtrip idempotency

- [x] `maintain --tui` list correctness (R31-B B1·B2): removing a row
  after an action while a filter is active now rebuilds the list with
  SetItems from the filtered source instead of an asymmetric RemoveItem —
  GlobalIndex stays correct across a second action (filter → action →
  remove → action regression); the list height is recomputed from the
  actual non-list lines with no trailing-newline overflow (View lines ≤
  terminal height).
- [x] Signing-key atomicity (R31-A A1): LoadOrCreatePAMSigningKey creates
  the key with O_CREATE|O_EXCL (EEXIST → load the existing key) —
  concurrent first runs converge on a single key.
- [x] PAM roundtrip idempotency (R31-A A2): export emits the stored
  content_hash as an additive memory metadata field and import re-derives
  it (jsonl A9 symmetry) — export → import → re-export is stable and a
  re-import reports every row duplicated.
- [x] Small closures (R31-B B3·B4·A3): the TUI suspects loader passes an
  explicit `--limit 500` (single source `ops.MaintainTUILoadLimit`) on
  all three report calls — silent truncation at the default 10/20 is
  gone; the maintain usage line now mentions `--tui` (opt-in, run
  alone); the export docs state `--out -` writes a literal file named
  `-` — there is no stdout convention.

### v1.6.0 minor (2026-11-16) — performance fixes · PAM completion · maintain TUI · parser unification

- [x] Performance (R30 findings; all numbers are synthetic benchmarks —
  50k/5.6k-row generated datasets, never production data. "Before" is
  the R30 survey baseline, "after target" is the v1.6 goal; the measured
  close-out lives in docs/PERF.md):
  `pending` topic-prefix matching rebuilt the full active scan per row
  (O(pending×active)) into a one-shot prefix-key map (O(N+M); 50k rows
  255s → under 1s target) · `import --format jsonl` dedup and INSERT
  moved from per-row lookups to a hash-array `ANY($1)` precheck plus
  chunked pgx Batch inserts with chunk-commit transactions (partial
  failure preserves the imported count; retry is idempotent via dedup;
  5.6k rows 25.6s → seconds target) and the row cap is decided by one
  line-count scan before parsing instead of a full parse (312MB peak →
  lightweight) · `write` batch dedup uses the same `ANY($1)` batching
  (50k rows 137s → seconds target). Verdict and output equivalence
  regressions ship with every fix — behavior is byte-for-byte identical.
- [x] PAM completion: export emits the spec-required owner block
  (did:key:ed25519 id) and relation fields — external `pam validate`
  perspective clean · `integrity.checksum` is verified on import
  (mismatch → exit 3, never a silent pass) · PAM spec §18 Ed25519
  signing: `export --format pam --sign` attaches a signature block
  (payload {checksum, export_id, export_date, owner_id} JCS → Ed25519;
  key auto-generated 0600 under the config dir, age conventions);
  import verifies the signature when present — failure prints one stderr
  warning and continues (spec SHOULD) · FormatPAM memory churn cut from
  876MB (50k rows) toward ≤1/4 with byte-identical output — checksum
  reproduction is the first-priority regression.
- [x] `maintain --tui` (opt-in only — no adaptive entry, the subcommand
  arguments stay authoritative): dup-suspects · conflict-suspects ·
  stale lists → detail view with a what-survives merge preview →
  m merge · k keep · i invalidate · q quit; every action delegates to
  the existing maintain subcommands (the pending TUI pattern —
  GlobalIndex · sink causes · FLINT_NO_TUI inherited).
- [x] Argument-parser unification: 12 commands move to the spec-table
  mini-parser (lib/wire/args.go — about 800 lines of hand-rolled
  parsing removed). The N9 `--` rules, value consumption, and
  repeat-flag semantics are 100% preserved — before/after equivalence
  is held by the existing command tests (no behavior change).
- [x] Small closures: `diff` usage legacy site → wire.UsageErrorf (the
  v1.5.4 omission) · `doctor --json` machine-readable output (text
  unchanged, fail → exit 1 in both) · `maintain --check` responses gain
  a `next` key — the recommended action (the pending-import convention)
  · pending TUI `e` action delegates to `maintain --expire-pending`.
- [x] docs/PERF.md (new): measurement conventions (isolated DB ·
  synthetic seed · median of 3) · the R30 baseline table · v1.6 targets
  and measured results · a reproduction guide · the known limitation
  that Doltgres has no pg_trgm, so `read` LIKE matching stays a full
  scan (deferred, not silently dropped).

### v1.5.5 minor (2026-11-16) — terminal detection fix + CLI edge completion


- [x] Terminal detection fixed: `os.Stdout.Stat()` + `ModeCharDevice`
  classifies `/dev/null` as a character device, so a redirect
  (`flint report > /dev/null`) was mistaken for a TTY and could open the
  TUI. Redirects are no longer treated as a terminal — they take the
  same non-interactive path as pipes and flags (v1.5.2 adaptive entry;
  `FLINT_NO_TUI` and explicit flags unchanged).
- [x] Usage type completed — all remaining sites: `hot` · `brief` ·
  `compact` · `nudge` unknown-flag/argument errors now report
  `"error":"usage"` with an allowed-flags hint and exit 3 (previously
  they were silently ignored as non-3 exits). Remaining sites
  without the usage type: 0 — every command is covered.
- [x] `-h` / `--help` global aliases: same output as `flint help`
  (usage line + command summary), exit 0 — dispatch-level handling
  before the command table, like `--version`/`-v`.
- [x] `write --help` guidance: `write` accepts `--help` and prints its
  usage guidance instead of treating the flag as content input.
- [x] `export --format pam` row count = published rows: the count
  reflects what was actually written (published knowledge; pending
  rows are excluded by default and never counted).
- [x] `import` extension sniffing + migration hint: `--format` is
  inferred from the file extension when omitted, and a format mismatch
  suggests the matching migration command.
- [x] `setup` port-in-use warning: when the Doltgres port (5434) is
  already occupied by another listener, setup prints a one-line stderr
  warning before `compose up` and continues (ping · migrate still
  decide the real outcome).

### v1.5.4 minor (2026-11-16) — usage type everywhere + agent ergonomics P3

- [x] Usage error type extended to all commands (v1.5.3 shipped it for
  `read` only): `maintain` · `doctor` · `report` · `audit` · `backup` ·
  `import` · `update` unknown-flag/argument errors now report
  `"error":"usage"` with an allowed-flags hint in the message (exit
  codes unchanged — unknown-flag paths stay exit 3; genuine internal
  failures keep their existing error types).
- [x] Agent ergonomics P3: `flint --version` / `flint -v` alias prints
  the same output as `flint version` (exit 0) · unknown command · bare
  `flint` / `flint help` print usage guidance (one JSON line with
  `message` · `usage` keys — no `error` key, information unchanged;
  unknown command keeps exit 3) · `read --limit` above the cap surfaces
  the clamp: `--json` emits one additive line with `limit_requested` /
  `limit_applied` (text mode unchanged) · fresh-DB `backup_keep`
  "not set — default" notice silenced (an invalid configured value
  still warns).

### v1.5.3 minor (2026-11-16) — agent CLI trust + TUI polish

- [x] Agent CLI trust (P1): `teardown` is a dry-run by default — it
  prints what would be removed and exits 0; execution requires `--yes`
  (the `maintain --invalidate` convention) · `read` rejects
  unrecognized `-`-prefixed args with exit 3 (the `--` end-of-options
  rule and positional queries unchanged) · exit unification: unknown
  command · `done` usage error · bare `maintain` usage now exit 3
  (were 1 · 0 · 1).
- [x] Agent CLI trust (P2): `read` eligible zero-hits exit 0 — `--json`
  emits `{"hits":0}`, text mode stays silent (the exit 1 silence
  exception is gone) · usage/flag errors now report `"error":"usage"`
  (with an allowed-flags hint in the message; ErrLine shape unchanged) ·
  `approve` failure messages embed the usage (`--id N
  [--supersede OLD]`).
- [x] TUI polish (backlog #133): narrow-width key hints (40-col
  abbreviated, q kept) · nonInteractiveHint wrap · `pending` filter
  matches row IDs (`#9001`) · supersede target list loads on `s` (lazy,
  loading line) instead of on entry · successful supersede removes the
  stale candidate from the target list · action failures surface the
  sink error/message on the status line (no more bare "exit 3").

### v1.5.2 minor (2026-11-16) — TUI adaptive default

- [x] TUI default for `pending` · `export` · `report`: a bare
  invocation in a terminal now opens the TUI; piped/non-terminal or
  any flag keeps the existing output (isTerminal fallback). Explicit
  `--tui` forces the TUI — non-terminal falls back to one error line
  and exit 3. `setup` behaves the same (unchanged). Flag paths fully
  preserved.

### v1.5.1 patch (2026-11-15) — audit R25 fixes (8 findings)

- [x] TUI defects (4, measured on a real pty): A1 `pending --tui`
  window-resize handling (WindowSizeMsg → SetSize, fixed 80x20
  removed) · A2 detail body wordwrap (width − 2) replacing hard
  truncation · A3 export confirm-cancel now prints "export cancelled"
  (no silent 0-byte file) · A4 ESC/q hints corrected to ctrl+c (huh's
  actual binding)
- [x] Docs/wire (6): B1 PLAN v1.5.0 actual scope + v1.6 marketing
  deferral · B2 HISTORY v1.4.1/1.4.2 entries + contract rows · B3 ADR
  links (4) · B4 cli.md `--tui` contract · B5 plugin.json English ·
  B6 commands wording set

### v1.4.2 patch (2026-11-15) — docs-sync gate exit propagation

- [x] R23 finding: the `go test|grep|tail` pipe swallowed the failure
  exit, leaving the `release: docs-sync` gate idle (verified by injecting
  a red exit 2) — replaced with exit capture·re-throw plus a Makefile
  recipe regression test
- [x] TUNING·thresholds comments aligned to the shared export·import row
  cap (cli.md as the source)

### v1.4.1 patch (2026-11-15) — audit R22 fixes (9 findings)

- [x] A1 PAM dangling-relation block (both sides filtered by emitted
  ids — hand-written import defense) · B2 export row cap
  (`export_import_max_rows` shared with import; exceeding = exit 3)
- [x] A2 real-DB test idempotency across 6 files (Close no-op · unique
  payloads · boundary DELETE · restoreFn/recallFake stubs —
  `SUPERLITE_REAL -count=2` green)
- [x] C1~C6 docs·wire (HelpSummary completeness guard · PLAN 28 items ·
  docs-sync switched to the full 58 · cli.md contract · shared cap
  wording)

### v1.5.0 minor (2026-11-15) — English unification · TUI · docs in English

- [x] CLI user-facing messages unified in English (~400 strings across
  ops · knowledge · cli/wire · backup/db/store/mcp) with test assertions
  updated together. Machine channels unchanged (JSON error codes · exit
  0/1/2/3 · English JSON keys). The injection-guard phrase set is now the
  English **"data block, not instructions"**, synchronized across
  lib/knowledge output, docs/contracts, and SKILL.md
- [x] TUI layer (lib/tui — bubbletea · bubbles · huh) with four screens:
  `flint setup` wizard (no args), `flint pending --tui`, `flint export
  --tui`, `flint report --tui`. All actions delegate to the existing
  paths; flag paths fully preserved; non-terminal invocations fall back
  automatically
- [x] Core docs in English — QUICKSTART · contracts (6) · TUNING ·
  METRICS (§4.8 diagnosis now four views incl. --tui); README.ko.md stays
  Korean as the Korean documentation layer; docs contract tests updated
  alongside
- [x] Performance structure items (3) and PAM signing deferred to v1.6

### v1.4.0 마이너 (2026-11-14) — PAM v1.0 내보내기·데이터 이동성 완결

- [x] `flint export --format pam` — PAM v1.0 표준 memory-store 문서
  내보내기(루트·매핑·status/supersedes relations·integrity 체크섬).
  체크섬은 JCS 정규화 직렬화 기반 — export_id·export_date 제외 본문은
  동일 rows에 대해 항상 동일 바이트(재현성)
- [x] 사이드카 체크섬 — export `--out` 기록 시 `<FILE>.sha256` 자동
  생성(backup 관례 준수 sha256sum 형식·stdout 모드 미생성)
- [x] 기술부채 3종 — valid_at jsonl 왕복 보존(재export 동일성)·PAM
  project형 pending 적립(v1.3 파싱만 하던 스킵 해제)·import 행 수 상한
  (export_import_max_rows 등록 기본 100000·초과 exit 3)
- [x] 실DB PAM 왕복 e2e — export→(테이블 정리 후 fresh 목표 상태에서)
  import→행 수·content·계보(supersedes 방향) 정합·체크섬·사이드카 일치
- [x] i18n·마케팅 계열은 v1.5로 이월 — docs/PLAN.md §6

### v1.3.1 패치 (2026-11-14) — 감사 R19: 발견 12건 수정

- [x] import 재이식 결함 수선 — 배치 InsertBatch 실패의 유령 성공 차단
  (exit 1·부분 성공 수 보존)·명시 id 적립 후 setval 시퀀스 동기·
  invalidated행 타임스탬프 보존(invalid_at·ingested_at·valid_at)·
  SetSupersededRef 0행 계상 제외·왕복 dedup 해시를 write 전체 키
  기준으로·파일 읽기 오류 원인(ENOENT/EACCES) 보존
- [x] PAM·export 3건 — PAM supersedes 계보 방향 정정(from=신규·
  to=구)·export --since 바인드 문자열화(Doltgres 이종 방지)·
  FormatCSV 수식 인젝션 중립화(=,+,-,@ 접두 셀 `'` 처리)
- [x] 배포물 LICENSE 동봉 — goreleaser 플러그인 zip·.mcpb 번들에
  LICENSE 포함(files 글로브 덮어쓰기 해소)·plugin.json license 필드
- [x] 문서 정합 — v1300 플랜 PAM project형 서술 정정(파싱만·적립
  스킵 — v1.4 후보)·versions_test rebase 주석 잔존 정리·export/import
  실DB 왕복 e2e 신설(재export 동일성·PAM pending 적립)

### v1.3.0 마이너 (2026-11-14) — LICENSE(MIT)·지식 export·import 마이그레이션

- [x] LICENSE 추가(표준 MIT 전문) — 라이선스 부재 최우선 해소·README
  EN/KO License 섹션 동반
- [x] `flint export` 4형식 — jsonl(전 필드 스냅샷·재이식 왕복)·md(노트
  앱 이식)·txt(grep·주입)·csv(목록 인덱스). 필터 --status
  active|all·--tag·--since(updated_at 컷오프)·--out 파일 기록(덮어씀
  경고). exit 계약 기존 준수(0 성공·2 서버다운·3 무효 값)
- [x] import 마이그레이션 — `flint import --format jsonl`(export 왕복
  재이식·content_hash 중복 스킵 멱등)·`--format pam`(PAM v1.0
  memory-store.json 수입 — pending 적립·source는 import:pam:<platform>
  기록). 기존 규칙 변환 import는 --format 유무로 경로 분기·불변
- [x] i18n·마케팅 계열은 v1.4로 이월 — docs/PLAN.md §6

### v1.2.4 패치 (2026-11-14) — 마이너 3건: MCP 효율·원천 단일화

- [x] MCP store 재사용(B4, R17 보류 해소) — 도구 호출마다 store를
  열지 않고 서버 수명 1회 open·재사용. RunWithStore 주입 변형(nil
  폴백·기존 Run 완전 동치)·mutex 가드 캐시·pgxpool 재연결 위임.
- [x] kind 원천 단일화 — validate.go kindExpect를 수출 상수
  KindExpect로 승격, MCP 계층 값 복제 제거(값 불변).
- [x] Merge 재병합 거부 메시지 상태 구분 — invalidated vs active 외
  vs active+invalid_at 손상(코너 분기, 리뷰 발견).
### v1.2.3 패치 (2026-11-13) — 감사 R17: 12건 수정

- [x] 문서 정합 4건 — PLAN 진단 플래그 서술 정정(진단은 항상
  포함 — 별도 플래그 없음)·METRICS §4.6 잔여 후보 자기모순 해소
  (A2는 회수 완료·A1은 파생 지표만 잔여로 정정)·METRICS 진단
  섹션 신설(§4.8 — 8규칙·A/B/C계층·신호→지표 교차참조·diag_* 키)·
  goreleaser 주석 잡명 정정(ci.yml deploy 잡)
- [x] docs-sync 테스트 수 표기 실측 통일(41종)·세 뷰 동일 원천 완성 —
  기본(한 줄 JSON)·--text(사람 읽기·진단 Sentence, 이번에 신설 —
  그 전엔 JSON·HTML 2뷰)·--html(대시보드 카드)이 동일 Diagnose()
  산출

### v1.2.2 패치 (2026-11-13) — 감사 R16 종결

- [x] parseFloatPositive·parseFloatOrDefault에 NaN·±Inf 거부 가드 —
  Go의 NaN 비교 특성으로 rrf_k=NaN이 폴백 점수 전체를 조용히
  무의미화하던 것 차단(프로브 실증)

### v1.2.1 패치 (2026-11-13) — 감사 R15: 9건 수정

- [x] [실증] rrf_k 오버라이드 전면 불가 수선 — 0~1 클램프 파서 공유가
  기본값 60을 전부 기각. 전용 parseFloatPositive로 양수 전범위 허용
- [x] bumpDailyFlat 주석 정직화(LWW 유실 감수·RecordUsageCount 계약
  정렬)·readDailyFlat 허구 참조 정정·kreads per-key 낡은 주석 정정
- [x] HISTORY v1.1.0·v1.2.0 엔트리 보강·헤더 v1.2.0 갱신·대시보드
  ±30% 추정 라벨·TUNING diag_* 문서 가드 확장(dual 파일 검색)

### v1.2.0 마이너 (2026-11-13) — 관측 고도화: 저장 수선·계측 확장·규칙 진단

- [x] 저장 수선 — daily 플랫 키 원자화(daily:<kind>:<date>·40001 재시기
  제거)·스키마 v10(read_events bytes·zero_reason·source·knowledge_reads
  테이블)·hot_stats blob 파생화·last_brief_at 버그 즉치
- [x] G4 정합·CRAG 제로히트 분해·토큰 회계(effect.cost p95·절약 하한·
  주입 원가·always-on/on-invoke)·검수 카운터 8종·발화 fired_* 5계열·
  G6 회상 운명(survived_pct·bad_recall_pct)
- [x] 규칙 진단 D — DiagnosisItem{signal·value·basis·action·
  tuning_key}·8규칙·3뷰 동일 원천(뷰 분열 금지)·정상 침묵
- [x] make docs-sync — 배포 전 문서 정합 게이트(계약 테스트 41종)

### v1.1.0 마이너 (2026-11-13) — 토큰 관측(G5)·마일스톤 잔여

- [x] G5 "최소 주입" 계측 — read·hot(ClampHot 후)·brief 출력 바이트
  daily 카운터(read_bytes·hot_bytes·brief_bytes) — 스키마 무변경·
  본문 미기록(개인정보 무수집). report effect.bytes·status
  hot/read_bytes_14d. METRICS G5 행·정의·금지조항(토큰 환산 배제)
- [x] legacy 세션 라벨(session-current) 폴백 제거 — 세션별 파일 단일
  경로(감사 B3·PLAN §6 마일스톤 이행)

### v1.0.13 패치 (2026-11-13) — CI 전면 수리·릴리스 파이프라인 복구

마일스톤 점검에서 발견된 누적 결함 수선(감사 외 경로):

- [x] [심각] main CI가 v0.39 이후 적색이었음 — staticcheck U1000(패키지
  분할 잔여 미사용 별칭 9종). 로컬 make ci가 lint를 포함하지 않아
  방치. 별칭 삭제 + 로컬 검증 관례에 make lint 추가
- [x] e2e 2종 — 스키마 버전 하드코딩(8/7 → LatestSchemaVersion 파생,
  v0.42 v9 미반영)·recall 경고 전역 오염(형제 테스트 금일 write —
  임계값 격리). lint 스킵으로 가려져 있었음
- [x] Release가 v0.38.0에서 중단 — deploy가 test 의존이라 lint 적색이
  25버전(v0.39~v1.0.12)의 발행을 차단. v1.0.13이 복구 후 첫 자동
  발행(중간 버전은 태그로만 존재 — 재발행하지 않음, 룰링)
- [x] CI 감시 패턴 결함 시정 — 파이프 감시가 gh 종료코드를 삼켜
  "그린" 오판했던 것(#133~#145 머지가 무검증). 이후 종료코드 직접
  확인

### v1.0.12 패치 (2026-11-12) — 다각도 감사 R13

R12 수정 전건 실DB 통과·잔여 3건 수정(누적 88건):

- [x] bridge.md 방어식 Strict 갱신(R12 문서 수정 누락분)
- [x] [실증] 중첩 allowlist 등록 잔여 평문 — 긴 항목 우선 정렬
- [x] [실DB] CAS 경합 패자 40001 → won=false(병렬 no-op 계약 정합)

### v1.0.11 패치 (2026-11-12) — 다각도 감사 R12 — v1.0.10 결함 포획

R12 3각도(정확성·보안·문서) 10건 도출·수정(누적 85건):

- [x] [치명·실DB] CompareSetMetaKV 인자 스프레드 누락 — v1.0.10의 CAS가
  전면 불능(자동 백업 0건). fake claim 주입 테스트가 못 잡은 결함을
  실DB e2e가 포획. 스프레드 수정+인자 포획 회귀
- [x] [높음·실DB] 동일 초 병렬 경합 단일 승자 붕괴 — RFC3339Nano로
  newVal 유일성 확보
- [x] [중·실증] MaskSecretsStrict 접두사 잔여 유출 — sentinel 보호가
  더 긴 실제 비밀의 패턴 접두사까지 가리던 조합 결함. 패턴 마스킹
  우선 재설계
- [x] [하·실증] [REDACTED:…] 마커 개조 방지(sentinel 보호)·살균 가드
  MaskSecrets 식별자 확장
- [x] 문서 5종 — METRICS §4.7 재번호·make ci -count=1 고정·PLAN 표
  범위·bridge.md 방어식 갱신

### v1.0.10 패치 (2026-11-12) — 감사 잔여 의사결정 배치 (켄트 백크 판정)

R1~R11 잔여 항목 전수 결정·집행(누적 75건):

- [x] [수정] 자동 백업 원자 선점(CAS — wire.CompareSetMetaKV 신설,
  병렬 세션 덤프 정확히 1회 회귀) — R2-A4 누락분
- [x] [수정] brief allowlist 등록값도 마스킹(MaskedStrict — 기기 밖
  계약 정합, dump 진단용과 구분)
- [x] [수정] CI -count=1 고정(캐시 무효 — v0.41.1 불일치 재발 방지)
- [x] [기획] PLAN — 감사 정책(재개 조건·정지 규칙·가드 설계 경계
  선언)·에라 표 감사 행·v1.1.0 legacy 라벨 제거 선언
- [x] [기획] METRICS §4.7 — status read_calls 보존창 의미 서술 정합
- [x] [기획] RUNBOOK — 실DB 격리 룰·go test 러너 표준 문서화
- [결정] restore 자체 풀 수용 종결(트랜잭션+Exec 필요 — wire.Store
  쓰기-repo-전용 계약상 정당)·doctor schema 상수 유지(v10 시점)

### v1.0.9 패치 (2026-11-12) — 다각도 감사 R10

가드 10차 폐쇄 — 지역값 전 경로 전파(누적 69건):

- [x] localStringTable(함수 지역 문자열 값)을 ②~⑤ Ident 해석에
  consts와 병합 — 지역값×Sprintf/Errorf/Builder/Join/return 6변형
  폐쇄(재프로브 실증·회귀 47케이스·오탐 0)

### v1.0.8 패치 (2026-11-12) — 다각도 감사 R9

가드 9차 폐쇄 — 같은-블록 조립 공간 소진(누적 68건):

- [x] 블록 지역 값 테이블(종자·치환 전파·체인)·지역 슬라이스 Join
  접기·HISTORY v1.0.x 헤딩 통일

### v1.0.7 패치 (2026-11-12) — 다각도 감사 R8

가드 8차 폐쇄(누적 65건):

- [x] const 형식 Ident 접기·중첩 Join 재귀 결합(재프로브 실증·오탐 0)

### v1.0.6 패치 (2026-11-12) — 다각도 감사 R7

가드 7차 폐쇄(누적 63건):

- [x] fmt 형식 리터럴 조립 접기(Errorf/Fprintf)·자기 참조 재할당 체인
  연결(재프로브 실증·오탐 0)

### v1.0.5 패치 (2026-11-12) — 다각도 감사 R6

가드 6차 폐쇄(누적 60건):

- [x] unquoteLiteral → strconv.Unquote — \xNN·\NNN 이스케이프 우회 폐쇄
- [x] += 누적 결합·fmt.Errorf/Fprintf·slices.Concat 결합 판정(재프로브 실증)

### v1.0.4 패치 (2026-11-12) — 다각도 감사 R5

R5 판정 3건 수정 — 감사 루프 누적 57건:

- [x] shQuote 주석 gofmt 안정형(make ci 레드 해소 — R2 잔류)
- [x] 가드 5차 폐쇄 — fmt.Sprint/Sprintln 공통 결합·패키지 var 슬라이스
  접기 Join·strings.Builder 연속 WriteString 결합(재프로브 실증)

### v1.0.3 패치 (2026-11-12) — 다각도 감사 R3 종결

R3 수렴 감사 5건 도출·수정 — 이후 라운드부터 도출 0 수렴:

- [x] 쓰기 가드 3차 폐쇄 — 중첩 Sprintf·상수 접기·strings.Join 결합
  판정(재프로브 3종 적발 실증)
- [x] compact stdin 8MiB LimitReader(초과 조용히 exit 0)·세션 라벨
  개행 거부(1줄 계약)·maintain --reason 플래그 흡수 가드

### v1.0.2 패치 (2026-11-12) — 다각도 감사 R2

신선 에이전트 3각도(정확성·계약·가드·보안·성능 통합) 재감사 15건 도출·수정
(R1 수정 전건 양성 확인 포함):

- [x] 훅·세션 라벨 클러스터 — binPath 셸 인용(공백 경로 훅 소실)·세션별
  라벨 파일(병렬 덮어쓰기 오염)·검증을 기록 앞으로(무효 라벨 부활 경로
  폐쇄)·훅 진입 비간섭 복원(무효 라벨 조용히)·jq 폴백 //empty
- [x] 가드 2차 우회 폐쇄 — 면제×조립 교차·Sprintf 동사 분할(재프로브 실증)
- [x] 인자 계약 — 플래그 흡수 값 가드(backup·restore·report)·report 기본
  days clamp·import 닫는 펜스 길이(CommonMark)
- [x] 성능 — prune 게이트 1h 캐시·checkSecrets thresholds 공유(왕복 -1)
- [x] 보안 — --mask 공유 덤프 allowlist 경고·adapters 17파일 session_id
  정리+정합 가드

### v1.0.1 패치 (2026-11-12) — v1.0.0 출시 후 다각도 감사 R1

6각도(정확성·계약·보안·가드 레드팀·성능·문서) 병렬 감사 32건 도출·수정:

- [x] status 스키마 허용 목록 LatestSchemaVersion 파생(v9 DB exit 3 결함)·AS OF 해시 재검증
- [x] Invalidate 재갱신 가드(invalid_at IS NULL — supersede 계보 보존)·read 경로 prune 훅 복구(24h 게이트)·BumpMetaKV 단발 멱등화
- [x] exit 3 통일(unknown flag·무효값 전 커맨드 — 표 구동 계약 테스트)·--limit/--session 정합
- [x] audit 전열 살균·doctor projects 사후스캔·tuning 값 render·hooks.json @sh+eval(재설치 경로 동반)
- [x] 가드 보강 7종(토큰화·재귀 스캔·패키지 전체·hidden 고정 — 재프로브 전 건 적발)
- [x] 게이트 순회 probe 스킵(접속당 21→6왕복)·thresholds 1회 로드
- [x] HISTORY v0.44.0·v1.0.0 엔트리 보강·RUNBOOK v9·cli.md exit 표·flint_hot session_id 제거·MCP -32600 응답

## v0.1 코어 루프 (2026-10-21 완료)
- [x] 기술검증 3건 → Doltgres 구동·스키마 → write/read/hot/status
- [x] 수동 백업(덤프) → Claude Code 플러그인(훅+스킬) → 시딩
- 시딩: 활성 프로젝트 3건(데모 게임 프로젝트·데모 유틸 프로젝트·데모 스크립트 모음) + flint 기획 결정
  일괄 + 사용자 선호(출력 스타일)
### v0.2 운영 편의 (2026-10-21 완료)
- [x] approve 정식화 / maintain 트리거 / done 임계값 경고 / 백업 자동
### v0.3 확장 (2026-10-22 완료)
- [x] flint mcp / Hermes 카탈로그·OpenClaw 어댑터 / 임계값 튜닝
### v0.4 운영 콘텐츠·계측 (2026-10-22 기획·완료)
- [x] 콘텐츠 정책 v2 구현 — CONTENT.md(유형 8종·플레이북 계약·증류 루프·어휘표) write 검증 반영:
  어휘 밖 tags/domain 경고, seed 정합 승격
- [x] 자가발전 루프 — maintain 세션에서 패턴 탐지(성공 2회·실패→성공)→플레이북 후보 제안,
  done 시 플레이북 사용 로그·성공률 기록, 성공률 기반 무효화 후보 보고
- [x] 계측 영속화 — hot 길이 통계를 meta에 누적(현재 프로세스 내 한정 결함:
  CLI 단발 프로세스라 hot_calls가 매 실행 초기화됨), p95 실측 실현
- [x] hot 예산 meta화 — 2,000자 상수 → meta hot_budget(TUNING.md 예약 키 회수)
- [x] setup/teardown 원커맨드 — §9 약정 중 미구현: docker compose up·Migrate·초기 시딩·
  플러그인 설치 안내 원커맨드
- [x] 세션 자동 요약 — done 시 sessions 테이블 자동 기록(수동 write 의존 제거)
- [x] MCP 뷰 캐시 방어 — 장수 mcp 서버 프로세스의 stale 뷰 describe 위험:
  read 경로 simple protocol 격리 또는 스키마 버전 감시·풀 재생
- [x] 보안 구조 방어(ADR-0006) — 메모리 포이즈닝·비밀 유출 대응:
  입출력 살균·비밀 패턴 스캔 경고, 백업 sha256 무결성+restore 가드, origin 필터·분포
- [x] CI·릴리스 파이프라인 — GitHub Actions(go test + make e2e, Doltgres 서비스)·
  GoReleaser·릴리스 워크플로(§12 약정 이행)
- v0.5 이연: 백업 로테이션·보존 정책 / 검색 품질(랭킹·태그 제안)
- 후보(운영 데이터 확보 후): 병렬 쓰기 충돌 실측 스파이크·복구 훈련 자동화

### v0.5 신뢰성·검색 품질 (2026-10-22 기획·완료)
- [x] mcp CLI 연결 — `flint mcp` 서브커맨드 등록+usageLine 갱신.
  검증은 실 bin/flint 프로세스 e2e(JSON-RPC 왕복) — 라이브러리 직접 호출 테스트만으론
  배선 결함이 새어감(v0.4 부채: .mcp.json이 unknown command로 즉사)
- [x] 병렬 쓰기 충돌 실측 — N=8 동시 write 프로세스: 통과기준 = exit 1(재시도 소진)
  0건 · 전량 정합 커밋(손실·중복 0). 충돌→재시도 성공률 측정치 ADR화 + e2e 회귀화
- [x] 스키마 인덱스 — 스파이크 선행: CREATE INDEX·함수 인덱스(lower) Doltgres
  지원 확인 + ~10k 행 read p95 측정. 지원·효용 입증 시 schema v3 적용,
  무효 시 "시퀀셜 스캔 수용" ADR로 종결(에이전트 기억 규모 수천 건)
- [x] 감사·롤백 루북 — `flint audit` 신설(dolt_log 최근 20, --session 필터) +
  RUNBOOK.md(오염 감지→dolt_revert 절차·복구 훈련 절차). revert 커맨드화는
  사용 빈도 입증 후(룰링 — 문서 루북 우선)
- [x] 복구 훈련 정례화 — `flint restore --drill`: 덤프를 임시 스키마에 재생·행수
  대조·maintenance_runs 기록. maintain --check에 30일 경과 보고(meta last_drill)
- [x] 백업 로테이션·보존 — meta backup_keep(기본 14): backup 성공 후 구덤프+사이드카
  삭제. "DB 행은 전 보존, 파일 덤프는 keep-N" 원칙 구분 명시(TUNING.md §15 연계)
- [x] 검색 품질(v0.5 범위 축소) — ①제로히트 계측: read 0건 비율 meta 누적·status
  노출 ②랭킹: topic 정합 > confidence > 최신 순 정렬.
  n-gram·코사인은 트리거(제로히트율 20% 초과·read_calls≥50) 도달 시 별도 이연

### v0.6 적립 가시성·데이터 유지보수 (2026-10-22, 스펙 §1·§2)

적립 가시성(§1):

- [x] 유입 계측: write/hot/read 성공 실행마다 meta 기록 — last_write_at·
  last_hot_at·last_read_at(RFC3339) + 일별 키 `daily:YYYY-MM-DD` JSON
  {"write":N,"hook":N,"manual":N,"mcp":N,"dup":N,"hot":N,"read":N}.
  14일 초과 키 자동 정리. write 소스 = `--source hook|manual|mcp`
  (기본 manual·무효값 exit 3), 플러그인 훅은 hook, MCP 경로는 mcp,
  duplicate 응답은 dup 별도 증분.
- [x] status 확장: last_* 3필드 추가(기존 필드·순서 불변, 미설정 키는 null).
- [x] stall 진단(주기+grace 이원 모델, Healthchecks.io 준용): last-seen =
  heartbeat(nodata 패턴 — 성공 신호의 부재 감지). 경고 = stall_hot_h
  (기본 48h) 초과 hot 부재·stall_write_h(기본 72h) 초과 write 부재.
  warn은 exit 0 유지.
- [x] `flint doctor`: 검사 5종 — hook-file(훅 설정·플러그인 캐시에 flint
  등록 여부, FLINT_CLAUDE_HOME 오버라이드)·db(Ping)·schema(버전 4 일치)·
  integrity(flint_knowledge_* 인덱스 3행)·last-seen(3경로 경과+grace).
  brew doctor 준용: --list-checks·--check NAME 서브셋, fail만 exit 1·
  warn은 0, fail마다 수정 명령 한 줄 제시.
- [x] `flint report [--days N(기본 7·상한 14)]`: 일별 유입(소스·dup 구분)·
  zero_pct·hot p95·pending/active/invalidated·stale 수·기간 무효화 수.
  한 줄 JSON.

데이터 유지보수(§2):

- [x] 항목 참조 계측(schema v4): knowledge.read_count int NOT NULL DEFAULT
  0·last_read_at timestamptz. read 결과·hot 주입 항목 id 전수 증분
  (BumpReads). 신규 인덱스 없음(status 인덱스 재사용·개인 규모 시퀀셜
  수용). 계측 실패는 조회를 깨지 않음(조용히).
- [x] `flint maintain --stale [--days N] [--limit M(기본 20)]`: active 중
  (last_read_at < now-N일) 또는 (read_count=0 AND ingested_at < now-N일).
  미참조 버킷(read_count=0) 구분 표시. 보고 전용·cron 친화적·exit 0.
  처리는 기존 maintain --invalidate(무효화=가역). 물리 삭제·자동
  confidence 변경 없음(§15 이연 유지).
- [x] 임계값: stale_days 기본 90(Confluence 스테일 스캔 표준·분기 리뷰
  관례)·stall_hot_h 48·stall_write_h 72 — Thresholds 확장 + meta
  오버라이드(기존 체계 재사용, TUNING.md §2).
- [x] 랭킹 tie-breaker: 기존 순위(topic 정합>confidence>최신) 뒤에
  last_read_at DESC NULLS LAST만 추가(조회시 파생·파라미터 0개·가역).
  가중 freshness 스코어는 민감성(arXiv 2509.19376 자체 보고)으로 이연 —
  트리거 read_calls≥50.
- [x] 유지보수 경로(maintain·report·doctor)는 전부 오프라인 CLI —
  핫패스 비간섭(Letta sleep-time 준거).

### v0.7 적립 자동화·실사용 재개 (2026-10-23 기획)

- [x] 자동 write 훅 — 세션 종료/컴팩트 시점 자동 증류 적립(메커니즘은 레퍼런스
  조사로 룰링: PreCompact 훅 vs SessionStart 넛지 vs 스킬 주도)
- [x] distill 스킬(플러그인) — 에이전트 자가 기억 추출·write --source hook
- [x] dup 대응 강화 — 자동화로 늘어나는 중복의 병합 제안 보고
- [x] 포트/DSN 설정 — FLINT_DSN 우선·설정 파일·compose 오버라이드 가이드
  (5433 점유 시 실사용 재개 조건)
- [x] 테스트 DSN 미설정 조기 실패
- [x] doctor 확장 — 자동 적립 훅 등록 검사·포트 충돌 진단
- [x] setup 원커맨드 보강
- 후보(v0.8): 시간여행 노출(read --as-of·diff·status --as-of — §16 검증
  전제) / 수명주기 액션(maintain 배치 무효화 dry-run·보존 정책 — stale
  실측치 축적 후)

### v0.8 시간여행 노출·수명주기 액션 (2026-10-23 기획)

시간여행 노출:

- [x] `flint read --as-of <시점>` — 시점 지정 조회(AS OF 쿼리, §16 검증 전제)
- [x] `flint diff` — 두 시점 knowledge/sessions 행 비교 보고(한 줄 JSON)
- [x] `flint status --as-of <시점>` — 시점 스냅샷 상태
- [x] audit 연계 — dolt_log 기반 시점 목록으로 as-of 후보 보조

수명주기 액션:

- [x] maintain 배치 무효화 — stale 항목 일괄 무효화 dry-run 기본·실행은
  명시 플래그(무효화=가역, 기존 원칙 승계)
- [x] 보존 정책 — invalidated 항목 보존 기한 관리(물리 삭제는 §15 이연 유지)
- [x] superseded 개념 — 갱신 지식 적립 시 구 항목 무효화 연결(Zep
  invalidated-not-deleted 준거, v0.7 증류 스킬 4연산과 연계)

### v0.9.2 2차 코드리뷰 수정 (2026-10-27 완료, PR #9)

- [x] 코드리뷰 지적 8건 — 레이스 ErrDuplicate 재확인 skip·잔여 재시도,
  agentForSession UUID만 claude 매핑, Classify 전역 상태 제거(conf_floor
  0~1 단일 게이트), dup 계측 일괄 증분(RecordUsageCount) 등
- [x] project kind 멱등 회귀 수정 — 자연키(name) 사전 dedup,
  ExistsProject 배선으로 시딩 멱등 복원

### v0.9.3 운영 정착 (2026-10-28 기획·완료)

- [x] R1 테스트 격리 — regression 고유 스키마·DSN, e2e DSN 헬퍼 교체
- [x] R2 실DB 정크 정리 — regression 잔여 행 삭제
- [x] R3 duplicate 응답 id 노출 — `{"status":"duplicate","id":N}`
- [x] R4 maintain 중복 그룹 보고 — `{"status":"dup-suspects","groups":[{"topic":T,"ids":[…]}]}`
- [x] R5 문서 동기화 — README(EN/KO) 배지 v0.9.3·아키텍처/라이프사이클
  SVG 임베드, 마일스톤 기록

### v0.9.4 훅 자가수복 (2026-10-28 기획·완료, PR #12) — 소급 기록

- [x] R1 훅 자가수복 — `doctor --fix`가 훅 스크립트 훼손·부재 시
  복구(멱등, 기존 진단 경로 승계)
- [x] R2 문서 동기화 — RUNBOOK 훅 자가수복 절차, 마일스톤 기록

### v0.9.5 채택 품질 (2026-10-29 기획·완료, PR #13)

- [x] R1 중복 절감 — SKILL.md 규칙 3종 추가(프롬프트 토큰 절감)
- [x] R2 read 촉진 — nudge 힌트 문구 반영(v0.7 계약 문구는 불변)
- [x] R3 설정 신규 생성 — `doctor --fix`가 settings.json 부재 시
  최소 뼈대 생성(백업 없음, 기존 merge·멱등 경로 승계)

### v0.9.6 검색 고도화 (2026-10-29 기획·완료)

- [x] R1 토큰 폴백 — read 구문 제로히트 시 단어 단위 OR 재검색·
  정합수 재랭킹, `read_fallback_calls` 계측
- [x] R2 계측 노출 — status JSON에 `read_fallback_calls` 필드
- [x] R3 문서 동기화 — README(EN/KO) read 폴백 1줄, 마일스톤 기록

### v0.9.7 전파·이전 정리 (2026-10-29 기획·완료, PR #15) — 소급 기록

- [x] R1 스킬 싱크 — `setup --update-rules`로 ~/.claude/skills 재동기화
- [x] R2 포트 5434 — Doltgres 포트 충돌 회피 기본값 이전
- [x] R3 배지·관측 — README 배지 갱신·관측 지표 보강
- [x] R4 문서 동기화 — 마일스톤 기록

### v0.10.0 검색 품질·백업 신뢰성 (2026-10-30 기획·완료, PR #16(예정))

- [x] R1 did-you-mean — read 제로히트 시 근접 후보 제안(suggestions)
- [x] R2 NFKC 유니코드 정규화 검색 — 매칭 비교 문자열 양측 정규화
- [x] R3 자동 백업 사이드카 — `flint done` 자동 백업 sha256 사이드카
  무결 검증
- [x] R4 doctor backup-age — 마지막 백업 72h 초과 시 warn 진단
- [x] R5 문서 동기화 — README(EN/KO) 배지 v0.10.0·제안/무결성 1줄,
  마일스톤 기록

### v0.11.0 노출 최소화 (2026-10-31 기획) — 프로파일·요약 주입·nudge 분기·훅 통합

- [x] R1 노출 프로파일 — `flint setup --exposure`로 주입 노출 수준 지정
- [x] R2 hot 요약 줄 — 세션 시작 주입을 요약 줄로 최소화
- [x] R3 nudge 분기 — 종료 시점 증류 안내 분기 처리
- [x] R4 훅 통합 — 플러그인 훅 3엔트리 통합
- [x] R5 문서 동기화 — SKILL/README(EN/KO) 노출·펼침 안내, 마일스톤 기록

### v0.12.0 pending 열람 완결·버전 파생 (2026-10-31 기획·완료) — read --status·VERSION 태그

- [x] R1 read --status — pending 상태 조건 열람(--as-of 병용 시 as-of 우선)
- [x] R2 make build VERSION 태그 — 최근 git 태그에서 버전 파생(태그 부재 시 dev 폴백)
- [x] R3 문서 동기화 — README(EN/KO) 배지 v0.12.0·read --status·make build
  1줄, SKILL pending 열람 안내, 마일스톤 기록

### v0.13.0 지능 축 (2026-10-31 기획) — 채택 재랭킹·모순 탐지

- [x] R1 채택 재랭킹 — RankOrder 최종 타이브레이크 read_count DESC
  (안정 정렬·기존 순서 존중)
- [x] R2 maintain --conflict-suspects — 동일 topic 요약 유사도 0.6
  미만 그룹 리스팅(dup-suspects 출력 승계 + 요약 앞 40자)
- [x] R3 doctor conflicts 검사 — 모순 후보 그룹 1+ warn(안내:
  maintain --conflict-suspects)·0이면 ok
- [x] R4 문서·TUNING 기록 — README(EN/KO) 채택 재랭킹·conflict-suspects
  1줄, TUNING conflictDupThreshold, 마일스톤 기록

### v0.13.2 패치 정합 (2026-11-01 기획) — 심층 리뷰 2차 수정

- [x] 프로세스 룰: 각 릴리스의 마지막 커밋에 README(EN/KO) release 배지
  갱신 포함(배지 가드는 최신 태그·다음 예정 버전 허용 — 커밋→태그 창 레드 없음)

### v0.14.0 패치 (2026-11-04 기획) — 제로히트 침묵 계약·zero_near 계측

- [x] R1 제로히트 침묵 계약 — read 재검색까지 제로히트 시 무출력·
  exit 1(제안 출력 폐지)
- [x] R2 zero_near 판별 계측 — 최고 근접 점수 suggestMinScore 0.15
  이상 제로히트를 zero_near로 계측
- [x] R3 report 노출·문서 정합 — report에 zero_near 계열 필드 노출,
  README(EN/KO) 침묵 계약 서술 갱신, TUNING zero_near 소절, 배지
  v0.14.0

### v0.15.0 우아함 축 (2026-11-05 기획) — 핫패스 계측 단일화·지표 기간화·진단 폐쇄

- [x] R1 핫패스 계측 단일화 — read_stats 단일 읽기-수정-쓰기
  (BumpReadStats 통합), 계측 왕복 예산 ≤ 5(병합 upsert)
- [x] R2 zero 지표 기간화 — daily read_zero·read_zero_near 키·
  report --days 창 합산 산출(zero_pct·zero_near_pct, 누적 read_stats
  오염 절연)
- [x] R3 doctor zero_near 검사 — 최근 7일 daily 합산 표본 read ≥ 10 &&
  zero_near 비율 ≥ 30%면 warn(어휘 불일치 의심), 체인 말미 등록
- [x] R4 계약 문서화 — README(EN/KO) exit 코드 표(0/1/2/3)·TUNING
  기간화 지표·doctor 임계·왕복 계약 소절, 배지 v0.15.0

### v0.15.1 패치 (2026-11-05 기획) — e2e 침묵 계약 잔존 청산·CI 그린 복구

- [x] R1 e2e 주입 케이스 계약 정합(exit 1·완전 무출력)·R2 배지 v0.15.1
- 태그는 #27 머지 커밋에 지연 발행(검증 스크립트 결함으로 그 시점 CI 레드 —
  v0.15.2에서 수정)

### v0.15.2 패치 (2026-11-05) — CI 빚 청산: 검증자·프로비저닝·진단성·드릴 가드

- [x] R1 validate_yaml 포트 단얱 5433→5434(v0.9.3 포트 전환 잔존 스테일 —
  CI test 잡은 #20 이상 전부 FAILURE였음이 판명, 즉시머지가 은폐)
- [x] R2 CI flint_test 프로비저닝(psql CREATE DATABASE — 서비스는 flint만
  생성)·GO_TEST_FLAGS 훅 추가(직렬화 필요 시 즉시 적용 가능한 예비선,
  현행은 평행 — 신규 DB 로컬 전수 그린 실측)
- [x] R3 진단성 — db.Retry 소진 시 마지막 오류 래핑(기존 폐기로 원인
  은폐)·드릴 테이블 제거 실패 시 오류 노출
- [x] R4 드릴 가드 requireDropVisibility — doltgresql 업스트림 결함(신규
  초기화 스토리지에서 DROP TABLE 무음 no-op·잔존, 1.3.3·1.4.0 실측)시
  드릴 스킵. 정상 엔진 전량 실행. 업스트림 이슈 후속.
- [x] R5 배지 v0.15.2·v0.15.1 태그 #27 머지 커밋에 지연 발행

### v0.16.0 (2026-11-05) — 릴리스 자동화·플러그인 마켓플레이스 배포

- [x] R1 릴리스 자동화 — 태그 push 시 Release 자동 생성(바이너리·
  checksums·플러그인 zip)
- [x] R2 플러그인 매니페스트 — `.claude-plugin/` 2종으로 마켓플레이스
  배포(`/plugin marketplace add synuns/flint` → `/plugin install flint@flint`)
- [x] R3 문서·배지 — README(EN/KO) 설치 섹션(릴리스 바이너리·플러그인
  설치·기존 setup 병기)·배지 v0.16.0
- 원칙 갱신: 태그 push가 곧 사용자 확인 — Release 자동 생성(v0.16.0부터
  수동 릴리스 절차 없음)

### v0.16.1 패치 (2026-11-05) — CI 리뷰 10건·플러그인 배포 결함

- [x] HIGH-1 release 잡 `needs: test` — 테스트 그린 후에만 릴리스 발행
- [x] HIGH-2 `.mcp.json` command `bin/flint`→`flint` (PATH — hooks와 기준
  통일, 배포 zip에 바이너리 없어도 MCP 기동)
- [x] HIGH-3 linux 교차빌드 — `goos`에 linux(amd64·arm64) 추가
- [x] MED goreleaser 버전 고정(latest 부양 제거)·`workflow_dispatch`
  트리거(재발사 외 재시도 경로)·doltgres digest 고정·잡별
  `timeout-minutes`(test 20·release 15)
- [x] LOW psql 준비 실패 `|| echo` 위장 삼킴 제거(exit 1)·플러그인 zip에서
  `*_test.go` 제외·validate_yaml이 release 잡·hooks.json 검사
- [x] 문서 — 배지 v0.16.1(EN/KO)·plugin.json 0.16.1 동기·README PATH
  요구 문구에 MCP 포함

### v0.16.2 패치 (2026-11-05 기획) — 드릴 계약 완결·pending 만료·쓰기 프로토콜

- [x] R1 드릴 계약 완결 — DROP 잔존 검증(수동 DROP 안내)·지연 정리
  독립 ctx·regression 단얱 갱신
- [x] R2 CI CREATE 가드 — flint_test 생성 실패 시 already exists 외
  exit 1(진단 위치 보존)
- [x] R3 헬퍼·검증 정리 — docs 공유 docStr·flint-plugin 공유 readDoc
  통합·validate_yaml 매니페스트 파싱 1회화
- [x] R4 pending 만료 — `maintain --expire-pending <days> [--commit]`
  (dry-run 기본·invalidated 전환)·doctor 만료 안내
- [x] R5 쓰기 프로토콜·문서 — SKILL 쓰기 우선순위 3계층(갱신 우선·
  최소화·read 선행)·배지 v0.16.2(EN/KO)

### v0.16.3 패치 (2026-11-05 기획) — 튜닝값 레지스트리화

- [x] 튜닝 상수 7종 Thresholds 레지스트리화 — suggest_min_score·
  suggest_scan_cap·conflict_dup_threshold·backup_age_warn_h·
  zero_near_min_read·zero_near_warn_pct·pending_age_days(기본값은
  thresholds.go 단일 소스·meta 오버라이드 우선)·TUNING.md §2 갱신·
  SKILL.md 개인 관측 인용 제거·CLAUDE.md 방어 계층 1줄·배지 v0.16.3(EN/KO)

### v0.16.4 패치 (2026-11-05) — release 잡 멱등 가드

- [x] 이미 발행된 태그는 자산 업로드 스킵(422 already_exists 방지 —
  이력 재작성의 태그 재푸시로 발견)·배지·plugin.json 동기

### v0.16.5 패치 (2026-11-05) — setup --mcp·MCP Registry 준비

- [x] `flint setup --mcp` — 하네스 자동 탐지·등록(exec.LookPath,
  결과 한 줄 JSON)·플랫폼별 `.mcpb` 번들 Release 자산화
  (scripts/make_mcpb.sh·goreleaser post hook + extra_files,
  checksums 불변)·mcp-registry/server.json(플레이스홀더)·README
  사용법·발행 3단계 안내·배지 v0.16.5(EN/KO)·plugin.json 동기

### v0.16.6 패치 (2026-11-06) — MCP Registry 자동 발행

- [x] server.json template($schema·version)·publish_mcp.sh·CI github-oidc
  스텝 — 태그 push 시 무인 발행

### v0.16.7 패치 (2026-11-06) — 완전 자동 릴리스 파이프라인

- [x] VERSION 파일(단일 버전 소스)·autotag 잡(main push→버전 변경 감지→
  자동 태그)·MCP Registry 발행 항상 실행(goreleaser 스킵과 분리)·
  github-oidc id-token: write. 사용자는 PR에 버전만 올리면 끝.

### v0.17.0 패치 (2026-11-06) — 의존성 관리·setup UX·CI 리뷰

- [x] R1 의존성 자동 관리 — dependabot(gomod·github-actions 주간)·
  CI mcp-publisher 다운로드 latest→v1.8.7 고정(재현성)·
  scripts/bump_version.sh(VERSION·배지 EN/KO·plugin.json 일괄 범프 —
  배지 누락 사고 3회 방지 자동화)
- [x] R2 setup UX 고도화 — 단계별 시각적 프로그레스(ANSI ✓/✗·딤 세부,
  비TTY 자동 plain)·`--json` 출력 불변
- [x] R3 CI 파이프라인 설계 리뷰 — docs/CI_REVIEW.md(구조 진단·개선점)
- [x] R4 버전·문서 — VERSION·배지 v0.17.0(EN/KO)·plugin.json 동기

### v0.17.1 패치 (2026-11-06) — CI 리뷰 후속·코드 위생·문서 정합

- [x] R1 CI_REVIEW §4 청산 — 태그 push `|| true` 제거(실패 시 중단)·
  concurrency cancel-in-progress(같은 레프 연속 실행 취소)·DB 대기
  단일화(doctor 통과 후 psql 1회)·PyYAML pip 캐시(setup-python+
  requirements.txt)
- [x] R2 코드 위생 — gofmt 18파일 정리 + `make ci` fmt 게이트 신설·
  as-of 접두어 플레이크 방지(해시 접두어가 상대 문법 \d+[hd]와 겹칠 때
  회피 창 선택 — 제품 계약 "① 상대 우선"은 불변)
- [x] R3 Quick Start 실패 수정 — `make install` GOPATH/bin 실설치화
  (bare `flint` 동작)·`make seed` FLINT_SEED_DSN 명시·Go 배지 1.26·
  exit 3 서술 확장(스키마 위반 포함)·Copilot 자동 등록 과대 표기 정정
- [x] R4 문서 스위트 정합 — QUICKSTART v0.17(setup --mcp·플러그인·
  시각 진행 서술)·RUNBOOK doctor 검사 10종 표+`--expire-pending`
  계약·adapters 4종 자동 등록 안내·TUNING suggest_min_score 폐지 기능
  서술 정정
- [x] R5 버전 — VERSION·배지 v0.17.1(EN/KO)·plugin.json 동기

### v0.18.0 (2026-11-06) — 회상 우선 메모리 (recall-first memory)

테마 선정 근거: 관측 정합 결과 누적 zero_pct(구 메트릭, 초기 이력 지배)와
기간화 zero_pct가 갈라 검색 품질 트리거는 미도달 — 실측 신호는 중복
재제출이 지배적이고 회상이 굶는 상태라는 것. 내부 전수(중복 경로·read
유도 장치)·외부 조사(mem0 NOOP·Anthropic already-exists·세션 시작
프로토콜·Doltgres 확장 현황)를 종합해 "기록만 하고 회상 안 함"을
write·주입·종료·관측 4개 접점에서 교정한다.

- [x] R1 duplicate 응답 피드백(#61) — 기존 행 topic·state 노출(재기록
  대신 read/supersede/approve 유도)·SKILL.md 가이드
- [x] R2 hot 헤드라인 주제 노출(#63) — normal 프로파일에 최근 결정 주제
  상위 3·회상 힌트(minimal 불변·--full 바이트 불변)
- [x] R3 done 회상 경고(#64) — 오늘 read 0회·기록 임계 이상 시 종료 경고
  (recall_warn_writes 기본 3, thresholds 레지스트리 등록)
- [x] R5 폴백 검색면 tags 포함(#65) — 태그로만 분류된 지식 회수(구문
  접합면 불변)
- [x] R4 status 기간화 지표(#62) — read_calls_14d·read_zero_pct_14d 병기
  (누적값 오독 방지, report와 동일 산식)
- [x] 버전 — VERSION·배지 v0.18.0(EN/KO)·plugin.json 동기

### v0.18.1 패치 (2026-11-06) — v0.18.0 코드리뷰 8건·완성도 2건

- [x] R-a 개인 관측 수치 서술 중화 — PLAN 엔트리·테스트 주석·아카이브
  spec 2건에서 실측 백분율 인용 제거(원칙: 근거는 일반 서술로)
- [x] R-b 회상 경고 전달 경로 폐쇄 — Stop 훅 stdout은 자동 배선에서 안
  보이므로 done이 recall 마커를 남기고 다음 SessionStart(nudge)가 전용
  문구 1회 표시 후 자동 소진(증류 마커와 Reason 분기·기존 경로 불변)
- [x] R-c recall_warn_writes -1=끔 의미론(파서·폴백·TUNING 문서화)
- [x] R-d daily 파서 단일화 — windowedDaily 공유 리더(report·status가
  소비, 3중 복제 해소)·read_zero_near_pct_14d 노출(데드 반환 폐지)
- [x] R-e DupIDByHash를 DupRowByHash 위임으로 통합(dedup SQL 단일 소스)
- [x] R-f bump_version.sh가 배지 alt도 교체(구버전 alt 잔존 갭)·README
  alt 정합·hotTopics 상수화·DupRowByHash 실DB 테스트·done/e2e 마커
  격리(FLINT_CONFIG_DIR — 테스트가 실사용자 config에 마커 남기던 오염)
- [x] 버전 — VERSION·배지 v0.18.1(EN/KO)·plugin.json 동기

### v0.18.2 패치 (2026-11-06) — 2차 리뷰 5건 — 마커 수명 주기 분리

- [x] R-a recall 마커 파일 분리(recall-hint.json) — v0.18.1의 단일
  마커 파일 설계 결함 2건 폐쇄: ① Stop 이벤트에서 recall 마커를 증류로
  오인해 엉터리 넛지 + --clear로 회상 알림 소멸 ② recall 기록이 내구성
  증류 마커(pending-distill)를 덮어써 증류 체인 유실. Stop은 증류만
  넛지, SessionStart는 둘 다 안내(회상만 자동 소진)·v0.18.1 잔존
  마이그레이션(증류 파일의 Reason=recall 식별)
- [x] R-b thresholds 파서 범용화 — parseIntInRangeOrDefault(min 하한)
  도입, recall_warn_writes 특수 분기 제거·TUNING에 0 폴백·1=매기록
  경고 문서화
- [x] R-c 관측 수치 잔존 중화 완료 — 아카이브 spec 3건 추가 항목
- [x] R-d doctor hook-file fix 문구 현대화 — settings.json 수동 등록
  안내를 플러그인 마켓플레이스 설치 우선으로
- [x] 버전 — VERSION·배지 v0.18.2(EN/KO)·plugin.json 동기

### v0.18.3 패치 (2026-11-06) — staticcheck 도입·사체 코드 청산

- [x] R-a staticcheck 2026.2.2 도입 — make lint 타깃(PATH→GOPATH/bin
  폴백)·CI test 잡 게이트 추가(버전 고정 — mcp-publisher 관례 준용)
- [x] R-b 사체 코드 제거 — portCheck(doctor v0.7 구판 래퍼, runDoctor
  경로가 실경로)·injectedKnowledgeIDs+hotSurvivorCount(접미 없는 구판
  생존 산정, hotLineSurvivorCount 체인이 실경로) — 각자의 테스트를
  실경로(runDoctor --check port)로 이관 후 제거
- [x] R-c 경미 결함 — nil Context(SA1012)·불필요 Sprintf 3건·미사용
  sentinel·CanonicalJSON 결정성 비교 명료화(2회 호출 변수 비교)

---

<!-- v0.44.0 T1: 구 PLAN.md §14 이관분(v0.19.0~v0.43.0, 시간순 재배열) -->

### v0.19.0 마이너 (2026-11-07) — 효과 계측 (effect instrumentation)

- [x] R1 측정 기준 문서 — docs/METRICS.md: "도움"의 정의 4목표(GSM
  매핑)·증거 등급(Tier 1 상관 프록시 / Tier 2 연결 상관 / Tier 3 합성
  인과=floor)·금지조항(사용량≠효과·단일 지표 게이밍·상관의 인과 서술
  금지·실측치 미인용). 기준이 코드보다 앞선다(HEART/GSM 관례)
- [x] R3 read 이벤트 additive 계측 — 스키마 v6 read_events(ts·hits·
  zero·near·fallback·session, 14일 보존), EnsureSchemaV6 게이트,
  read --session 선택 라벨(조회 동작 불변). 쿼리 본문·개인 문구 미기록
- [x] R2+R4 report 효과 섹션 — effect 객체: hit_pct(G1)·dup_prevented
  (G2, 절약 프레임 재해석)·reuse_pct·dead_pct(G3)·read_events_n·
  read_write_pairs·pair_pct(G4, 30분 창 Go-사이드 조인 — Doltgres
  interval 리스크 회피)
- [x] 버전 — VERSION·배지 v0.19.0(EN/KO)·plugin.json 동기

### v0.20.0 마이너 (2026-11-07) — 효과 계측 정밀화 (effect precision)

- [x] R1 스키마 v7(additive) — read_events.ids(반환 id CSV, 상위
  10)·session_starts(세션 경계)·hot_events(주입 노출), 3테이블 14일
  통합 보존 정리
- [x] R2 세션 경계(D) — session-start 훅이 SessionStart 1회당 경계
  기록(출력 없는 내부 계정, 전 프로파일). compact·resume 재발발은
  보수적 분할로 수용(룰링)
- [x] R3 페어링 정밀화(D) — 같은 세션 배정에서 read 직후 30분 내
  write만 페어(타 세션 우연 동반 제외). 경계 부재 환경은 시간창
  폴백. 신지표 recall_led_pct(회상 선도 세션 비율 — done 회상
  경고와 같은 관점의 관측 버전)
- [x] R4 사용 연쇄(B) — 반환 id 기록 + chain_pct(24h 내 재반환 또는
  같은 topic write). IR 암시적 피드백 관례 — 프라이버시 무해·비간섭
- [x] R5 노출 계층(E) — hot --full 주입 id 기록(hot_events·30일 보존)
  + dead_pct 정합 가드: 주입은 BumpReads로 read_count를 올리므로(리뷰
  재정의) 정상 경로에 후보 밖 — 가드는 참조 증분만 실패한 계측 유실
  불일치를 메운다
- [x] 룰링: 기저율 셔플(C) 이연 — 세션 경계가 우연 동반을 구조적으로
  제거, 잔여는 표본 축적 후 재평가(METRICS §4 명시)
- [x] 버전 — VERSION·배지 v0.20.0(EN/KO)·plugin.json 동기

### v0.20.1 패치 (2026-11-07) — 패치 루프 1차: 전수 조사 18건 중 정확성 4건·테스트 왜곡 2건

관측·조사 → 레퍼런싱 → 브레인스토밍 → 수정 4단계의 1차 이터레이션
(전체 기능 스윕 7건 + 테스트 품질 감사 9건 + 운영 관측 2건):

- [x] F1 무효화 상태 정합(high) — Invalidate·Merge(from)이
  status='invalidated' 병기(배치 경로와 단일 법칙 — read --status
  invalidated 노출·report counts 정합) + Approve 승격 시 invalid_at
  청소(active+invalid_at 좀비 방어)
- [x] F2 setup 배선 — st 고정 nil로 정상 DB에서도 매번 degraded(QUICKSTART
  약속 불이행). 연결 시도 후 실패 시에만 nil 진행으로 수정 — 깨진 동작을
  '계약'으로 문서화하던 테스트(TestSetupJSONMode)도 재작성(테스트 감사
  축2 사례) + DB 다운 degraded 경로 별도 테스트로 보존
- [x] F3 hot 헤드라인 카운트 왜곡 — 카운트 경로 상한 50(HotHeadlineSQL),
  주입(--full)은 예산 상한 10 계약 불변
- [x] F4 자동 백업 로테이션 — maybeAutoBackup이 keep-N 로테이션 호출
  (수동 경로와 동일 계약, 이전엔 무한 축적)
- [x] T1·T8 테스트 왜곡 즉치 — effect fixture 미매칭 쿼리 t.Fatalf
  계약화(즉시 3종 잠복 흡수 발견)·hot_full_guard want 리터럴 고정
- [x] 버전 — VERSION·배지 v0.20.1(EN/KO)·plugin.json 동기

이월(v0.20.2+): docs 테스트 통합(26파일→1)·doctor --check 체크명 정합·
F5 --fields 오타 검증·F6 nudge 죽은 분기·F7 MCP 사체·T2 stale/dead
판별 계약·T3 게이트 v2~v5 내용 단언·T6 신규 테스트 네이밍 정책.

### v0.20.2 패치 (2026-11-07) — 패치 루프 2차: 이월 8건 완료

- [x] T5 docs 테스트 통합 — 버전별 26파일(v0접두) → docs/versions_test.go 단일
  table-driven(표 50행 + 커스텀 로직 18함수 원본 보존, 커버리지 불변)
- [x] O2 doctor --check 정합 — 전체 리포트 체크명(skill-file·harness-*)
  단독 실행 허용(이름 공간 불일치 해소)
- [x] F5 --fields 검증 — 알려진 컬럼만 허용, 오타 위치 fallback 왜곡
  exit 3으로(ReadFieldKnown 사전 검증)
- [x] F6 nudge 죽은 조건문 제거·F7 MCP 사체 제거(NewSimpleStore 체인 —
  테스트 순환까지 정리)
- [x] T2 판별 계약 — 제품 SQL 자기 식별 마커(/* stale */·/* dead */),
  substring 추측 판별 폐지
- [x] T3 게이트 산출물 표 단언 — v2~v7 컬럼·인덱스·테이블 존재 검증
  (게이트 no-op 회귀 방어)
- [x] T6 테스트 작성 규약 — docs/TESTING.md(네이밍·fixture 라우팅·
  단언 원칙)
- [x] 버전 — VERSION·배지 v0.20.2(EN/KO)·plugin.json 동기

### v0.20.3 패치 (2026-11-07) — 패치 루프 3차: 문서·매니페스트 정합 7건

관측(문서·매니페스트·배포 자산 스윕 + 운영 G4 파이프라인 실증) → 수정:

- [x] D1 TUNING §8·§10 낡은 '코드 상수 직접 수정' 서술 정정(high) —
  §2 레지스트리화 이후 실상과 정면 모순(튜닝 원칙 위반을 문서가 유도)
- [x] D2 README 하이라이트 줄 v0.17.0→현재(EN/KO) + 재방치 방지
  게이트(TestReadmeHighlightMatchesVersion — 배지 동기 밖 영역)
- [x] D3~D7 사소 정합 — setup 행 '시드 안내' 정확화·PLAN 파일 수
  34→26·QUICKSTART 의존(x/text)·RUNBOOK 스키마 v7·검사 조건부
  보충·KO 서브텍스트 8종·QUICKSTART 주석 위치
- [x] 운영 실증 — session_starts 기록 파이프라인 정상(빈 기록은 설치 후
  SessionStart 미발생, 결함 아님)
- [x] 버전 — VERSION·배지 v0.20.3(EN/KO)·plugin.json 동기

### v0.20.4 패치 (2026-11-07) — 패치 루프 4차: 마일스톤 B③·C①의 패치 규모 항목 조기 회수

사용자 지시로 패치 루프 연속 진행(버전마다 정지하지 않음) — v0.21.0+
후보 중 패치 규모 항목을 회수:

- [x] B③ 계측 임계값 thresholds 레지스트리 이양 — dead_days·
  pair_window_minutes·chain_window_hours·events_retention_days·
  hot_events_retention_days·events_volume_warn 6키(판정 창·보존·용량,
  TUNING §2 동기)
- [x] C① doctor event-volume 체크 — 계측 3테이블 합계 행 수 warn
  (성장은 정상 동작이므로 warn 안내만, fail 아님)·--check 지원
- [x] PruneReadEvents 보존일 meta 오버라이드·report 판정 창 오버라이드
- [x] 버전 — VERSION·배지 v0.20.4(EN/KO)·plugin.json 동기

### v0.20.5 패치 (2026-11-07) — 패치 루프 5차: B② 세션 라벨 자동 주입

- [x] session-start 훅이 현재 세션 라벨을 config 경로 파일
  (session-current)에 기록, read가 --session 미지정 시 조용히 자동
  채택 — G4 라벨링이 에이전트 협조 없이 자동화. session-end가 정리
  (스테일 방지). 병렬 세션 last-writer-wins(룰링 — 보조 계정)
- [x] 버전 — VERSION·배지 v0.20.5(EN/KO)·plugin.json 동기

### v0.20.6 패치 (2026-11-07) — 패치 루프 6차: 보안 경화 5건 + C② 위생

- [x] S1(medium) hot 출력 재살균 — read와 동일 방어(ADR-0006): 저장 후
  유입 [flint] 마커·제어 문자가 세션 시작 주입 경계를 위조하지 않게
- [x] S2(medium) 살균·비밀 스캔 필드 확장 — tags 배열 원소·domain·origin
  적용(이전 사각지대 — 비밀 경고 없이 저장·백업 영속)
- [x] S3(low) 백업 파일 권한 0600·디렉터리 0700(자동·수동 덤프·사이드카)
- [x] S4(low) restore 사이드카 부재 시 재생 전 차단 — 무검증 임의 SQL
  재생 폐지(종료 후 경고→가드, ADR-0006 즉시 중단 취지)
- [x] S5(low) 스킬 싱크 심볼릭 링크 skip(링크 대상 민감 파일 노출 차단)
- [x] C② meta 키 위생 — 고아 키(last_dup_manual_at, 코드 무참조) 운영
  정리·drill_max_days TUNING §2 등록(문서 미등록 오버라이드)
- [x] 검증 차단 확인 3건 — MCP 인자 주입·SQL 접합·read 이중 방어(스윕
  보고)
- [x] 버전 — VERSION·배지 v0.20.6(EN/KO)·plugin.json 동기

### v0.21.0 마이너 (2026-11-07) — 전 AI 에이전트 어댑터 지원 (adapter coverage)

사용자 지시(2026-11-07): 일부 하니스만 명시하던 어댑터를 모든 AI
에이전트로 확장. 외부 조사(공식 문서 기반) → A/B/C급 분류 → 구현.

- [x] R1 harnessSpec.MCPAdd 필드화 — mcpAddArgs switch 제거, 하니스
  추가가 레지스트리 1행으로 종결
- [x] R2 A급 8종 자동 등록 — 기존 5종 + Qwen Code·Devin CLI·Cline
  신규 + Copilot CLI 검증 승격(안내 전용 분기 폐지)
- [x] R3 B급 6종 의사체크 — Windsurf·Zed·Amp·VS Code(.mcp.json)·
  Goose·Trae: doctor harness 체크+문서(등록 CLI 없음 — 파일 설정형).
  룰링: continue는 설정 경로가 파일 단위로 확정되지 않아 문서 전용
- [x] R4 어댑터 문서 10종 신규 + copilot.md 승격 갱신 + README 하니스
  표(EN/KO) 확장 — `.mcp.json`이 Copilot CLI·VS Code 양쪽을 커버하는
  수렴 표준임을 문서화
- [x] 버전 — VERSION·배지 v0.21.0(EN/KO)·plugin.json 동기

### v0.21.1 패치 (2026-11-07) — 패치 루프 7차: 어댑터 문서 스니펫 공식 검증

- [x] 신규 10종 스니펫을 공식 문서와 대조 검증(에이전트 웹 조사) —
  6종 정확·4종 수정: qwen(유효 JSON), zed(command 문자열·args 형제),
  continue(v1 스키마 메타데이터 필수), goose(command→cmd)
- [x] 전 10종에 등록 확인(`flint doctor --check harness-<name>`)·
  홈 경로 env 오버라이드 안내 보강
- [x] 버전 — VERSION·배지 v0.21.1(EN/KO)·plugin.json 동기

### v0.21.2 패치 (2026-11-07) — 패치 루프 8차: C③ PLAN 이력 아카이브

- [x] §14 마일스톤 과거 엔트리(v0.1~v0.18.x)를 docs/HISTORY.md로
  이관 — PLAN은 현행 시대(효과 계측 v0.19.0+)만 유지(868→456행).
  이력 무수정 원칙(이동 시점 그대로)
- [x] 문서 회귀 테스트의 과거 버전 계약행·커스텀 함수 4종을
  HISTORY.md로 재지정(커버리지 불변)
- [x] 버전 — VERSION·배지 v0.21.2(EN/KO)·plugin.json 동기

### v0.21.0+ 마이너 후보 (2026-11-07 사용자 지시 — 2026-11-09 완료 상태로 롤업)

> 원문 지시 항목을 현재 버전(v0.27.4 기준) 상태로 정리했다. 완료
> 상세는 아래 각 버전 엔트리 참조. 진행/이월 항목은 H(v0.28.0)와
> 잔여 목록으로 승계.

**A. 자동 업데이트 — 완료 (v0.22.0·v0.22.x)** — `flint update` 자가
갱신(sha256 checksums.txt 검증·verify-then-swap·https 가드·128MiB
상한)·세션 시작 [flint] 경계 내 1줄 갱신 안내(24h 캐시)·MCP Registry
자동 발행. 배포 폐회 — 21연속 자가 갱신 홉 실증.

**B. 지표 고도화 — 부분 완료** — ✓ 세션 라벨 자동 주입(v0.20.5 라벨
파일·v0.25.x read --session 채택)·임계값 레지스트리 이양(v0.24.0
tuning 30+키). 잔여(관측 창 후): 기저율 셔플 재평가(룰링 이연 —
표본 축적 후)·트렌드 Δ 보고·hit 품질 근사.

**C. 내부 테이블·문서 관리 고도화 — 대부분 완료** — ✓ 보존 기간 meta
오버라이드(daily_retention_d 등)·doctor 용량 점검(event-volume)·
PLAN→HISTORY 아카이브(v0.21.2)·버전별 docs 테스트 통합
(versions_test.go)·문서 상호 링크. v7 3테이블 구조 유지 판정. 잔여:
meta 키 롤링 정리(low).

**D. 개인화 기능 고도화 — 부분 완료** — ✓ 어휘 후보 제안(v0.24
suggestFrom)·`flint tuning` 인터페이스(v0.24.0 list|get|set|reset).
잔여: 프로파일 추천(effect 관측 기반 — 관측 창 후)·프로젝트/에이전트
스코프 필터(다중 에이전트와 함께 이연). 원칙 유지: 개인값은 운영
DB에만(repo 기본 불침투).

**E. 대시보드 — 평가 항목 유지** — 미판정(report 텍스트 소비 관측
중). 후보 `flint report --html` 정적 내보내기(단일 파일·로컬 렌더만).

**G. 고도화 로드맵 (2026-11-08 8각도 조사) — 대부분 반영**

| 테마 | 상태 | 반영 버전 |
|---|---|---|
| 검색 품질 1막(RRF·자모 bigram·pgvector probe) | **완료** | v0.25.0~v0.25.4 |
| F 프라이버시 실행(secrets 스캔·백업 암호화·마스킹·하드 삭제) | 스캔 **완료**(v0.26.0~v0.27.4)·암호화·마스킹 **v0.28.0 진행**·하드 삭제 이연(ADR-0012) | v0.26.0+ |
| 온보딩 임포트(CLAUDE.md→지식 변환) | **완료** | v0.27.0·v0.27.3 |
| 증류 자동화(규칙 기반) | 회로 폐쇄 **완료**(v0.27.0 세션 지식화·v0.27.2 실질화)·maintain 자동 배선 잔여 | v0.27.0+ |
| E 정적 export(report --html) | 잔여(텍스트 소비 관측 후) | — |
| 성능 관측 저비용(doctor active 행수·용량) | 잔여(low) | — |
| 다중 에이전트(domain 스코프) | 이연(트리거 대기 — 팀 수요 증거) | — |

**F. 프라이버시 기능 (2026-11-08 지시) — 진행/이월 혼재** — ✓ ① 저장
지식·세션 사후 비밀 스캔(doctor secrets — v0.26.0~v0.27.4, allowlist·
행 상한 포함). 진행 ② 백업 age 암호화·④ 덤프 마스킹(v0.28.0 — H②로
승계). 이연 ③ 하드 삭제(ADR-0012 — 백업 재구축 경로, 트리거 대기).
원칙 승계: 저장 살균·비밀 스캔·0600 권한·개인값 repo 불침투·계측
최소화(METRICS §4 '쿼리 본문 미기록').

### v0.22.0 마이너 (2026-11-07) — 자동 업데이트 + go install 배포 경로

사용자 지시(브레인스토밍 확정: 출시≠실행 갭 — 루프가 직접 실증한 고통):

- [x] R1 `flint update` — 최신 Release 확인→자산 다운로드→checksums.txt
  sha256 검증(필수)→tar 추출→version 실행 검증(verify-then-swap)→
  원자 교체(rename). 실패 경로 전부 원본 불변. --check 드라이런
- [x] R2 세션 시작 갱신 안내 — 캐시 TTL 24h(하루 1회 왕복), [flint]
  경계 내 1줄, 네트워크 실패 조용히(§13 — 안내만, 자동 실행 없음)
- [x] R3 doctor update 체크 — 릴리스 지연 warn(안내만)
- [x] R4 모듈 경로 전환 `flint`→`github.com/synuns/flint`(임포트 63파일·
  Makefile·goreleaser ldflags) — `go install github.com/synuns/flint/
  cmd/flint@latest` 배포 경로 개통. ReadBuildInfo 폴백으로 go install
  빌드의 버전 자가 보고(CurrentVersion)
- [x] 버전 — VERSION·배지 v0.22.0(EN/KO)·plugin.json 동기

### v0.22.1 패치 (2026-11-07) — v0.22.x 패치 루프 1차: update 경로 보강

- [x] S1 다운로드 URL https 강제 — API 응답의 browser_download_url이
  평문 스킴이면 거부(응답 변조·다운그레이드 방어). 테스트는 httptest
  TLS 서버 + 자체 서명 수용 seam(프로덕션 검증 불변)
- [x] S2 다운로드 상한 128MiB(io.LimitReader) — 무한 적립 방어,
  초과분은 체크섬 불일치로 즉시 중단
- [x] 버전 — VERSION·배지 v0.22.1(EN/KO)·plugin.json 동기

### v0.22.2 패치 (2026-11-07) — v0.22.x 패치 루프 2차: 리뷰 6건 — 프록시·리다이렉트·테스트 seam

- [x] ① 프록시 회귀 수정 — updateClient가 DefaultTransport 복제로
  ProxyFromEnvironment 유지(v0.22.1의 bare Transport가 프록시 환경
  전체를 단절시켰던 것)
- [x] ② 리다이렉트 https 가드 — CheckRedirect로 https→http 다운그레이드
  거부(S1의 실질 방어 완결)
- [x] ③ InsecureSkipVerify 전역 제거 — 클라이언트 팩토리 seam +
  httptest srv.Client() 주입(프로덕션 바이너리에서 검증 비활성 코드
  소멸, gosec G402 해소)
- [x] ④ 다운로드 상한 초과 명시적 에러 — 체크섬 불일치 오탐 진단 방지
- [x] ⑤ API 응답 8MiB 상한 해석 — 메타 경로 무한 적립 방어
- [x] ⑥ README 태그라인 실내용 반영 — 이후 매 패치 갱신 관례 명시
- [x] 버전 — VERSION·배지 v0.22.2(EN/KO)·plugin.json 동기

### v0.23.0 마이너 (2026-11-07) — 개인화 1막: tuning 커맨드·어휘 제안

사용자 지시(다음 마이너 = D개인화 브레인스토밍 순서표):

- [x] D③ `flint tuning` — TUNING §2 전체 키의 list/get/set/reset.
  값 검증은 실경로 파싱으로(폴백되면 거부) — 문서·레지스트리·명령이
  단일 소스. 이전까지 psql 직접 갱신이던 관리 진입장벽 해소
- [x] drill_max_days 레지스트리 이관 — drillReport가 메타 직독에서
  thresholds 단일 소스로(키 메타 22종 전량 tuning 노출)
- [x] D② 어휘 제안 — 어휘 밖 tag·domain에 근사 후보(접두 우선·편집거리
  ≤2) 경고에 병합("유사: adr") — 오타 수정 가이드
- [x] 버전 — VERSION·배지 v0.23.0(EN/KO)·plugin.json 동기

### v0.23.1 패치 (2026-11-07) — v0.23.x 패치 루프 1차: TUNING 관리 경로 문서화

- [x] TUNING §2 서두에 `flint tuning` 오버라이드 관리 경로 문서화
  (set 검증 규칙·개인값 저장 위치) — 새 관리면의 문서 정합
- [x] 운영 실증 — set→get(override 반영)→reset→get(기본 복귀) 왕복
- [x] 버전 — VERSION·배지 v0.23.1(EN/KO)·plugin.json 동기

### v0.23.2 패치 (2026-11-07) — v0.23.x 패치 루프 2차: 개인화 기획 리뷰 — 결함 1·문서 1

개인화 기획 리뷰(에이전트 설계 조사, 지시 반영) 결과 처리:

- [x] 결함 1(high) hot_view decision 팔 ↔ 어휘표 모순 — domain 어휘
  4종화(knownDomains·CONTENT §4에 decision 추가). 초기 스키마부터
  존재한 은폐 계약 불일치(어휘 준수 시 '최근 결정' 팔 공회전·
  decision 사용 시 unknown domain 경고)
- [x] 갭 TUNING §5 exposure_profile 문서화(값·효과·설정 경로)
- [x] 버전 — VERSION·배지 v0.23.2(EN/KO)·plugin.json 동기

v0.24 후보 적립(기획 리뷰 권고): 어휘 meta 오버라이드(vocab_tags·
vocab_domains 합집합)·tuning 커버리스 확장(hot_budget·backup_keep·
exposure_profile)·reset --all·consumer 표시·D① 축소형(profile_hint
표본 하한 포함·경계 내 안내)·D④ 관측 패치(도메인별 read 분해) 후
계층 도메인 §15 이연.

### v0.24.0 마이너 (2026-11-08) — 개인화 2막 + 임계값 설계 완결

사용자 지시: 개인화 2막에 임계값 효과 실증·재조정 기준·조정 시점·
리스트 충분/과잉의 전체 설계 점검 포함(관측+완전성 감사+레퍼런싱 합성).

- [x] R1 튜닝 누수 4건 — reportStaleDays→stale_days 단일화(오버라이드
  우회 결함)·daily 보존 3곳→daily_retention_days 신규 키(Doltgres
  파라미터 회피: Go 계산 컷오프)·backup_interval_h 신규(주기 오버라이드,
  backup_keep과 세트)·tuning 커버리스 확장(hot_budget·backup_keep·
  backup_interval_h·exposure_profile)
- [x] R2 발동 창구 정당화(감사 ③-1) — done이 recall_starved_count meta
  적립(마커 병행) + doctor recall-starved 체크: minimal 프로파일이
  마커를 침묵시켜도 관측 유지. session-start 정비 요약(pending_max,
  24h 캐시 — 임계값 6키의 유일 창구가 수동 커맨드였던 것의 최소 연결)
- [x] R3 TUNING §0 조정 가이드 — 언제(관측 창 후)·재조정 트리거 표·
  표기 정정 3건(pending_max·drill_max_days 소비 경로, events_volume_warn
  방어 상한 성격)
- [x] R4 개인 어휘 meta 합집합(vocab_tags·vocab_domains — repo가 아닌
  운영 DB, 기본 집합과 union)·reset --all·consumer 표시·profile_hint
  (D① 축소형 — 표본 하한 미달 '보류', METRICS §5 보고 전용 준수)
- [x] R5 hot_events 원천 문서화(--full 수동 전용 — 운영 공백이 정상)
- [x] 설계 점검 결론 반영: 키 수 과잉 아님(제거 후보 0) — 부족한 것은
  누수 4건(수정)과 자동 노출(연결)이었다
- [x] 버전 — VERSION·배지 v0.24.0(EN/KO)·plugin.json 동기
- [x] 리뷰 7건 — tuning get meta키 분기·정비 캐시 실패 시 미갱신·
  recall 조회 실패 warn 위장 방지·status read_window_days 필드(거짓
  14d 명칭 방어)·SetVocabExtras 대체语义(mcp 장기 프로세스)·meta키
  기본값 소비자 상수 단일소스·profileHint 표본 하한 th 연동

### v0.24.1 패치 (2026-11-08) — v0.24.x 패치 루프 1차: profile_hint 조기 return 결함

- [x] 운영 관측 발견 — profile_hint가 ids 없는 read 이벤트만 있는
  운영에서 영구 공백: 힌트 할당이 hasIDs 조기 return 뒤에 있었다.
  표본 하한 판정은 ids 유무와 무관하므로 가드 앞으로 이동
- [x] 버전 — VERSION·배지 v0.24.1(EN/KO)·plugin.json 동기

### v0.24.2 패치 (2026-11-08) — v0.24.x 패치 루프 2차: 개인화 평가 발견 — 결정 판정 adr 태그 전환

- [x] hot_view '최근 결정' 팔을 domain='decision'에서 tag='adr'
  기준으로(실사용 관측에서 decision domain 관례는 채택되지 않았다 — 결정은
  domain=main+tag=adr
  관례. 개인화 평가 실측이 확정, 기획 리뷰 대안 b 채택). CONTENT §4에
  정준 경로 문서화·decision은 레거시 표기
- [x] 버전 — VERSION·배지 v0.24.2(EN/KO)·plugin.json 동기

### v0.25.0 마이너 (2026-11-08) — 검색 품질 1막: RRF 융합·자모 bigram·pgvector probe

마일스톤 G 추천 이행(계측 체계 선행 — 개선 효과 검증 즉시 가능):

- [x] R1 RRF(Reciprocal Rank Fusion) 융합 랭킹 — 폴백 재랭킹을 순차
  타이브레이크(정합>read)에서 정합수·read_count·자모 유사도 3신호
  RRF 융합으로(k=60). 구현 초판의 부호 반전 결함(값=rank 혼동)을
  순위 기반 rrfFuse로 수정·회귀 테스트로 봉쇄
- [x] R2 자모 bigram — 한글 토큰을 초·중·종성 분해 bigram으로 근사
  매칭(오타·어미 변형 흡수 — "백업"↔"백업용"·자동↔수동 부분 공유)
- [x] R3 pgvector 에뮬 실증 probe — ADR-0011: CREATE EXTENSION·
  vector(3)·cosine `<=>` 순위까지 실증 성공. 채택은 보류(임베딩 생성
  경로·인덱스 성능·트리거 미도달 — ADR에 명시)
- [x] 버전 — VERSION·배지 v0.25.0(EN/KO)·plugin.json 동기
- [x] 리뷰 7건 — ① 자모 신호 한글 토큰 한정(비한글 이중 계상 방지)
  ② RRF 계약 주석 갱신(구 순차 타이브레이크 서술 폐기) ③ rrf_k를
  thresholds 레지스트리로(SetRRFK 주입·tuning 노출·TUNING §2) ④
  bigramSet 재사용(자모 분해만 신규) ⑤ FormatRows 주석 복원 ⑥ 정규화·
  토큰 집합 hoist(행당 중복 계산 제거) ⑦ ADR URL 개행 수정

### v0.25.1 패치 (2026-11-08) — v0.25.x 패치 루프 1차: RRF 제4신호 topic 정합

- [x] 운영 관측 발견 — 폴백 재랭킹에서 topic 정합 신호가 융합에 빠져
  read_count 지배적 주제 외 행이 topic 정합 행을 누름(구문 경로
  RankOrder의 topic 우선 관례가 폴백 융합에서 소실). topicMatchCount
  제4신호로 회수 — 운영 실측에서 topic=2/rc=6 행이 rc=109 행 선회
- [x] 버전 — VERSION·배지 v0.25.1(EN/KO)·plugin.json 동기

### v0.25.2 패치 (2026-11-08) — v0.25.x 패치 루프 2차: rrf_k 주입 순서 배선 결함

- [x] 직접 검증 발견(사용자 챌린지 "잘 살펴봐") — SetRRFK 주입이 폴백
  재랭킹 뒤(제안 블록)에 있어 단발 CLI에서 rrf_k 오버라이드가 같은
  실행에 반영되지 않음. 주입을 재랭킹 앞으로 hoist·loadThresholdsFn
  seam으로 순서 회귀 테스트. 낡은 v0.13.0 재랭킹 주석 갱신
- [x] 버전 — VERSION·배지 v0.25.2(EN/KO)·plugin.json 동기

### v0.25.3 패치 (2026-11-08) — v0.25.x 패치 루프 3차: 2차 리뷰·미커버 스윕 13건 중 7건

다각도 재검(사용자 챌린지) — v0.25.x 2차 심층 리뷰 6건 + 미커버 표면
스윕 7건 병렬:

- [x] U1(high) MCP store 누수 — tools/call마다 cli.Run이 pgxpool 2개를
  열고 닫지 않아 장기 세션에서 풀·고루틴·커넥션 영구 누적(max_connections
  고갈 시 전 명령 마비). Run 완료 후 defer st.Close()
- [x] R2(medium) NFD 토큰 자모 신호 누락 — containsHangul을 nfkc 후
  검사(NFD 분해형 질의의 자모 신호 회수)
- [x] U3(medium) tuning 동등 float 표기 거부 — '0.30'·'60.0'·'.5'가
  문자열 비교로 exit 3이던 것을 수치 비교로 수용
- [x] R4(low) rrfFuse 직접 단위 테스트 — 동률·0값 미기여·k 오버라이드
  격차(비대칭 픽스처) 단얜
- [x] R5(low) ADR-0011 재현 절차 기재(환경·SQL·합성 데이터)
- [x] R3·R6(info) 주석 정정 — topic 신호 이중 가중 의도 명시·정규화 멱급
- [x] 버전 — VERSION·배지 v0.25.3(EN/KO)·plugin.json 동기

### v0.25.4 패치 (2026-11-08) — v0.25.x 패치 루프 4차: 이월 5건 완결

- [x] U2(medium) lazyDumpFile 부분 실패 방어 — 본문 Write 실패 시 해시
  갱신 중단·failed 마킹, close는 사이드카 없이 부분 파일 폐기(체크섬
  통과 오탐 백업 슬롯이 온전한 구 백업을 rotate에서 밀어내던 것 차단)
- [x] U4(low) pattern.go·usage.go 죽은 표면 주석 정정 —
  maintain --distill 플래그는 존재하지 않음(내부 유틸임을 명시)
- [x] U5(low) JSON-RPC 이월 유지 — 오류 응답은 프로토콜 준수 강화라
  별도 스코프(현 주요 클라이언트는 객체 params만 사용, 실발견률 낮음)
- [x] U6(low) MCP 스캐너 초과 줄 생존 — 4MB 상한 유지하되 ErrTooLong
  시 서버 사망 아닌 스킵 생존(재귀 재개) — 4MB 초과 flint_write 한
  번으로 세션 전체 상실 방어
- [x] U7(low) expire-pending dry-run 나열 상한 20(무상한 JSON 방어)
- [x] 버전 — VERSION·배지 v0.25.4(EN/KO)·plugin.json 동기

(v0.25.3 이월 — 처리 완료:
죽은 표면(distill 미실종)·U5 JSON-RPC 오류 응답 부재·U6 스캐너 4MB
상한 서버 사망·U7 expire-pending 무상한 나열.

### v0.26.0 마이너 (2026-11-08) — 프라이버시 실행 (F) + 증류 예산 가시성

다음 마이너 심층 레퍼런스 조사(에이전트: gitleaks 룰셋·age API·
Doltgres 이력·Letta/Mem0·Claude Code memory 포맷) → 권고 F 채택:

- [x] R1 doctor secrets 사후 스캔 — 비밀 패턴 카탈로그 확장(anthropic
  ·gitlab-pat·jwt·google — gitleaks 상위 룰 이식) + doctor secrets
  체크: knowledge 전수 재스캔, 발견 시 warn(패턴명+id만 출력 — 본문
  인용 금지). 저장 시점 스캔의 사각(회피·이후 유입) 폐쇄
- [x] R4(조사 확정) 하드 삭제 ADR-0012 — Doltgres filter-branch
  부재 확인(전수 문서 조사)으로 경로=백업 재구축 확정·구현 별도 이연
- [x] R5(부탐) 증류 예산 가시성 — nudge SessionStart 문구에 pending
  N/상한 M 동봉(PendingMax 재사용·임계 초과 시에만 — Letta 인밴드
  예산 노출 관례, 규칙 기반 정합)
- [x] R2·R3(백업 age 암호화·마스킹) — 이버전 제외: age 의존 추가는
  별도 실증 후(스코프 절제 — R1·R5로 핵심 가치 선공급)
- [x] 리뷰 7건 — ① 무효화 행 제외 재스캔(Fix 해소 경로 정합) ② 스캔
  전열 확장(쓰기 시점과 동일 — tags·entity·domain·origin) ③ sk-ant
  이중 보고 방지(특수 패턴 마스킹 — gitleaks 우선순위 관례, RE2
  lookahead 불가 대응) ④ nudge 초과 계약 정렬(>) ⑥ 전수 구체화
  룰링(Store[][]any 추상화 내 동일 수준) ⑦ sort 임포트 그룹. ⑤
  캐시 공유는 유지보치(24h 캐시와 즉시성 요구가 다름 — 룰링)

### v0.26.1 패치 (2026-11-09) — v0.26.x 패치 루프 1차: 프라이버시 사이드 이펙트 대비

리스크 브레인스토밍(오탐·가용성·권한 호환·메타데이터·성능 5종 식별) 대응:

- [x] #1(최대 리스크) 오탐 완화 — secrets allowlist
  (~/.config/flint/secrets-allowlist.txt, repo 밖·personal-markers 관례
  승계): 등록 문자열을 마스킹 후 스캔 — 쓰기 경고·doctor secrets 모두
  적용. 문서용 예시 토큰·JWT 문서 오탐 해소
- [x] #5 성능·자원 방어 — doctor secrets 행 상한
  (secrets_scan_row_limit, 기본 10000·thresholds 튜닝): 도달 시 부분
  스캔임을 명시 warn('미발견' 거짓 보고 방지) + write 경고 보완 안내
- [x] #3 백업 권한 경화 호환성 — RUNBOOK에 절차 기록(타 사용자 잡
  접근 거부 대응)
- [x] 버전 — VERSION·배지 v0.26.1(EN/KO)·plugin.json 동기

잔여(룰링·문서화로 대응): #2 무효화 가용성 트레이드오프(설계상 감수)·
#4 --reason 메타데이터(의도된 감사 흔적).

### v0.26.2 패치 (2026-11-09) — v0.26.x 패치 루프 2차: 프라이버시 전체 재검수 10건 중 7건

독립 재검수 에이전트(6각도: 우회·오남용·정합·재노출·문서·테스트):

- [x] 발견1(high) sessions 사후 스캔 부재 — 주석은 '세션 summary 포함'
  의도였으나 코드는 knowledge만. sessions(id·agent·summary) 스캔 추가
- [x] 발견2(medium) done 세션 기록 agent 무살균 — InsertBatch 직접
  호출이 sanitizePayload 우회([flint] 마커·비밀 무검사 저장 + 발견1로
  사후 스캔도 없던 이중 사각). SanitizeText+ScanSecrets 적용
- [x] 발견3(medium) allowlist 가시성 — doctor 출력에 'allowlist N항목'
  표시(오타·무단 등록 감지) + RUNBOOK 확인 절차
- [x] 발견4 session_id 무살균 컬럼 — sanitizeFields에 추가
- [x] 발견5 RUNBOOK doctor 검사 목록 갱신(13종)·발견9 재노출 주의
  (read 평문 반환·덤프 평문) 서술 추가
- [x] 발견6 신규 3종 패턴 회귀 테스트·발견7 allowlist 파일 로딩
  테스트(env·주석·빈 줄)
- [x] 발견8 상한 도달 문구 정확화('스캔 미실행')
- [x] 발견10(스캔 비대칭) 룰링: doctor는 조인·write는 필드별 — doctor
  조인이 상위집합 감지라 유지, 주석 명시는 차후
- [x] 버전 — VERSION·배지 v0.26.2(EN/KO)·plugin.json 동기

### v0.27.0 마이너 (2026-11-09) — 세션 지식화: 기억이 자라는 루프

브레인스토밍(신규 발굴+운영 데이터+경쟁 동향) → "유입은 많고 순환이
막힘"(pending 방치·재발견 다수·회상 포화 신호) 판정:

- [x] R1 세션 지식화 — done이 세션 종료 요약에서 지식 후보를 pending
  으로 적립(규칙 기반: 최소 길이·빈도 상한 session_knowledge_days·
  content_hash 중복 방지). LLM 판정 배제(§13)·approve 게이트 승계
- [x] R2 증류 회로 폐쇄 — R1이 distill 마커를 남겨 nudge 예산 문구
  (v0.26.0 R5)와 자연 연결: 세션→지식→nudge→maintain→approve 순환
- [x] R3 `flint import [path] [--commit]` — CLAUDE.md·AGENTS.md·
  .cursorrules(리뷰 5에서 제외 — 불릿 규칙이라 헤딩 분할과 약속 불일치)·
  GEMINI.md·copilot-instructions를 헤딩 단위 pending
  지식으로 변환(dry-run 기본·dedup 멱등·confidence 0.5 승격 경로)
- [x] R4 confidence 감쇠 산출 — doctor stale 체크에 시간 경과 정보
  확장은 이버전 제외(스코프 절제 — R1 순환 데이터가 먼저)
- [x] R5 SKILL.md 최신화 — import·tuning·update·secrets 명령 반영
- [x] 버전 — VERSION·배지 v0.27.0(EN/KO)·plugin.json 동기

### v0.27.1 패치 (2026-11-09) — v0.27.x 패치 루프 1차: 세션 지식화 활성화

- [x] 운영 관측 발견 — v0.27.0 리뷰 1로 '대기 상태'였던 세션 지식화의
  소스 경로 활성: runDone이 Stop 훅 stdin의 compact_summary
  (PostCompact 훅이 이미 생성)를 res.Summary로 연결 — 표준 배선에서
  실제 요약이 지식화에 흐른다. 파싱 실패·부재는 기존 스킵 계약 유지
- [x] 버전 — VERSION·배지 v0.27.1(EN/KO)·plugin.json 동기

### v0.27.2 패치 (2026-11-09) — v0.27.x 패치 루프 2차: 세션 지식화 실질화

다각도 재감사(5에이전트 병렬 + 실증) 24건 발견 중 세션 지식화 주관 8건:

- [x] [정책] v0.27.0 엔트리의 운영 실측치 인용(백분율 2종) 일반 서술화 —
  저장소 최상위 원칙(실측 수치 근거 인용 금지) 위반 수선
- [x] [활성화] PostCompact 표준 경로 연결 — Claude Code 훅 규격상
  compact_summary를 전달하는 훅은 PostCompact뿐(Stop stdin에는 해당
  필드가 없음 — 공식 문서·운영 관측으로 확인). flint compact가 세션
  행 적립과 별개로 maybeSessionKnowledge를 호출한다. Stop stdin 경로는
  타 하네스 폴백으로 유지
- [x] [활성화] stale 종료 경로에서도 지식화 — 조기 return 분기에 갇혀
  미기록 연속 시 요약이 폐기되던 사각 제거
- [x] [계약] tags를 []string{"process"}로 전달 — ":process:" 문자열은
  normalizeTags default 분기에서 빈 문자열로 소거되어 모든 적립 행의
  tags가 빈 값으로 저장되던 결함(done·import 양 경로)
- [x] [계약] 요약 500룬 절단(import 대칭)·살균 후 길이 게이트·topic
  살균(write 경로 대칭)
- [x] [보안] done·import 적립 시점 ScanSecrets 경고 — write 경로 계약
  정렬(경고만, doctor 사후 스캔과 이중 방어)
- [x] [가시성] 적립 실패·마커 실패 stderr 1줄(완전 침묵 관례 비대칭 수선)
- [x] 테스트 — 빈도 상한(당일 skip·경과 적립·파손 fail-open)·-1 끔
  센티널·dedup skip·살균 후 게이트·절단·stale 경로 적립·PostCompact
  적립·stdin 4분기·tags 단언. allowlist 테스트 전역 상태 cleanup 추가
- [x] 리뷰 8건 반영 — 마커 순서(compact 마커 먼저, 지식화 마커 생존)·
  세션 식별 가능할 때만 마커·비밀 경고는 dedup 통과분만(재경고 소음
  방지)·빈 게이트를 임계값 로드 앞으로(불필요 왕복 제거)·origin hook
  명시(기계 적립 식별)·runDone 꼬리 단일화(분기 이중 유지 드리프트
  방지)·warnSecrets 헬퍼 3중 복제 수렴·폴백 주석 정정
- [x] 버전 — VERSION·배지 v0.27.2(EN/KO)·plugin.json 동기

이월(v0.27.3 후보): import 파서 견고화(주석-펜스 상태기 재작성·~~~
펜스)·import 계약(배치 내 dedup·혼재 읽기 불가 보고·origin 정규화·
ExistsHash 오류 계약)·doctor secrets sessions/행 상한 테스트·SKILL 낡은
표현. 수용 룰링: 빈도 상한 경쟁(계측 목적·피해 제한적)·500룬 절단 후
본문 수정 재적립 미반영(문서 언급). 지식 행 session_id 연결은 스키마
무변경 원칙으로 마이너 후보.

### v0.27.3 패치 (2026-11-09) — v0.27.x 패치 루프 3차: import 견고화

재감사 이월(import 주관 — 전건 실증 확인 후 수정):

- [x] 파서 상태기 재작성(stripImportNoise) — ①주석 블록 내부 ```
  라인이 펜스를 열어 이후 섹션 전체를 삼키던 결함(주석 처리 우선·
  펜스 판정은 주석 제거 후 라인만) ②같은 줄 `<!-- 주석 --> 뒷텍스트`의
  inComment 잔류로 이후 문서 무음 삭제 ③줄 중간 인라인 주석 잔존
- [x] ~~~ 펜스 인식(CommonMark) — parseMarkdownSections·주석 제거
  양쪽. 물결 펜스 내부 헤딩 오판 유사 지식("규칙[가짜]") 적립 차단.
  펜스 추적기(fenceTracker) 공유 — 여는/닫는 마커 문자 일치 검사
- [x] 배치 내 dedup(seen 맵) — 같은 파일 2회 지정이 unique 위반
  롤백+exit 0(호출자 성공 오인)이 되던 결함 → skipped 보고
- [x] 혼재 읽기 불가 보고 — 읽기 가능·불가 혼합 시 unreadable 배열을
  dry-run·commit 출력에 포함(부분 유실의 무음 누락 제거). 파일명은
  JSON 인코딩(경로 내 따옴표 파손 방지)
- [x] origin 파일명 정규화 — CLAUDE.md·./CLAUDE.md 재임포트가 동일
  해시로 멱등(경로 표기 차이의 이중 적립 수선)
- [x] ExistsHash 오류 계약 — 조회 실패 항목 skip+stderr 경고(존재
  아님 처리 금지, write 계약 정렬)
- [x] 미확인 플래그 exit 3 — 오타 플래그의 무음 경로화 방지
- [x] 사소 — tuning 키 맵 capacity·코멘트 갱신, v0.27.0 R3 엔트리
  .cursorrules 제거 후주
- [x] 테스트 — 주석-펜스·같은 줄 종결·인라인·물결 펜스·미닫힌 펜스
  EOF·배치 dedup·혼재 보고·origin 멱등·미확인 플래그 9종
- [x] 리뷰 6건 반영 — origin basename→Clean 정규화(디렉터리 다른 동일
  파일명 충돌 방지)·미종결 펜스 개시 라인 본문 중립화(나머지 헤딩
  보존)·dedup 조회 전량 실패 시 실패 보고(거짓 성공 금지·부분 실패는
  dedup_errors 카운트)·같은 줄 주석 2쌍 전부 제거·미확인 플래그
  errLine·exit 1 관례 정렬·tuning 맵 capacity len 기반
- [x] 버전 — VERSION·배지 v0.27.3(EN/KO)·plugin.json 동기

남은 이월: doctor secrets sessions 발견 경로·행 상한 도달 테스트 2종.

### v0.27.4 패치 (2026-11-09) — v0.27.x 패치 루프 4차: doctor secrets 스캔 계약 완결

이월(T4·T5) 처리 — v0.26.2 도입 sessions 사후 스캔의 무테스트 경로:

- [x] 세션 조회 실패 시 거짓 ok 수선 — 기존 serr 침묵 skip은 '세션까지
  스캔했고 미발견'이라는 오탐을 냈다. 지식 경로('지식 조회 실패' warn)와
  대칭인 warn+재시도 안내로 수정
- [x] 테스트 3종 — sessions.summary 발견 경로(PII 최다 필드 — 도입
  취지의 발견 케이스가 무테스트였다)·행 상한 도달 분기(LIMIT 경계,
  부분 스캔의 '미발견 ok' 거짓 보고 방지)·세션 조회 실패 warn
- [x] 리뷰 5건 반영 — 세션 유래 발견의 해소 안내 분기(knowledge
  무효화가 무관 session:N 발견을 유도하지 않게 — done 재종료·DB 정리
  경로 안내)·세션 조회 LIMIT(지식과 동일 상한 — 메모리 무한 증식
  방지)·세션 실패·상한이 이미 잡은 지식 발견을 버리지 않고 격차
  주석(거짓 ok·발견 유실 동시 방지)·writeThresholds 1회 로드·
  README EN 현지화(전체 배포 폐회 → end-to-end release pipeline)
- [x] 버전 — VERSION·배지 v0.27.4(EN/KO)·plugin.json 동기

### v0.28.0 마이너 (2026-11-09) — 전체 보안 점검·경화 (H)

원칙: 아키텍처 틀(단일 바이너리·한 줄 JSON·exit 계약·비간섭 §13·
스키마 v7·Doltgres 로컬 전제)은 불변 — 보안 층만 강화한다. 로컬 단일
사용자 위협 모델을 문서화하고 그 경계 안팎의 위협을 구분한다.

- [x] ① 위협 모델 문서화 — ADR-0013(자산 4종·경계 5면 표·가정·명시적
  비대상 4항: 기기 탈취·로컬 권한 상승·원격 DB 배치·로컬 DoS)
- [x] ② 백업 at-rest 보호 — age 암호화(filippo.io/age v1.2.1·X25519
  키 파일 0600 자동 생성·backup_encrypt 옵트인으로 자동+수동·
  restore/drill 복호 경로·rotate .age 인식·doctor backup-key 체크)·
  마스킹 덤프(--mask — 패턴명 치환·allowlist 원문 보존·사이드카
  미부여+마커 이중 방어) — F②④ 승계 완료
- [x] ③ 전체 코드베이스 보안 감사(4각도 병렬: SQL·파일시스템·네트워크·
  주입/누설 — 발견 22건) — 즉시 수정 16건: doctor 백업 권한 승계·
  EnsureIdentity 원자 쓰기+불량 키 거부·backup --out chmod 강제·자동
  덤프 파일명 PID(병렬 경합)·restore TOCTOU 폐쇄(단일 판독 검증·재생)·
  insertOne 식별자 DB 계층 인용·RankOrder 파라미터화·as-of 해시 엄격
  문자셋·maintain/audit/diff/duplicate 출력 재살균(hot·read와 대칭)·
  SanitizeText Cf 문자(ZWSP·RLO) 제거+마커 대소문자 무시 치환·
  backup 무인자 stdout 경고·unknown command/tool JSON 인코딩
- [x] ④ 업데이트 공급망 — gzip 폭발 상한(해제 스트림 128MiB)·CI 액션
  커밋 SHA 고정(유동 태그 태그 재작성 공격 차단)·루트 permissions
  박탈+잡별 최소. 수용 룰링: 같은 오리진 체크섬(계정 침해 시 무력 —
  서명은 다음 경화 단계)·.mcpb checksums 대상 외(레지스트리 사슬이
  무결)·mcp-publisher 다운로드 TLS 오리진 신뢰
- [x] ⑤ 노출면 최소화 — docker-compose 127.0.0.1 바인딩(0.0.0.0 LAN
  노출 폐쇄, 실행 컨테이너 재생성 적용)·MCP stdio 전용·파괴 연산
  미노출 확인(감사)·doctor backup-key 체크(②)
- [x] ⑥ 정적 보안 게이트 도입 검토 — gosec v2.22.7 시험: 31건 전부
  설계 의도(공개 스킬 파일 0644·하네스 설정 관례·검증된 자가 갱신
  exec·로컬 기본 DSN) — 신규 결함 0. 룰링: make ci 편입 불채택
  (설계 FP 25+건 억제 소음 비용 > 효과 — staticcheck·리뷰 게이트·
  본 감사가 커버)
- [x] ⑦ secrets 패턴 갱신 — AI 생태계 4종 추가(openrouter sk-or-
  specific·github-app gh[opsr]_·groq gsk_·xai) — gitleaks 상위 추적
- [x] 버전 — VERSION·배지 v0.28.0(EN/KO)·plugin.json 동기

룰링: 의존 filippo.io/age v1.2.1 추가(자체 crypto 회피 — PLAN F 명시
후보·감사된 순수 Go 구현)·키 파일 방식(자동 백업 비대화형 호환 —
passphrase는 요청 시)·암호화 기본 OFF 옵트인·사이드카는 암호문 기준
(평문 무결성은 age AEAD가 담보)·수동 백업도 meta backup_encrypt 일관
적용(stdout 파이프는 평문 계약 유지).

### v0.28.1 패치 (2026-11-09) — v0.28.x 패치 루프 1차: doctor 전체 체인 backup-key 배선

- [x] v0.28.0 결함 수선 — backup-key 체크를 doctorTailChecks(--check 허용
  목록)에만 추가하고 전체 실행 체인 append를 누락해 `flint doctor`가
  체크를 실행하지 않았다. 체인 말미(event-volume 뒤)에 삽입
- [x] 재발 방지 — 전체 체인 출력이 doctorTailChecks 전 항목을 포함하는
  정합 테스트(이름 소스 불일치 배선 누락 클래스 봉쇄)
- [x] 버전 — VERSION·배지 v0.28.1(EN/KO)·plugin.json 동기

### v0.28.2 패치 (2026-11-09) — v0.28.x 패치 루프 2차: 백업 덤프 repo 반입 3층 방어

사용자 지시("개인 데이터는 repo 안에서 관리되지 않는다") — 감사 결과
현재 오염은 관측되지 않음(워킹트리·git 전체 이력·가드 실증), 설계 구멍 3건 수선:

- [x] 패키지: `backup --out`이 git 저장소 내부면 stderr 경고(진행 유지
  — 사용자 명시 선택, 사고 예방 고지)
- [x] repo: .gitignore `flint-*.sql*` 패턴 — 자동 명명 덤프·사이드카
  add 원천 차단
- [x] 가드: check_personal.py 덤프 형상 검사 — 자동 명명 파일명·
  `-- flint backup (ADR-0002)` 매직 헤더를 마커와 무관하게 FAIL
  (마커 없는 개인 지식 덤프도 차단). 3층 전부 실증(무시·차단·FAIL)
- [x] RUNBOOK '백업 위치 원칙'·테스트(outInsideGitRepo)·버전 동기

### v0.28.3 패치 (2026-11-09) — v0.28.x 패치 루프 3차: FLINT_HOME 인프라 데이터 루트 통일

사용자 지시("개인 DB 백업은 인프라쪽") — 3곳 산재(백업=~/.flint·키·
마커·캐시=설정 디렉터리)를 2공간 모델로 정리:

- [x] FLINT_HOME(기본 ~/.flint, FLINT_HOME env > FLINT_CONFIG_DIR 폴백)
  = 인프라 데이터 루트 — backup/·backup.key·마커(pending-distill·
  recall-hint)·세션 라벨·캐시(갱신·정비). DB 도커 볼륨과는 독립
  (백업의 존재 이유가 볼륨 손실에서의 생존이므로 볼륨 내부 배치는
  재난복구 자기모순 — 룰링)
- [x] 레퍼런스 대조 — taskwarrior가 같은 계층 구조: 데이터 루트 ~/.task
  (데이터·hooks/·backups/) + 설정 ~/.taskrc 분리 + TASKDATA env 오버라이드
  (XDG는 옵트인). pass도 ~/.password-store 단일 루트. XDG 표준
  (DATA=이식 데이터·STATE=기계 국소 상태)은 다중 홈 세분화 — 단일 홈
  계열 선례(taskwarrior·pass)가 flint 인프라 루트 모델과 일치,
  XDG 배치 원하면 FLINT_HOME env(TASKDATA 관례 승계)
- [x] config 공간(사용자 편집: config.json·allowlist·personal-markers)은
  기존 문서화 경로 유지 — 설정과 데이터의 의미 분리
- [x] dataFile() 단일 헬퍼 — 레거시(configDir) 아티팩트 자동 이전
  (복사 후 원본 제거, 신규 우선, 실패 조용히 재시도)
- [x] 테스트 — FLINT_HOME 오버라이드·이전 수신·레거시 제거·신규 우선
- [x] RUNBOOK·TUNING 경로 문서 갱신·버전 동기

### v0.29.0 마이너 (2026-11-10) — 증류 2막: 승격 보조와 지식 위생

브레인스토밍(사용자 방향 확정 A) — 회로(세션→지식→승격→회상)의 마지막
마찰은 사람 게이트: pending이 며칠씩 방치되는 관측·검수 대화 비용.
규칙 기반 보조로 게이트를 낮춘다(LLM 판정 배제 §13·신뢰 게이트 불변):

- [x] R1 `flint pending` 심사 뷰 — 삼단 추천(승격/병합/만료/보류) +
  사유·명령 안내. 판정 규칙: 만료(경과>PendingAgeDays) > 병합(유사
  active 접두 일치) > 승격(최근 3일+본문 40룬) > 보류
- [x] R2 approve 병합 보조 — 유사 active 존재 시 similar_active 안내 +
  `approve --id N --supersede OLD` 한 번에 승격·무효화·선수 기록
  (maintain --supersede 갱신 경로 승계)
- [x] R3 세션 지식 topic 품질 — "세션[claude]"에 요약 키워드(첫 문장
  구분자·20룬) 반영 — 회상 topic 정합 신호 직결. 콜론은 구분자 제외
  (제목 핵심이 뒤에 오는 형태 보존)
- [x] R4 session_id 연결 — knowledge.session_id(기존 컬럼·스키마
  무변경)에 세션 지식 귀속. 조회 실패는 조용히(연결은 보조)
- [x] 리뷰 10건 반영 — supersede 커밋 Store.Commit 정경로(dolt_commit은
  내장 함수·테이블 아님)·OLD active 사전 확인(rows 무시 무음 성공 금지)·
  유사 근사 topicKey 단일 구현(세션 공통 접두 제거 — 접두 퇴화 오탐
  수정)·N+1 배치·writeSessionRecord id 승계(종료당 1회 조회)·키워드
  최이른 경계·판정 임계 레지스트리화(pending_promote_days·
  pending_promote_min_runes)·origin 출력·엔트리 실측 서술 일반화
- [x] 테스트 — 키워드 추출·topic 품질·session_id·삼단 판정 3종·병합 힌트
- [x] 버전 — VERSION·배지 v0.29.0(EN/KO)·plugin.json 동기

룰링: 추천 전부 규칙 기반(판정 안내일 뿐 — 실행은 approve·maintain)·
R5 confidence 감쇠는 v0.27.0 R4가 이미 반영해 제외(스코프 절제).

### v0.29.1 패치 (2026-11-10) — v0.29.x 패치 루프 1차: 동시성 감사 이월 + 가드 갭 폐쇄

- [x] 세션 종료 감사 커밋(동시성 감사 발견 8) — done 꼬리의 세션 행·
  지식화·meta 카운터 쓰기가 dolt 커밋 없이 종료되던 것을 무관한 다음
  write의 dolt_add -A 전역 스테이징이 흡수해 감사 귀속이 어긋났다.
  종료 경계 커밋 추가(실패 stderr·비간섭 §13)
- [x] 가드 신규 파일 갭 폐쇄(2026-11-10 마커 유입 사고 근원) — add/commit류
  도입 조작은 미추적 신규 파일(--others --exclude-standard)도 스캔.
  기존 갭: add 전 ls-files이 신규 파일을 못 봐 add+commit 일괄 명령이
  스캔을 우회. push는 스테이지 전량 스캔 유지(게시 경계 강도 불변) —
  실증: 신규 마커 파일 FAIL→제거 OK
- [x] 수용 룰링(동시성 감사): 신규 DB 병렬 첫 종료 세션 행 중복(stale
  경고 조기 발화만, 이후 흡수 — low)·마커 반쪽 읽기 넛지 1회 유실 가능
- [x] 버전 — VERSION·배지 v0.29.1(EN/KO)·plugin.json 동기

### v0.30.0 마이너 (2026-11-10) — 계측 2막: hit 품질 근사와 추세 (B 잔여)

- [x] R1 트렌드 — report effect.trend: 최근 7일 vs 직전 7일
  (hit_pct·fallback_pct·top1_pct의 prev/cur/delta). 빈 창 미표시.
  pair_pct 추세는 표본 확대 후(룰링)
- [x] R2 hit 품질 근사 — 스키마 v8(read_events.first_rank additive):
  read 시점 첫 topic 정합 순위 기록(폴백 제외·빈 쿼리 0) →
  exact_pct·top1_pct·mrr 산출. status 스키마 게이트 1~8
- [x] R3 기저율 셔플 재판정 — 표본 미달(read_events_n<200)로 재이연,
  트리거 METRICS §4 명시(세션 경계가 구조적 보정 승계 룰링 유지)
- [x] 테스트 — firstExactRank(순위·대소문자·빈 쿼리)·trend 창 분할·
  빈 창 nil·레거시 v3→v8 연쇄 승격 회귀 갱신
- [x] 리뷰 8건 반영 — 열 재배치 잔여 파싱 2블록 제거(fallback=zero
  오염·ids 미파싱 연쇄 붕괴 — 프로덕션 계측 사망)·추세 페치 창
  max(days,14) 확장(기본 --days 7에서 trend 영구 공란 결함)·품질
  정의 qualityOf 단일화·픽스처 5열 실측 모양·PLAN 앵커 중복 정리·
  e2f 메시지·root 주석 갱신
- [x] 버전 — VERSION·배지 v0.30.0(EN/KO)·plugin.json 동기

### v0.31.0 마이너 (2026-11-10) — 서명 공급망: 릴리스 키 서명 (D)

사용자 방향 확정(키 기반 + GitHub Secrets — keyless는 신뢰 뿌리가
GitHub라 계정 침해 방어가 없고 flint update 직접 검증에 sigstore 의존
트리가 필요해 기각, 수동 서명은 배포 폐회 위반으로 기각 — ADR-0014):

- [x] 검증기 lib/cli/sigverify.go — cosign 키 서명을 Go 표준 라이브러
  리만으로 검증(ECDSA P-256 SPKI+SHA-256+ASN.1 기본·ed25519 순수
  지원, 신규 의존 0). 실제 cosign v2.6 픽스처로 실증
- [x] update 배선 — checksums.txt .sig 자산 검증이 sha256 대조보다
  선행(위조 체크섬은 서명을 못 갖는다). 불일치 즉시 거부
- [x] 공개키 임베드(go:embed lib/cli/cosign.pub) — 빈 파일 = 전환
  모드(.sig 있어도 경고 후 진행, 키 등록 전 배포 차단 없음)
- [x] CI 서명 스텝 — COSIGN_PRIVATE_KEY 시크릿 있을 때 cosign
  sign-blob(tlog-upload=false)→.sig 업로드·cosign-installer SHA 고정
- [x] ADR-0014(전환→하드 검증 이행·키 롤링·비밀키 에이전트 취급 금지)
  ·RUNBOOK §9(사용자 최초 1회 명령 3줄)·테스트 4종
- [x] 리뷰 7건 반영 — 하드 검증 fail-closed 구현(공개키 임베드 시 .sig
  부재·스킴 오류도 거부 — 부재 판정은 자산 목록 기준)·키 롤링 탈출
  경로 --insecure-skip-signature(신뢰 이전은 조용히 일어나지 않는다)·
  CI 서명 후 임베드 공개키로 사후 검증(쌍 불일치 조기 차단)·RUNBOOK
  비밀번호 모순 수정(프롬프트 방식)·배선 테스트(쓰레기 서명 거부·부재
  거부·우회 통과·전환 모드 통과)·주석 경로 정정
- [x] 버전 — VERSION·배지 v0.31.0(EN/KO)·plugin.json 동기

### v0.31.1 패치 (2026-11-10) — 서명 활성화: 공개키 임베드

사용자 키 등록 완료(시크릿 2종·비밀키는 repo 밖 ~/.config/flint 보관)
— 전환 모드 종료, 다음 릴리스부터 하드 검증:

- [x] lib/cli/cosign.pub 임베드(실제 공개키) — 이 릴리스 바이너리부터
  .sig 부재·불일치 거부. CI 사후 검증(cosign verify-blob) 동시 활성
- [x] RUNBOOK §9 키 보관 경로·gitignore 패턴(release.key repo 반입 방지)
- [x] 버전 — VERSION·배지 v0.31.1(EN/KO)·plugin.json 동기

### v0.31.2 패치 (2026-11-10) — 서명 CI 수선: cosign v2.6.0 고정

- [x] v0.31.1 서명 스텝 실패 원인 수선 — cosign-installer v4가 최신
  v3을 설치하는데 v3 sign-blob은 신규 번들 형식(--bundle)을 기본 요구.
  검증기·로컬 툴과 v2.6.0 고정(버전 드리프트 방지)
- [x] 버전 — VERSION·배지 v0.31.2(EN/KO)·plugin.json 동기

### v0.31.3 패치 (2026-11-10) — v0.29~0.31 사후 감사 12건 (3에이전트 병렬)

- [x] [높음·계측] G4 집계 소스 오염 3건 — 페치 창(14일, 추세용)이
  fallback_pct 분모·페어링 루프·pair_pct 분모·chain_pct·recall_led로
  누유. 전부 보고창(inWin) 소스로 교체 — 기본 --days 7에서
  fallback/pair_pct가 절반 이하 과소평가되던 결함
- [x] [중간·보안] pending 출력 재살균 회귀 — v0.28 H③ 패턴(read·hot·
  maintain 계열)을 v0.29 신규 코드가 누락. topic·summary·origin에
  SanitizeText 적용(ESC 주입·Cf 왜곡·[flint] 마커 차단)
- [x] approve --supersede 후검사 — 사전 확인~UPDATE 경합의 0행 갱신
  무음 성공 창 폐쇄·CI 서명 검증→업로드 순서 교환(불일치 .sig 오염
  예방)·서명 실패 태그 재push 복구 RUNBOOK §9 추가
- [x] firstExactRank trim 제거(SQL lower(원q) 정의 정렬)·--as-of·
  --status 조회의 rank 혼입 배제(METRICS §2 계약화)
- [x] .sig URL 자산 목록 캡터(조립 금지)·dataFile 원자 쓰기(tmp+rename)
  ·updateSkipSignature 진입 리셋(방어)·pending actives 1회 조회·
  ValidAt cellToTime 정경로
- [x] 실증 — pending 판정·approve·supersede 종단(운영 DB, 대상 실수
  복구 완료)·v0.31.2 .sig 임베드 키 검증(감사 에이전트)
- [x] 버전 — VERSION·배지 v0.31.3(EN/KO)·plugin.json 동기

### v0.32.0 마이너 (2026-11-10) — MCP 검수 회로: pending·approve 노출

사용자 방향 확정(B) — 세션 안 대기 지식 검수 회로: MCP 도구면에
flint_pending·flint_approve 추가(read·write·hot에 이어 5종).

- [x] `flint pending --json` — 기계 판독 출력(id·verdict·age·origin·
  topic·why·counts·next 안내). MCP·스크립트 소비 단일 소스
- [x] MCP flint_pending(pending --json 위임)·flint_approve(id 검증·
  supersede 동반 조립 — 무효 입력 exit 3 실행 없음). 신뢰 게이트는
  write와 동일 수준(로컬 stdio 단일 사용자)
- [x] 도구 목록 5종 갱신·테스트(조립·검증 3종·매니페스트)·README
- [x] 리뷰 10건 반영 — supersede 키 존재 시 타입 오류 무음 스킵→exit 3
  (문자열 "3" 오타이 승격만 실행되는 의미 변질 차단)·items 빈 경우
  null→[]·valid_at 파싱 불가 행의 터무니 나이·expire 오판 가드(hold)·
  verdict 어휘 promote|merge|expire|hold 통일·어댑터 문서 5종 갱신
  (copilot·openclaw allowlist 정합)·PLAN 앵커 중복 정리·텍스트 모드
  구조화 비용 가드·sliceEq 중복 제거·주석 정정
- [x] 버전 — VERSION·배지 v0.32.0(EN/KO)·plugin.json 동기

### v0.32.1 패치 (2026-11-10) — v0.32.x 패치 루프 1차: MCP 표면 사후 감사 4건

- [x] [높음] pending --json counts·items 모순 수선 — valid_at 파싱 불가
  가드를 jsonMode 한정에서 판정·counts 공통 앞으로(원 kind 집계와 보정
  verdict의 분리 제거 — MCP 소비자의 무효화 유도 경로 차단). 회귀
  테스트로 모순 실증 후 수선(테스트 주도 확인)
- [x] intArgStrict — approve id·supersede의 소수 절삭 금지(7.9→7 오지정
  방지, exit 3). read limit의 관대 절삭과 구분되는 엄격 계약
- [x] 어댑터 문서 15종 도구 표에 pending·approve 행 추가·e2e 주석 갱신
- [x] 버전 — VERSION·배지 v0.32.1(EN/KO)·plugin.json 동기

### v0.33.0 마이너 (2026-11-10) — 지식 위생·이력 관리

브레인스토밍(사용자 방향 확정 B) — 관측: active·invalidated 지식과
meta 키·dolt 커밋이 축적되고 pending은 회전 중. 시스템 건강 — 남은 병목은
이력 위생 도구 부재. 도구 제공이 본질(긴급 정리 아님):

- [x] R1 `maintain --meta-audit` — 키 분류 보고(known/thresholds/
  tuning/daily+나이/unknown). unknown은 사용 중단 후보로 안내
- [x] R2 `maintain --prune-invalidated N` — N일 경과 invalidated 정리
  (dry-run 기본·--commit 확정 — expire-pending 관례 승계). DELETE는
  현재 상태만 — dolt 이력 보존(버전 기억·감사 원칙, ADR-0012 무관)
- [x] R3 doctor meta-hygiene — 미지 키·daily 보존 창+7일 유예 초과 warn
- [x] R4 임계값 invalidated_prune_days(기본 90) 레지스트리 등록
- [x] 패치 루프 감사 반영(병행 v0.31.x~v0.32.x 사후) — v0.32.1 age_days
  보정 유실 회귀 수선(292년 표기·테스트 봉쇄)·hasIDs 루프 inWin·빈 창
  ProfileHint "보류" 완결·마이그레이션 tmp 잔존 청산
- [x] 리뷰 10건 반영 — pb_ 플레이북 카운터 접두 분류(활성 통계 미지
  오탐·삭제 유도 방지)·cutoff timestamptz 파라미터(Doltgres 관례)·
  N 부재 임계값 폴백(invalidated_prune_days)·usage 갱신·doctor 고아
  return 제거·knownMetaKey 단일 소스·quoteJSON 중복 제거·DELETE 실제
  영향 행수 보고(스냅샷 아님)·meta 이중 스캔 제거·죽은 방어 정리
- [x] 테스트 — 분류(known 2·daily·unknown)·prune dry-run/commit/exit·
  실운영에서 meta-audit·prune dry-run·doctor 검증 완료
- [x] 버전 — VERSION·배지 v0.33.0(EN/KO)·plugin.json 동기

### v0.34.0 마이너 (2026-11-11) — 비서 AI 브리지(flint brief)

브레인스토밍(사용자 방향 확정: B 먼저·필요시 C 재론) — flint의 표면은
전부 로컬 전제다(stdio 전송·localhost DB·훅 주입·ADR-0013 단일 사용자).
클라우드 비서 AI는 기존 표면으로 닿을 수 없으므로, 원격 표면 개방 대신
수출면(브리지)을 만든다 — ADR-0015.

- [x] `flint brief [query] [--limit N]` — active_knowledge 뷰만 조회
  (pending·invalidated 비출 금지)·MaskSecrets+SanitizeText 유출 방어
  (마스킹→중립화 순서)·[flint] 데이터 블록 헤더·brief_budget(기본
  3,000·ClampHot 절단)·brief 유입 계측(§13 조용히)
- [x] 회수 경로는 기존 프리미티브 재사용 — write --status pending·
  import→approve(신규 코드 없음, README 브리지 절에 문서화)
- [x] ADR-0015 — 브리지 채택·원격 쓰기 경로 상시 금지·원격 MCP(C)
  재론 조건(실사용 관측·커넥터 특정·노출 감수 + ADR-0013 개정 선행)
- [x] 문서 — README EN/KO: 배지·커맨드 표·브리지 절·"MCP=로컬
  클라이언트" 정직성 한 줄·TUNING.md §5 brief_budget
- [x] 버전 — VERSION·배지 v0.34.0(EN/KO)·plugin.json 동기

### v0.34.1 패치 (2026-11-11) — v0.34.x 패치 루프 1차: brief·브리지 사후 감사 4건

- [x] [높음] brief pending 유출 차단 — active_knowledge 뷰는 invalid_at만
  걸러 pending 행을 포함, BuildRead 경로가 미검수 지식을 기기 밖으로
  내보냈다(ADR-0015 결정4 위반 — 격리 DB 종단 실증). BuildReadStatus
  status='active' 전환으로 이중 조건(status·invalid_at) 차단. 회귀
  TestBriefExcludesPending
- [x] ClampHot 역설정 예산에서 [flint] 경계 헤더 중간 절단 보정(하한=
  헤더 길이) — 회귀 TestBriefHeaderSurvivesTinyBudget·엣지 계약 고정
  회귀 TestBriefLimitContract·TestBriefDashDashGuard
- [x] brief 계측 kind 화이트리스트 추가(RecordUsageCount) — 실DB 계측
  계약 회복(daily additive)
- [x] 브리지 회수 경로 문서 정정 — `write --status pending`은 허구
  (플래그 부재)였다: confidence<conf_floor pending 분류·import는 항상
  pending이 실제 계약(README EN/KO·ADR-0015)
- [x] 프로브 자동생성 산출물 회수 — tests/regression → .superlite/probes
  보관(chore)
- [x] 버전 — VERSION·배지 v0.34.1(EN/KO)·plugin.json 동기

- [x] wire_v07 nudge 레지스트리 오염 수선 — t.Cleanup unregister가
  패키지 registry를 영구 오염해 -count=3 재실행에서 플래키(선결함,
  main 재현). 원 핸들러 복원으로 hermetic화(race 게이트 green)

Follow-up: active_knowledge 뷰 status 무필터로 flint read도 pending
노출(선결함, 이번 범위 밖 — 별도 결정).

### v0.34.2 패치 (2026-11-11) — 전면 설계·기획 리뷰 잔여 처리 + 구조 수렴 에라 선언

**구조 수렴 에라 선언.** 2026-11-11 전면 설계·기획 리뷰(3에이전트 병렬
심층 + 직접 실증 39건)는 개별 수정 사항 너머의 구조 병인 5개를 특정했다:

- ① 계약을 복제 + 주석으로 시행 — 같은 계약이 여러 파일에 복제되고
  동기는 주석에만 달려 있어 드리프트가 상시 발생한다.
- ② 진실의 원천이 산문 — 코드의 진실을 산문 문서가 지정하는 구조라
  정합 유지가 사람 손에 의존한다.
- ③ seam 파편화 — 테스트 seam이 저마다 달라 테스트-프로덕션 괴리가
  벌어진다.
- ④ 계측의 도메인 침식 — 계측 코드가 도메인 로직 사이로 스며들어
  경계를 흐린다.
- ⑤ 기획 문서 사망 — PLAN이 이력 아카이브로만 존재하고 살아 있는
  계획으로 기능하지 않는다.

이에 구조 수렴 에라(v0.37+)를 선언한다 — 로드맵:

| 버전 | 수렴 과제 |
| :--- | :--- |
| v0.37 | render·meta·util 수렴 |
| v0.38 | 패키지 분할 1/3 — 계약층 wire·출력층 render 추출 |
| v0.39 | 패키지 분할 2/3 — knowledge 분리 (v0.39.0 완료) |
| v0.40 | 패키지 분할 3/3 — ops 분리 (v0.40.0 완료) |
| v0.41 | 스키마 진실 단일화·상태 전이 단일화·Store 읽기전용 (v0.41.0 완료) |
| v0.42 | 계측·튜닝 단일 원천 (v0.42.0 완료) |
| v0.43 | seam 동질화 (v0.43.0 완료) |
| v0.44 | PLAN 재무장·표면 계약 6종 → v1.0 게이트 |

신규 신기능은 병인 해소 우선, 트리거 대기 원칙 전면 적용(진행 중
v0.35.0 프로파일·v0.36.0 report-html은 예외).

같은 리뷰의 즉시 수정 분류 잔여 처리:

- [x] R1 tuning 관리 키 단일소스화 — backup_encrypt list 거짓보고·reset
  누락 수선, tuningKeyAll+동질 가드
- [x] R2 usage 문장 레지스트리 파생 — pending 누락 해소+드리프트 가드
- [x] R3 Copilot 자동 등록 오안내 3곳 정정
- [x] R4 doctor 검사 수 16종 통일
- [x] R5 스키마 v8
- [x] R6 TUNING §2 2키
- [x] R7 SKILL MCP 5종
- [x] R8 PLAN 실측치 일반 서술화
- [x] R9 README.ko go install 절
- [x] R10 소규모 — CONTENT 8종·exit 1 서술·배지 기준
- [x] 버전 — VERSION·배지 v0.34.2(EN/KO)·plugin.json 동기

세부: [docs/plans/archive/2026-11/2026-11-11-flint-v0342.spec.md](plans/2026-11-11-flint-v0342.spec.md)

### v0.35.0 마이너 (2026-11-11) — 프로파일 판정 완결: 추천에서 전환까지 (D①)

브레인스토밍(사용자 방향 확정 A·버전 0.35.0 — v0.34.0은 병렬 세션).
profile_hint가 "회상 선도 낮음 — minimal 전환 고려"를 매번 안내만
하던 미완결 회로를 실행 가능 회로로 완결:

- [x] `flint profile` — 현재 프로파일 + 추천(유지/normal/minimal) +
  근거 지표(read_events_n·recall_led_pct·fallback_pct·pair_pct) +
  전환 next 안내(추천==현재면 미표기)
- [x] `flint profile --apply normal|minimal` — 전환(setup --exposure
  재사용 — 기록 경로 단일 소스). 자동 전환 없음(안내·실행 분리)
- [x] 판정 규칙 METRICS §4.5 문서화 — profile_hint와 단일 소스
- [x] 테스트 — 표본 부족 보류(유지)·minimal 추천+next·근거 지표·
  무효 apply/플래그 exit 1·실운영 왕복(전환·복원)
- [x] 버전 — VERSION·배지 v0.35.0(EN/KO)·plugin.json 동기

후보 문서화(사용자 지시 — B·C):
- **report --html (E)**: 다음 마이너 후보 1순위 — read_events 표본이
  쌓여 trend·품질 게이지가 비어 있지 않은 시점이 적기(빈 대시보드 방지)
- **nudge-pending 배선 (G 잔여)**: 세션 시작 nudge에 flint pending
  추천 요약 연결 — 소규모 패치급

### v0.36.0 마이너 (2026-11-11) — report --html: 정적 단일 파일 대시보드 (E)

후보 문서화 1순위(v0.35.0 엔트리) 이행 — E 평가 기준 ①~③ 충족
아키텍처(한 줄 JSON·단일 바이너리와 양립)·프라이버시(로컬 렌더만·
외부 요청 0)·유지 비용(임베드 템플릿·프레임워크·JS 무의존 — Go
html/template 렌더):

- [x] `flint report --html [--out f] [--days N]` — GSM 4목표 카드·hit
  품질(exact/top1/MRR)·7일 추세(전기 대비 ±)·일별 활동 표. 스냅숏
  생성(자동 갱신 없음 — 관측은 report 원천)
- [x] 재설계(사용자 지시 — 리서치 에이전트 스펙 기반): 지표 6종 추가
  (표본 배지 n=·일별 스몰 멀티플 바(read/write/차단)·지식 재고·위생
  행·추세 델타칩(▲▼ 부호+화살표)·G2 dup_pct 병기·profile_hint 라인) —
  4건은 기존 수집 필드 전달 누락분(수집 변경 0)
- [x] 컴포넌트 라이브러리 기반(사용자 지시) — Water.css v2(MIT·22.7KB)
  vendor 임베드(go:embed): 상류 유지보수 토큰·다크모드(prefers-color-
  scheme) 계승, flint 컴포넌트층(카드·히어로·칩·스파크라인·바)은
  water 토큰 위에 얹음. 후보 비교: Pico 83KB(예산 초과)·Tailwind
  컴파일(Node 빌드 의존 — Go 단일 빌드 붕괴) 기각
- [x] 스파크라인(SVG path Go 사전계산 — 템플릿 산술 금지·평평 가드)·
  바 툴팁(네이티브 title)·반응형(≥960 4열/600~959 2열/<600 1열)·
  인쇄 스타일·레이아웃 1080px 상한
- [x] 자기완결 — 외부 리소스 로딩(src/href http·@import) 0 실증.
  수집 체인은 runReport와 동일 소스(HTML 별도 지표 없음)
- [x] 합성 데이터 렌더 실증(공개 산출물 원칙 — 전 요소 구조 단언)·
  예산 48KB(Water 베이스 23KB 포함) — 실렌더 33.6KB
- [x] 테스트 — 스펙 전 요소·칩 방향 반전/빈 창·평평 가드·플래그 배선
  (--out 확장자 보정)·자기완결(네임스페이스 URI 제외 정밀화)
- [x] 사본 3차 개선(사용자 피드백) — 카드 그리드 slop 제거(제품 분석
  레이아웃: Plausible 스트립·Vercel 히어로 인라인·Stripe 타입 규율)→
  jargon 전면 사람말화(과교정)→확정: 간결 지표명 유지+설명·표본은
  native 툴팁. jargon 렌더 부재 회귀 테스트. 단위 정정(hot p95=출력
  rune p95·ms 아님)+토큰 추정 footer(A3). Water.css 제거 — 스펙
  커스텀 CSS로(실렌더 33.7→7.2KB)
- [x] 토큰 경제 조사(사용자 지시) — 수집 후보 A1·A2·B1~B6·금지 C를
  METRICS §4.6 로드맵으로 문서화(구현은 구조 수렴 시리즈와 조율)
- [x] 대시보드 설계 레퍼런스 추가 조사(사용자 지시 — brunch mklab·
  Yellowfin·5of10 2026 가이드·5초 룰) — 반영 4건: 차트-범례 정합(3계열
  그룹 바 — read/write/차단 하루당 묶음·날짜 툴팁)·빈 상태 명시(Trend
  부재 시 '지난주 데이터 부족' 안내)·마이크로 라벨 11→12px(접근성)·
  5초 룰/역피라미드 구조 부합 확인(히어로→스트립→추세→상세)
- [x] 버전 — VERSION·배지 v0.36.0(EN/KO)·plugin.json 동기

### v0.36.1 패치 (2026-11-11) — 리뷰 잔여 구조 스윕 1차 (무충돌 항목)

2026-11-11 전면 리뷰 잔여 항목 중 v0.35.0(프로파일)·v0.36.0
(report-html) 트랙과 파일이 겹치지 않는 것만 선별한 구조 스윕 —
전 항목 '단일 소스화 + 동질 가드' 패턴(v0.34.2 관례 승계):

- [x] R11 doctor 체크 등록표 통합 — 31종 체크 등록이 4곳에 분산된
  것을 CheckSpec 단일 소스로 수렴(전체 체인·--check 허용목록·
  runDoctorCheck 디스패치 파생). --list-checks 6종→16종 전량
  (미노출 결함 회수), st==nil 판정을 등록표 속성(onStoreNil)으로
  명문화
- [x] R12 meta 접근 단일 진입점 — getMetaKV/setMetaKV(원자 upsert)
  신설, SELECT·ON CONFLICT SQL 5곳 복제 해소. read 폴백 카운터
  증분도 원자 경로로(비원자 창 제거)
- [x] R13 util군·계약 위생 — truncateRunes·countPending 수렴,
  maintain·import unknown flag exit 3 통일, nudge 상수 병합,
  maintainSupersede 경합 후견사(0행 갱신 오류화)
- [x] R14 자체 연결 제거 — approve·done·backup이 root가 주입한
  Store를 사용(mcp 풀 누수 사각 폐쇄), quoteJSON·thresholds 로더
  통합
- [x] 버전 — VERSION·배지 v0.36.1(EN/KO)·plugin.json 동기

리넘버 룰링: 스펙 작성 시 v0.34.3 → v0.35.0 선행 병합으로 v0.35.1 →
v0.36.0 선행 병합으로 v0.36.1 (스펙 §2 조항 2회 발동·스펙 파일명은
작성 시점 그대로). report.go·profile.go는 무결(v0.36.0 트랙 충돌 회피). 세부: [docs/plans/archive/2026-11/2026-11-11-flint-v0343.spec.md](plans/2026-11-11-flint-v0343.spec.md)

### v0.37.0 마이너 (2026-11-12) — 구조 수렴 1막: 출력 살균 조크포인트 (render)

구조 수렴 에라(v0.34.2 선언) 1막 — lib/cli 산재 출력 살균 호출을
render 단일 출구로 수렴(패키지 분할 없음, 위임만 — 출력 바이트 불변):

- [x] render.go 신설 — Text(SanitizeText 위임)·Masked(마스킹→살균
  순서 보존, ADR-0015 계약)·Cells(셀 전수 살균·원 슬라이스 갱신)·
  Rows(Cells 후 FormatRows 위임)
- [x] 출력 경로 16곳(cli) 수렴 — read·hot(셀 루프)·maintain·audit·
  diff(구조체 필드)·pending·brief(Text·Masked) 채택
- [x] TestSanitizeTextChokepoint 소스 가드 — lib/ 비테스트 소스의
  SanitizeText 직접 호출을 화이트리스트 밖 탐지 시 위반 파일:줄과
  함께 FAIL(렌더 우회 영구 차단, 병인 ① '계약 복제+주석 시행'의
  출력 부분 해소)
- [x] 명시적 예외 — 입력 살균(write·import·done)과 store 측
  pipeline(lib/store/sanitize.go·pipeline.go)은 화이트리스트 유지
- [x] 버전 — VERSION·배지 v0.37.0(EN/KO)·plugin.json 동기

세부: [docs/plans/archive/2026-11/2026-11-12-flint-v0370.spec.md](plans/2026-11-12-flint-v0370.spec.md)

### v0.38.0 마이너 (2026-11-12) — 패키지 분할 1/3: 계약층 lib/wire · 출력층 lib/render

구조 수렴 에라 2막 — lib/cli(24.8k줄) 3분할의 첫 단위. 이동+별칭만:
시그니처·동작·출력 바이트·exit 코드 전부 불변, cmd/flint/main.go 무변경.

- [x] lib/wire(계약층 신설) — Store 인터페이스·디스패치 Run(Store 공장
  주입형 — cli.newStore seam 관례 승계)·Handler·registry·Register/
  Lookup·usage 생성(usageOrder 조립)·dsn 해결(ResolveDSN — 설정
  디렉터리는 cli marker.go가 wire.ConfigDir 주입)·ErrLine·QueryOnce·
  meta 접근(GetMetaKV/SetMetaKV/BumpMetaKV) 이동
- [x] lib/render(출력층 신설) — render.go(Text·Masked·Cells·Rows)·
  textutil 통합(TruncateRunes·CountPending — pending.go 정의 이동),
  render→wire 단방향 의존
- [x] cli 별칭 호환 — cliwire_alias.go·render_alias.go(var 별칭은
  테스트 seam 교체 관례 유지)로 잔여 ~30파일 무편집 컴파일.
  재배선 1차: 저빈도 5파일만 wire 직접 참조(나머지는 v0.39·v0.40
  이동 시 자연 해소)
- [x] realStore·ensureSchemas cli 잔류 — 메서드 26개 산재로 대폭발
  이동 회피, knowledge/ops 분리 시점 재검
- [x] bridge 가드 — wire.Registered() 집합 ↔ UsageOrder ∪
  HiddenFromUsage 동질(TestRegisteredMatchesUsage — usage 드리프트
  영구 가드의 cli 실등록 연결)
- [x] 버전 — VERSION·배지 v0.38.0(EN/KO)·plugin.json 동기

3분산 룰링(2026-11-12 리뷰 병인 ③): 패키지 3분할을 3릴리스에 분산 —
대폭발 이동의 회귀 위험 > 속도. 갱신 로드맵(구조 수렴 에라 선언의
표를 대체):

| 버전 | 수렴 과제 |
| :--- | :--- |
| v0.37 | render·meta·util 수렴 |
| v0.38 | 패키지 분할 1/3 — 계약층 wire·출력층 render 추출 |
| v0.39 | 패키지 분할 2/3 — knowledge 분리 (v0.39.0 완료) |
| v0.40 | 패키지 분할 3/3 — ops 분리 (v0.40.0 완료) |
| v0.41 | 스키마 진실 단일화·상태 전이 단일화·Store 읽기전용 (v0.41.0 완료) |
| v0.42 | 계측·튜닝 단일 원천 (v0.42.0 완료) |
| v0.43 | seam 동질화 (v0.43.0 완료) |
| v0.44 | PLAN 재무장·표면 계약 6종 → v1.0 게이트 |

세부: [docs/plans/archive/2026-11/2026-11-12-flint-v0380.spec.md](plans/2026-11-12-flint-v0380.spec.md)

### v0.39.0 마이너 (2026-11-12) — 패키지 분할 2/3: 지식 명령층 lib/knowledge

구조 수렴 에라 3분할의 2단위. 이동+별칙만: 시그니처·동작·출력 바이트·
exit 코드 전부 불변, cmd/flint/main.go 무변경.

- [x] lib/knowledge(지식 명령층 신설) — 9커맨드(write·read·hot·
  brief·pending·approve·import·done·compact) 이동·내보내기,
  wire.Register로 계약층 연결(cli 초기 등록 6커맨드의 이중 등록은
  별칭 경유 동일 핸들러 — 무해)
- [x] 공용 헬퍼 wire 이동 — LoadThresholds(LoadThresholdsFn seam·
  ThresholdsKeys·ApplyThresholdsFallback 동반)·BumpReadStatsToMeta
  (LoadReadStats·BumpReadStats 동반)·ReadSessionLabel(SessionLabelFile —
  ConfigDir seam 경유)
- [x] realStore 메서드 cli 추출 파일 3종 — realstore_write.go·
  realstore_read.go·realstore_curation.go로 양쪽 소유분 분리(realStore
  본체는 cli 잔류 — v0.40 ops 분리 시 재검)
- [x] 단일 구현 수렴 — warnSecrets(knowledge 귀속)·cellToInt
  (render.CellToInt 단일화, read 측 복제 삭제)
- [x] CountPending knowledge 귀속 — ops 잔류(status·nudge)는 별칭 소비
- [x] 별칭 호환 — knowledge_alias.go·knowledge_curation_alias.go로
  잔여 cli 파일 무편집 컴파일, bridge 가드
  (TestRegisteredMatchesUsage) 녹색
- [x] 부채(1줄) — 순수함수 복제 4종(cellToTime·quoteJSON·
  backupEncryptOn·metaKV 클로저쌍): knowledge→cli 임포트 사이클
  금지로 임시 복제. v0.41 wire 재편 시 계약층 수렴 예정
- [x] 버전 — VERSION·배지 v0.39.0(EN/KO)·plugin.json 동기

세부: [docs/plans/archive/2026-11/2026-11-12-flint-v0390.spec.md](plans/2026-11-12-flint-v0390.spec.md)

### v0.40.0 마이너 (2026-11-12) — 패키지 분할 3/3: 진단·정리·관측 lib/ops · 3분할 완결

구조 수렴 에라 3분할의 마지막 단위 — 완결. 이동+별칙만: 시그니처·동작·
출력 바이트·exit 코드 전부 불변, cmd/flint/main.go 무변경.

- [x] lib/ops(진단·정리·관측층 신설) — 15파일 3탄 직렬 이동·내보내기:
  1탄 doctor·audit·diff·status·tuning / 2탄 maintain·metrics·marker·
  nudge·sessionhook / 3탄 report·report_html·backup·sigverify·rules_sync
  (+dashboard.tmpl.html 동반). wire.Register로 계약층 연결
- [x] realStore 메서드 cli 추출 파일 3종 — realstore_diag.go(1탄분 —
  조사 결과 메서드 0, 관례 선언만)·realstore_care.go(2탄 6종)·
  realstore_supply.go(3탄 Dump 위임 1종)
- [x] 공용 Store 유틸 wire 이동 — ParseAsOfSpec·ErrInvalidAsOf(asof.go)·
  ExposureProfile(profile.go — ExposureKey 동반) — knowledge·ops seam 소비
- [x] cli composition root 수렴 — root.go init 일원화(커맨드 등록·
  양방향 seam 주입·교체 전파 래퍼), 사용 0 별칭 24종 제거(ops_alias_1·2·3·
  knowledge_alias·cliwire_alias 축소) — cli 잔여는 root·realstore_*·setup·
  update·version·mcp·별칭 최소 22파일
- [x] 의존 방향 실증 — ops→cli 임포트 0(go list), ops→wire·render·
  knowledge 단방향 유지
- [x] 동작 불변 — 전체 스위트 녹색, bridge 가드
  (TestRegisteredMatchesUsage) 녹색
- [x] 버전 — VERSION·배지 v0.40.0(EN/KO)·plugin.json 동기

3분할 완결(v0.38 wire·render / v0.39 knowledge / v0.40 ops). cli 이상
축소·realStore 본체 이동은 v0.43 seam 동질화에서 재검.

세부: [docs/plans/archive/2026-11/2026-11-13-flint-v0400.spec.md](plans/2026-11-13-flint-v0400.spec.md)

### v0.41.0 마이너 (2026-11-12) — 스키마 진실: 게이트 등록표·상태 전이 단일화·쓰기 repo 집중

스키마 진실 단일화 — 스키마·상태·쓰기의 진실 원천을 lib/db로 모은다.
동작·출력·exit 불변(데이터 이전 없음, 구조만).

- [x] 승격 게이트 등록표 — lib/db/schema_gates.go 신설:
  SchemaGates() {Version, Ensure} v2..v8 연속 + LatestSchemaVersion
  단일 소스, cli ensureSchemas 등록표 파생. 가드 3종
  (TestSchemaGatesContiguous·TestSchemaGatesCoverAdmin·
  TestEnsureSchemasUsesGates) — 게이트 함수만 추가하고 등록을
  잊던 무경고 스킵(v0.28.1류) 폐지
- [x] 상태 전이 repo 단일화 — Repo.Invalidate(배치)·Repo.Supersede:
  status·invalid_at·superseded_by 병기(계보 텍스트 마커 이중 모델
  폐지)·rows-affected 경합 후견사 내장(0행 → 오류). maintain·approve
  갱신 경로 전부 Repo 경유
- [x] 쓰기 repo 전용 가드 — Repo.PruneInvalidated·MetaDelete(+잔여
  갱신 위임)으로 Query 통로 쓰기 전량 폐지,
  TestWritesThroughRepoOnly가 lib/{ops,knowledge} 비테스트 소스의
  UPDATE|DELETE|INSERT 토큰을 영구 차단
- [x] hot_view 단일 정의 — 동반 질의(hotKnowledgeIDsSQL·
  pendingValidAtsSQL) 폐지, 뷰 id·valid_at·ingested_at 노출로
  주입=증분 구조 보장(tie-break ", id DESC" 유지)
- [x] 순수함수 수렴 — wire.CellToTime·wire.QuoteJSON 단일 구현,
  로컬 복제 4종 삭제(v0.39 이월 부채 회수)
- [x] 버전 — VERSION·배지 v0.41.0(EN/KO)·plugin.json 동기

세부: [docs/plans/archive/2026-11/2026-11-13-flint-v0411.spec.md](plans/2026-11-13-flint-v0411.spec.md)

### v0.42.0 마이너 (2026-11-12) — 계측·튜닝 단일 원천: 등록표·파생 산출·이벤트 인덱스

지표 수치·보고 값 불변 원칙 아래 계측·튜닝의 진실 원천을 단일화한다.
스키마 v9는 additive(기존 DB 무손상).

- [x] thresholds 키 등록표 — lib/wire/threshold_meta.go 신설:
  ThresholdMeta{Field, Key, Default, Desc} 29키 단일 소스.
  ThresholdsKeys(SQL IN·maintain 감사)·ops TuningKeys(TUNING §2 표
  Default·Consumer·Get)·PruneReadEvents/PruneDaily 재파싱이 전부 등록표
  파생. 가드 TestThresholdRegistrySingleSource(등록표 ↔ 기존 키 집합·
  Parse 인식·Default 동질)·TestNewKeyPropagates(가상 키 등록 시
  4경로 동시 반영) — 키 추가 드리프트(4곳 수동 유지) 폐지
- [x] read_stats blob 폐지·파생 산출 — meta read_stats 누적 blob
  (read-modify-write, 병렬 유실) 폐지: read 1회 쓰기 4→2
  (BumpReads·RecordReadEvent). report 효과·doctor zero-near는
  daily:*·read_events 파생 산출. TestEffectParity가 같은 픽스처에서
  전환 전후 수치 동등을 단언. 운영 DB 잔존 blob은 읽지 않는 무해 잔류
- [x] RecordReadEvent 구조체화 — 위치인자 7개 → db.ReadEvent{Hits,
  Zero, Near, Fallback, Session, IDs, FirstRank}(db·knowledge 인터페이스
  동시 갱신)
- [x] 스키마 v9(이벤트 인덱스, additive) — CREATE INDEX IF NOT EXISTS:
  read_events(ts)·hot_events(ts)·session_starts(ts) + knowledge partial
  (status='pending' WHERE invalid_at IS NULL). 게이트 등록표 v9 등록으로
  연속성 가드 자동 — 실DB 실증
- [x] hotLens/hotPersisted 엣지 수선 — hotLens 절산(상한 초과 세션
  절단) 시 hotPersisted 오프셋 재조정(fr-v0400 기록 엣지),
  TestHotLensTruncateConsistency
- read_calls 보존창 의미 변화 — status read_calls는 누적 blob이 아니라
  events_retention_days 보존창(기본 14일)의 read_events 집계가 된다
- [x] 버전 — VERSION·배지 v0.42.0(EN/KO)·plugin.json 동기

세부: [docs/plans/archive/2026-11/2026-11-13-flint-v0422.spec.md](plans/2026-11-13-flint-v0422.spec.md)

### v0.43.0 마이너 (2026-11-12) — seam 동질화: fake-capability 단언 전수·술어 수렴·wrapper 사유 명시

프로덕션 동작 불변 — 테스트 인프라 동질화가 본체(2026-11-11 리뷰
병인 ③ 해소 완결).

- [x] fake-capability 컴파일 단언 전수 — lib/knowledge/
  fake_parity_test.go(12종)·lib/ops/fake_parity_test.go(5종): 패키지
  capability 인터페이스 전부를 컴파일 시점 `var _ X = (*fake)(nil)`
  단언하고 미구현 메서드는 canned 구현(호출 기록 슬라이스·선택적
  오류 필드)으로 채운다 — 유닛 테스트가 열화 폴백이 아닌 실경로를
  검증함이 컴파일로 보장된다. 소스 스캔 가드 TestFakeParityKnowledge·
  TestFakeParityOps가 인터페이스 발견 목록 ↔ 단언 목록 동질을 강제
  (신규 인터페이스 단언 누락 사각 폐지)
- [x] backupEncryptOn 술어 수렴 — knowledge 로컬 복제·ops 구현을
  wire.BackupEncryptOn 단일 소스로(양쪽 소비자는 var 별칭 위임,
  동작 불변). TestBackupEncryptOnSingle이 func 정의 잔여 0을 소스
  스캔으로 가드
- [x] wrapper seam 사유 명시 — cli→ops 교체 전파 래퍼 8종 중 유지분
  은 각 1줄 사유(소비 cli 테스트가 var 교체로 실행 경로 검증),
  교체 전파가 불필요한 windowedDaily는 직접 참조 전환
- [x] 병인 ③(seam 파편화) 해소 완결 — 구조 수렴 에라 병인 5개 중
  ①②③④ 해소, ⑤(기획 문서 사망 — PLAN 재무장)는 v0.44
- [x] 버전 — VERSION·배지 v0.43.0(EN/KO)·plugin.json 동기

세부: [docs/plans/archive/2026-11/2026-11-13-flint-v0433.spec.md](plans/2026-11-13-flint-v0433.spec.md)

### v0.44.0 마이너 (2026-11-12) — PLAN 재무장·표면 계약 6종·커맨드 표 가드: 병인 ⑤ 해소 완결·v1.0.0 게이트 준비

2026-11-11 전면 리뷰 병인 ⑤(기획 문서 사망 — PLAN이 이력 창고화돼
현행 계약을 기술하지 않음)를 해소한다. 코드 무변경(문서 재구성 +
문서-코드 동기 가드 신규만).

- [x] PLAN 재무장 1512→106줄 — §14 61엔트리 전수 HISTORY 이관(시간순)·
  현행 구조 2페이지(아키텍처·데이터 계약·원칙·비목표)로 재작성.
  구 본문 모순 4지점(서브커맨드 14·MCP 도구 3종·훅 2종·실현된 비목표
  self-update)은 TestPlanRearmedTwoPageContract가 봉쇄
- [x] 표면 계약 문서 6종 — docs/contracts/{cli,mcp,hooks,plugin,
  registry,bridge}.md 각 1페이지(코드 대조 — exit 표·도구 5종·훅 3종·
  배포 경로). 예시는 버전 숫자를 고정하지 않는다
  (TestContractsExamplesNotPinned)
- [x] 커맨드 표 동기 가드 — TestCommandTableMatchesRegistry(README
  EN/KO 커맨드 나열 == wire.Registered, hiddenFromUsage 제외)·
  TestSkillToolListMatchesMCP(SKILL 도구 == MCP 5종). 도입 시점 표
  8커맨드 누락을 포획·수정
- [x] 버전 — VERSION·배지 v0.44.0(EN/KO)·plugin.json 동기

세부: [docs/plans/archive/2026-11/2026-11-13-flint-v0444.spec.md](plans/2026-11-13-flint-v0444.spec.md)

### v1.0.0 메이저 (2026-11-12) — 구조 수렴 완결·게이트 충족: 안정 기조 전환

2026-11-11 전면 리뷰(39건·병인 5개) → 구조 수렴 시리즈 v0.37~v0.44로
전 병인 해소. v1.0.0 게이트(표면 계약 6종·커맨드 표 가드·단일 소스+
동질 가드 체계) 충족 선언.

- [x] 게이트 충족 — usageOrder·thresholds 등록표·쓰기 repo 전용
  AST·살균 조크포인트·fake 동질·커맨드 표·스키마 게이트 연속성
  상시 가드 전부 녹색
- [x] 배지 가드 메이저 점프 허용 — plannedBadgeVersions에 다음
  메이저(v(M+1).0.0) 후보 추가(0.x → 1.0.0 전환 창)
- [x] PLAN v1.0.0 달성 선언 — 다음 마일스톤을 안정 기조(관측 이월
  재판정·이름 충돌 재스윕·트리거 대기 원칙)로 전환
- [x] 버전 — VERSION·배지 v1.0.0(EN/KO)·plugin.json 동기

---


<!-- v0.44.0 T1: 구 PLAN.md §15~§18 부록(기획 시대 기록 보존) -->

## 15. 이연 결정 (트리거 정의됨)

- [x] 주입 예산 상한: hot_view 2주 실측 p95로 확정 → 회수(v0.4): hot_budget meta + p95 계측(TUNING.md §1)
- [ ] 보존·영구삭제: 데이터 축적 후 판단 (진짜 삭제는 filter-branch) — v0.5 백업 보존 정책과 연계
- [x] 백업 포맷 확정: pg_dump 검증 결과에 따름 → 회수(W0): pg_dump 실패, 자체 SQL 덤프 확정(ADR-0002)
- [x] 임계값 수치(pending>50·중복>20·conf<0.8·연속 3세션): 운영 중 조정 → 회수: meta 오버라이드 체계(TUNING.md §2)
- [ ] 검색 품질 고도화(자모 bigram·RRF 융합 랭킹): 제로히트율 20% 초과 +
  read_calls≥50 트리거 도달 시 별도 마일스톤 — v0.18.0 정합: 기간화
  zero_pct 기준 미도달(누적 구 메트릭은 초기 이력 지배로 폐기 판단).
  fallback_pct가 지속 20%+ 시 폴백 tags 확장(R5) 효과 먼저 실측.
  코사인은 Doltgres pgvector 에뮬레이션(2026-09 발표, 부분 지원) 실증
  전까지 보류

## 16. 기술 검증 (v0.1 첫 작업)

- [x] pg_dump 백업(export 방향) 호환 → FAIL(Doltgres 미지원): 자체 SQL 덤프로 대체 확정(ADR-0002)
- [x] dbhub↔Doltgres 실접속 → 대체 회수: flint mcp 서버가 실접속 증명(실DB e2e 통과), dbhub 외부 도구 검증 폐기
- [x] Doltgres AS OF 시간여행 쿼리 → 동작 확인(ADR-0002 전제로 채택)
- [x] flint 이름 전역 충돌 스윕 → 예비 스윕 완료, 비공개 한정 유지(ADR-0003, 공개 전 재스윕 의무 유지)

## 17. 정정 기록·비고

- datasette 대시보드 부활 불가(SQLite 전용) → PG 계열로 변경
- Doltgres 성숙도: 초기 추정과 달리 1.0 달성
- "Doltgres=Postgres 생태" 오류 → 확장 제한 공식 명시
- 전 검증은 웹 검색 다운(429)으로 context7 문서 기반 — 재충전 후 보강 권장

## 18. 명명

- **flint** — 부싯돌. 사람의 볼트가 Obsidian(흑요석)이면 에이전트의 기억은
  flint(부싯돌). 도구를 만드는 돌, 불씨를 내는 돌(`flint hot` = 불붙이기).
- 태그라인: **"version-controlled memory for AI agents"**
- 네이밍 관행 부합: 자연물·광물 계열(DB 계열 관례), 이름=상징·태그라인=기능 분리

### v0.1 구현 확정 정정 (2026-10-21)
- SQL embed 경로 `sql/`→`lib/sqlfs/` (go:embed 상위 접근 제약). 커맨드 진입 `cmd/flint/main.go`
- read `--kind` 필터 제거 — active_knowledge 뷰는 knowledge 단일 테이블
- 스키마 미정의 payload 키(`session.tasks`·`project.note`)는 저장 전 제거 + `warnings`로 고지
- Doltgres 제약 해결: 다중문장 Exec 미지원→문장별 실행, CREATE OR REPLACE 뷰 컬럼 미갱신→simple 프로토콜 DROP+CREATE
- 루프 교훈: wave0(스켈레톤)은 병렬 태스크보다 반드시 선행 (후행 시 스텝 덮어쓰기 사고 — v0.1 인시던트)
