# flint Effect Measurement Standard (v1.2.3)

> English documentation. For a Korean overview, see [README.ko.md](../README.ko.md).

The standard for measuring whether flint "helps" agents. Usage (read · write
call counts) is a necessary condition, not an effect — this document first
defines what counts as help, which signals yield which metrics, and what each
metric can prove (evidence tier). Metric definitions come before code
(Google HEART/GSM convention — design only in Goal→Signal→Metric order;
starting from metrics produces vanity metrics).

## 1. Design principles

1. **Goal→Signal→Metric (GSM)** — define "help" first, map how its success
   shows up as observable behavior (signals), then pick metrics. Never
   measure what is easy to re-measure.
2. **Usage ≠ effect** — call frequency (Engagement) is a necessary condition
   for adoption. Effect is the job of task-success metrics.
3. **Component separation (RAGAS convention)** — flint owns the retrieval
   side only. "Did the agent put its recalls to good use" (generation side)
   is measured as a correlational proxy, never narrated as causation.
4. **Separating observation from evaluation** — instrumentation (event
   recording) and verdicts (computed against a standard) are distinct.
   Synthetic e2e reproduction rate is a "floor, not ceiling".
5. **Set-based interpretation** — no single metric proves helpfulness.
   Gaming vectors (e.g. skipping recalls also raises hit_pct) are blocked by
   interpreting the set together; comparisons happen only within the same
   time window (14 days).

## 2. Definition of "help" and the GSM mapping

| goal | definition | signal (observable behavior) | metrics | source data |
|---|---|---|---|---|
| G1 recall hit | retrieved from memory at the moment of need | reads resolve without empty hands or fallback | `hit_pct` · `near_pct` · fallback rate | daily counters · read_events |
| G2 rediscovery block | prevents the waste of rediscovering what is known | re-recording attempts of the same content end as duplicates | `dup_prevented` (absolute volume) | daily counters |
| G3 asset health | accumulated knowledge stays fresh and reused | active knowledge gets recalled | `reuse_pct` · `dead_pct` | knowledge instrumentation columns |
| G4 recall-first habit | sessions start with recall, not just recording | writes follow reads within a short window | `read_write_pairs` · `pair_pct` | read_events × knowledge |
| G5 minimal injection | bytes entering context stay minimal within budget | measured output bytes across the three surfaces (read · hot · brief) | `bytes.<surface>.bytes_14d` · `avg_bytes` · `cost` (p95 · savings lower bound · injection cost) | daily counters (bytes, v1.1.0) · read_events.bytes · hot_events (v1.2.0) |
| G6 recall fate | recalled knowledge survives over time | still active 7 days after recall | `survived_pct` · `bad_recall_pct` | knowledge_reads × knowledge (schema v10, v1.2.0) |

The dup counter was consumed only through the "defect" frame (dup_pct) until
v0.18. From this document on, the same counter is also read as G2's positive
metric (`dup_prevented`) — a blocked re-recording means rediscovery waste was
saved. The two readings use different keys and derive from the same data
(dup_pct is a behavior-improvement signal, dup_prevented a cumulative-savings
signal — two questions with different directions).

## 3. Evidence tiers

| tier | meaning | metrics | limits |
|---|---|---|---|
| Tier 1 | derived proxy of existing passive data | hit_pct · dup_prevented · reuse_pct · dead_pct | correlation only — says nothing about whether recall "helped" |
| Tier 2 | correlation derived from session/time-linked instrumentation | read_write_pairs · pair_pct · survived_pct · bad_recall_pct | coincidental co-occurrence (written in the same period) mixes in |
| Tier 3 | causation — synthetic scenarios only | e2e reproduction · recall-quality suites | diverges from production distribution (floor, not ceiling) |

Measuring operational causation for Tier 3 (agent feedback surveys · A/B) is
not offered because it conflicts with the non-interference principle (spec
§13 — threshold-based warnings only, no instructions beyond the `[flint]`
boundary). That is a design choice, not a measurement handicap.

## 4. Metric definitions

All windowed metrics use the report `--days` window (default 7 · cap 14),
floor-truncated integer %, zero denominator → 0. The computation code in the
report effect section (the `effect` object) is the single source.

### G1 recall hit

- **hit_pct** = `100 × (1 − Σread_zero / Σread)` — share of windowed reads
  that had results. Sum of daily counters.
- **near_pct** = `100 × Σread_zero_near / Σread` — among zero-hits, the
  share that had near candidates (a signal of retrieval-side headroom). A
  G1-perspective companion of the same summation as `zero_near_pct`.
- **fallback_pct** = fallback-routed events / read_events_n — windowed
  fallback dependence (based on the read_events `fallback` flag). Unlike the
  existing cumulative `fallback_pct` (cumulative key `read_fallback_calls`),
  this is a windowed value.
- **zero_unowned_pct** = `100 × Σzero_reason='unowned' / Σread` — zero-hit
  decomposition (v1.2.0, an applied version of the CRAG 3-way split): among
  zero-hits, the share where active-knowledge count at verdict time was below
  `zero_owned_floor` (default 1), i.e. effectively not held. After reason
  recording, `zero_unowned_pct` + `zero_owned_miss_pct` sums consistently
  with zero_pct (same denominator).
- **zero_owned_miss_pct** = share of `owned_miss` (held but search missed)
  over the same denominator — the ownership split of the same
  retrieval-improvement signal as zero_near_pct.

### G2 rediscovery block

- **dup_prevented** = `Σdup` (window sum, absolute volume) — lines of
  re-recording attempts that ended as duplicates. A lower-bound proxy of
  rediscovery effort saved.

### G3 asset health

- **reuse_pct** = `100 × count(read_count > 0) / count(active)` — share of
  active knowledge referenced at least once. Reference instrumentation (T62)
  counts both search recalls and hot --full injections — it is not a metric
  that counts search reuse only. Point-in-time metric (not a period).
- **dead_pct** = `100 × dead / active`, dead = candidates with read_count 0
  and 30+ days elapsed, excluding hot exposure within the last 30 days. On
  the normal path, injection raises read_count and moves items out of the
  candidate set, so this exclusion is a **measurement-loss consistency
  guard** (filling gaps where only the reference increment failed). 30 days
  is the default constant from the general rationale that distillation and
  dedup cycles run weekly.

### G4 recall-first habit

- **read_write_pairs** — one pair when a knowledge commit lands within 30
  minutes after a session-assigned read (0 < Δ ≤ 30 min) (v0.20.0: session
  boundaries are the primary verdict — writes of other sessions that merely
  overlap in time are excluded as coincidental co-occurrence). Environments
  without session boundaries — older deployments and first instrumentation days —
  use the time-window fallback (v0.19.0 rule). Session intervals are
  `[start_i, start_{i+1})` and the start boundary is recorded by the
  SessionStart hook. Compact · resume re-fires are accepted as conservative
  splits of the same session.
- **pair_pct** = `100 × pairs / read_events_n` — a self-consistent value
  with denominator and numerator from the same source (read_events).
- **recall_led_pct** = among sessions with at least one write, the share
  whose first read precedes the first write (v0.20.0) — the per-session
  observation of "the session started with recall". The measurement version
  of the same perspective as done's recall warning (v0.18.0 R3).
- Automatic label injection (v0.20.5 · B②): the session-start hook leaves
  the current session label and read quietly adopts it when `--session` is
  unspecified — labeling is automated without agent cooperation. Parallel
  sessions are last-writer-wins (auxiliary account).
- **recall_led_pct source switch (v1.2.0)** — the computation source moved
  from read_events to session assignment over knowledge_reads (any kind —
  including hot injections and headline exposure). Hot-led sessions (started
  by injection alone) now also count as recall-led (a design-flaw repair).
  When knowledge_reads is empty (right after an upgrade), the legacy
  read_events assignment computation is inherited (equivalent fallback).
- **hot_read_chain_pct** = within the window, the share of hot exposures
  (knowledge_reads kind='hot', per item) followed by a read of the same
  knowledge within the pairing window (`pair_window_minutes`, default 30
  minutes) (v1.2.0) — the direct chain signal of "injection led to recall".
  Counts both hot --full injections and headline exposure.
- **chain_pct** = among reads that returned ids, the share followed within
  24 hours by (a) the same id returned again in a later read, or (b) a
  same-topic write (v0.20.0 usage chain) — the closest proxy for "something
  usable came back", which hit_pct cannot provide. Returned ids are recorded
  for the top 10 only (storage budget).

### G5 minimal injection (v1.1.0)

- **bytes.<surface>.bytes_14d** = window sum of daily `<kind>_bytes` (read ·
  hot · brief each) — the UTF-8 bytes the three surfaces actually emitted to
  context (read: final output; hot · brief: length 'after' ClampHot
  truncation — measured budget compliance). Instrumented as new daily-counter
  keys without raw-event schema changes, with the same retention window as
  the count metrics.
- **bytes.<surface>.avg_bytes** = bytes_14d / the call-count counter of the
  same window (read · hot · brief each, floor-truncated integer — zero
  denominator 0). Average injection volume per call.
- **status `hot_bytes_14d` · `read_bytes_14d`** — windowed companions of the
  same formulas (hot_budget compliance monitoring — TUNING §1).

### G5 token accounting — the cost object (v1.2.0)

- **cost.<surface>.p95_bytes** — nearest-rank p95 of per-surface output
  bytes. read from the read_events.bytes event distribution (rows recorded
  with bytes>0), hot derived from the per-event sum distribution of hot_events
  injected ids × knowledge summary length, brief has no per-event source and
  reports 0. Under 5 samples → 0 (zero-guard convention).
- **cost.<surface>.mode** — injection-character label: hot is `always-on`
  (injected unconditionally at every session start), read · brief are
  `on-invoke` (output only when called). Interpretation (e.g. budget
  allocation decisions) goes together with this label.
- **cost.savings_lower_bound_bytes** = `Σdup_prevented × average recorded
  summary bytes` (average over all knowledge summary lengths,
  floor-truncated) — a deterministic lower bound of the dedup logic-to-physics
  formula (bytes not stored thanks to duplicate prevention). Label:
  **"savings lower-bound estimate"** — the wording pins it as a lower bound,
  not a counterfactual upper bound.
- **cost.hit_injection_cost_bytes** = `Σhit × average summary bytes` — the
  **"injection cost"** label of recall returns (not a savings claim — the
  cost perspective that recall also spends context, per the investigation
  conclusion).
- The token/dollar conversion prohibition (§5) still applies — bytes are
  reported as-is.

### G6 recall fate (v1.2.0)

- **survived_pct** = among knowledge whose first recall in the window is
  followed by 7 elapsed days (`recallFateDays`), the share still alive
  without invalid_at · superseded_by.
- **bad_recall_pct** = share of superseded/invalidated among the same
  verdict targets. An observation of "what was recalled got dredged out
  later", not "recall was bad" — it also reflects the outcomes of
  invalidation and supersession policy.
- The denominator is the number of verdict-target ids (excluding first
  recalls under 7 days, including absent rows). The source is the
  knowledge_reads × knowledge join (schema v10) — a delayed metric that
  becomes judgeable only as per-item recall history accumulates. Evidence
  tier: **Tier 2 correlation** (§3) — never narrated as causation (§5).

### Instrumentation sources (schema v6 · v7 · v10)

- **read_events** (v6, ids added in v0.20.0): one row per read
  `ts·hits·zero·near·fallback·session·ids`.
- **read_events new columns (v10, v1.2.0)** — `bytes` (output bytes — source
  of p95 · cost) · `zero_reason` ('unowned'|'owned_miss'|'' — zero-hit
  decomposition, zero=1 rows only) · `source` ('hook'|'cli'|'mcp' — channel
  mix observation). Additive.
- **knowledge_reads (new table v10, v1.2.0)**: per-item recall history,
  one row `knowledge_id·ts·session·kind('hot'|'read')` — hot headline
  (non---full) injections are also fully recorded with kind='hot' (the
  shared source for G4 accounting, recall fate, and the hot→read chain).
  Retention follows the same window as read_events (initially 14 days) with
  a separate override `knowledge_reads_retention_days` (TUNING §2). The
  write path's read_count · last_read_at remain demoted derived caches
  (compatibility · performance).
- **session_starts** (v7): one row per SessionStart hook fire `ts·session` —
  the source of session-interval assignment. An internal account with no
  output (§13).
- **hot_events** (v7): one row per hot --full injection `ts·ids` (knowledge
  that survived into the actual output) — the source of the dead_pct
  consistency guard. **--full is a manual path**: the session-start hook
  wiring is headline-only (counts only, no injection), so this table being
  empty is normal in ordinary operation (stated v0.24.0).
- Retention: read_events · session_starts 14 days (same policy as daily
  counters), hot_events 30 days (aligned with the guard window). Pruned on
  rollover. Instrumentation failures pass silently (§13 — losing usage
  logs is better than failing the query); if event-log lookup fails, the
  affected metric degrades to 0/fallback and the report as a whole never
  breaks (the 0-fallback convention of other metrics).
- Personal phrasing and query bodies are never stored (numbers · flags ·
  ids · labels only).
- **Ruling (baseline shuffle deferred · re-deferred at the v0.30.0
  re-ruling)**: the time-shuffled null model for correcting coincidental
  co-occurrence in pairs remains deferred because session-boundary
  assignment structurally serves that role. Re-evaluation trigger for
  residual coincidence (independent occurrences within a session): when
  read_events_n ≥ 200 is observed (currently under the sample — confirm
  with the report's read_events_n).
- **v0.30.0 hit-quality approximation (G1 reinforcement)**:
  read_events.first_rank (first topic-match rank, excluding fallback-routed
  reads · schema v8) yields exact_pct (exact return rate) · top1_pct
  (exact top-1 rate) · mrr (mean reciprocal rank). Ranks inherit the
  RankOrder definition (lower matches) — a rank alone is not all of
  quality, so interpret as a correlational set (per §5). The quality source
  is the normal recall path only — fallback, `--as-of` historical checks,
  and `--status` lookups do not record rank (v0.31.3).
- **v0.30.0 trends (G1 · G4 reinforcement)**: report effect.trend — last
  7 days vs previous 7 days (hit · fallback · top1). If either window has
  zero samples, nothing is shown (no empty-window comparisons). pair_pct
  trends extend after the sample grows.

## 4.5 Profile verdict rules (v0.35.0 — flint profile)

Verdicts share a **single source** with report's profile_hint (no rule
splitting):

- sample floor: read_events_n < zero_near_min_read → "keep (observe first)"
- recall_led_pct >= 50 → keep normal (recall-led majority)
- recall_led_pct < 50 → recommend minimal (shrink hot headline · suppress
  nudge)

The underlying metrics (read_events_n · recall_led_pct · fallback_pct ·
pair_pct) accompany the verdict output so users can verify it (observation
transparency). Switching is `--apply normal|minimal` only — no automatic
switching (guidance and execution stay separate).

## 4.6 Collection candidates (token economy · investigation complete — unimplemented roadmap)

Candidates based on the 2026-11-11 multi-angle investigation (token-economy
focus). Timing and versions are coordinated separately (coexistence with the
structure-convergence series schedule). **v1.2.0 recovered A1 · A2 · B3** —
A1 path separation is knowledge_reads kind tagging ('hot'|'read' full
recording) · A2 is the read_events.bytes column (schema v10 — securing the
derived read_out_avg · out_per_hit sources) · the B3 equivalent is
hot_read_chain_pct. Remaining candidates below (A1 leaves only derived
metrics):

- **A1 remainder — derived budget_fill_pct** — path separation (kind
  tagging) was fully recovered in v1.2.0. Only the derived hot_budget fill
  ratio remains unimplemented
- **A3 token conversion display** — rune→token approximation (Korean ×1.1 +
  others ÷4 · ±30% stated) is report display only. **Already reflected in
  the v0.36.0 dashboard** (footer estimate label)
- **B1 recalled-knowledge age** — just an aggregate of read_events.ids ⋈
  knowledge.ingested_at (zero new instrumentation. For distillation-cycle
  judgment)
- **B2~B6** — write→first-read delay (first_read_at) · injection→recall
  conversion · session length distribution · invalidation growth rate ·
  post-zero retry success rate (all mostly aggregates over existing tables)
- **Prohibited (C)** — reporting absolute saved-token volumes as effect
  (counterfactual estimation) · automatic behavioral triggers off token
  metrics (§13) · collecting total measured harness tokens (out of scope)

## 4.7 Status window semantics (v1.0.10 consistency)

`read_calls` in `flint status` (a cumulative key) switched its computation
source to daily sums over the retention window in v0.42.0 — its meaning is a
sum within the `daily_retention_days` window (default 14 days), not a global
cumulative. The `*_14d`-named fields display the actual window (override
reflected — a defense against false names, v0.24.0). Identical formulas to
the report's windowed metrics, so there is no interpretive difference.

## 4.8 Structured diagnosis (v1.2.0)

Diagnosis is **always included** in report with no separate flag (healthy
silence — an empty array when nothing is wrong). Each item is a one-line
structure of signal + number + criterion source + action (DiagnosisItem),
and four views — default (single-line JSON) · --text (human-readable ·
diagnosis Sentence) · --html (dashboard cards) · --tui (v1.5.0 interactive
browsing) — render from the same source (ReportOutput.Diagnosis, a single
Diagnose() computation; no data splitting between views). --text was added
in v1.2.3 (before that, JSON · HTML 2 views).
Source code: lib/ops/diagnosis.go · design:
docs/plans/archive/2026-11/2026-11-13-flint-v1200.spec.md.

Three criterion layers · eight rules (the metrics column cross-references
the §4 signal→metric mapping):

| layer | signal (rule) | related metrics | tuning keys |
|---|---|---|---|
| A self-reference | hot_budget_saturation / hot_budget_slack (both directions of average hot injection vs hot_budget band) | G5 `bytes.hot.avg_bytes` | `hot_budget` · `diag_hot_budget_high` · `diag_hot_budget_low` |
| A self-reference | hot_bytes_trend (abrupt change between window-half hot_bytes sums) | G5 `hot_bytes` | `diag_trend_pct` |
| A self-reference | hit_cost_exceeds_savings (injection cost > savings lower bound — exceeding does not prove net loss) | G5 `cost` object | none (label contract) |
| B registry inheritance | zero_near_high | G1 `zero_near_pct` | `zero_near_warn_pct` · `zero_near_min_read` |
| B registry inheritance | dead_knowledge | G3 `dead_pct` | `diag_dead_warn` · `dead_days` |
| B registry inheritance | pending_backlog (review backlog) | review queue (operational state) | `pending_age_days` |
| C value default | recall_led_below_goal ('default goal' label — a habit goal, not a personal one) | G4 `recall_led_pct` | `diag_recall_led_goal` |

Layer B inherits reads of existing doctor keys (avoiding duplicate new diag
keys). The full list and defaults of `diag_*` tuning keys: docs/TUNING.md §2.

## 5. Prohibitions

1. Never report read/write volumes as effect (principle 2).
2. Never celebrate hit_pct improving alone — skipping recall shrinks the
   denominator and improves only the number. Interpret together with read
   volume · pair_pct as a set (principle 5).
3. Never narrate Tier 2 correlational metrics as causation (principle 3).
4. Never cite operational measured numbers as "evidence" in this document ·
   specs · commit messages — evidence stays general prose (top-level
   repository principle).
5. Never use effect metrics as triggers for automatic behavior changes
   (blocking · injection boosts) — observation and reporting only (keeping
   §13 non-interference).
6. Never report byte metrics converted into tokens — conversion-coefficient
   estimates breed error and overconfidence. Bytes as-is (v1.1.0 G5).

## 6. Related

- Computation: the `effect` object of `flint report` (single source of the
  §4 definitions).
- History: docs/PLAN.md v0.19.0 · v0.20.0 entries.
- Standard sources: Google HEART/GSM (Rodden et al., 2010) · RAGAS metric
  separation · Mem0 evaluation conventions (same-condition comparison) ·
  the offline-online gap convention (floor, not ceiling) · IR implicit
  feedback (return→usage chain).
