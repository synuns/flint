# Performance — measurement conventions and benchmarks

> English documentation. For a Korean overview, see [README.ko.md](../README.ko.md).

This document is the single source for flint performance claims. Every
number here comes from a **synthetic** benchmark on an isolated database —
the repo never records production or personal data (see the repository
top-level principle). No number may be quoted without its scale and
convention.

## 1. Measurement conventions

- **Isolated database** — a dedicated Doltgres instance (docker compose),
  never a working deployment. The bench starts from a clean state.
- **Synthetic seed** — generated rows only (`data/seed.jsonl` conventions);
  no real project names, paths, or content.
- **Median of 3** — each figure is the median of three runs; single-run
  outliers are not reported.
- **Benchmarks live next to the code** — Go bench tests in the owning
  package (`lib/knowledge`), so a regression fails in `go test`, not in a
  document. CI runs small-scale variants; each figure below names its own
  scale (bench scales can differ from the R30 survey scales — a number is
  never quoted without its scale).
- **Honest labeling** — "before" figures are the R30 survey baseline
  (2026-11-16, pre-v1.6.0); "after" figures are measured after the v1.6.0
  fixes. Target values are marked as targets, never as results.

## 2. R30 baseline and v1.6.0 results (synthetic)

| Operation | Scale (synthetic rows) | Before (R30 baseline) | v1.6 target | After (v1.6.0, median of 3) |
|---|---|---|---|---|
| `pending` topic-prefix match (prefix-key map, O(N+M)) | 50k | 255s | < 1s | 0.06s — live CLI, 10k active + 400 pending (index lookup ≈0.5µs/row vs 2.2ms/row scan at 5k bench scale) |
| `import --format jsonl` (ANY($1) batch dedup + chunked batch INSERT + chunk commits) | 5.6k | 25.6s | seconds | 6.1s — real-DB bench, 5.6k rows (`TestImportBatchBenchRealSlow`) |
| `write` batch dedup (ANY($1) batch precheck) | 50k | 137s | seconds | 5.2s — real-DB bench, 5k-line batch (`TestWriteBatchDedupRealSlow`) |
| `export --format pam` peak memory churn | 50k | 876MB | ≤ 1/4 | 47.3MB — in-memory bench, 20k rows (`BenchmarkFormatPAM`, B/op) |
| `hot --full` ranking query (recall_events derived join, schema v12) | 100 knowledge / 10k events | 9.15ms p95 (v11 direct columns, median of 3) | < 2× v11 | 16.37ms p95 — real-DB bench, 1.79× v11 (`TestHotDerivedRankingBenchRealSlow`) |
| `hot --full` ranking after retention prune (`recall_event_retention_days` 90, v1.9.1 R42-D) | 100 knowledge / 100k accumulated events → ~9.9k retained | 54.23ms p95 (same derived join on the accumulated state, median of 3) | < 2× v11 | 12.29ms p95, 1.06× v11 — real-DB bench (`TestHotDerivedRankingAfterPruneBenchRealSlow`) |

Note: every After figure is a measured median at its **own** scale, named
in the cell (the shipped bench scales are smaller than the R30 survey
scales; a number is never quoted without its scale). The v1.9.0 derived
ranking row compares the schema v12 `recall_events` aggregate join (count +
max per knowledge, index `idx_recall_events_id_time`) against the dropped
v11 `knowledge.recall_score`/`last_recalled_at` direct columns, reproduced
as a synthetic table with the identical distribution (`bench_k11`); the
"before" figure is that baseline, not an R30 survey number. The v1.9.1
retention-prune row seeds 100k events spread over 0–900 days (long-horizon
accumulation), prunes to the 90-day retention window (`db.Repo.
PruneRecallEvents`, wired into `maintain --prune-invalidated --commit`) and
compares the post-prune derived join against a v11 mirror rebuilt from the
retained events. The prune is the structural growth cap for the append-only
source: without it the ratio keeps climbing with total events (measured
2.33× at ~50k retained in an intermediate run of the same bench — the < 2×
contract holds at the retained scale stated in the row, and heavier arrival
rates keep a larger window). Scale statement, not a universal bound.
Per-row comparison
for the churn row: 17.5KB/row (876MB @50k) → 2.4KB/row (47.3MB @20k),
a ~7.4× cut — the 50k linear extrapolation ≈118MB sits under the
≤219MB (1/4) target, but the extrapolation is marked as such, not
measured. Verdict/output equivalence: the pending verdict output was
additionally verified byte-identical old-vs-new on the 10k+400 dataset,
and the PAM checksum reproduces the pre-optimization value on identical
rows (regression tests in the owning packages).

Equivalence is part of every performance claim: each fix ships with
verdict/output equivalence regressions (identical results, byte-identical
output where applicable — the PAM checksum reproduction is first priority).

## 2b. v1.16.0 search-perf results (synthetic)

Fixture: an isolated scratch DB on the compose Doltgres instance
(`flint_perf_v1160`, dropped after the run) — 2,000 synthetic knowledge
rows (seed-1160 generator), 40k `read_events` / 60k `knowledge_reads` /
2k `hot_events` / 100 `session_starts` spread over 7 days. The
"before" binary is the v1.14.1 source (`git archive` of the release
merge) built and run against the identical fixture; the "after" binary
is the v1.16.0 worktree build. Each figure is the median of 3 runs
(read: p95 over 40 CLI invocations per run, rounds interleaved across
binaries; profile: single-invocation wall-clock, interleaved).

| Operation | Scale (synthetic) | Before (v1.14.1, median of 3) | After (v1.16.0, median of 3) |
|---|---|---|---|
| `read` 3-token phrase query (`--limit 50`) with the in-window token rerank (T1) | 2k knowledge rows | 184.3ms p95 | 167.4ms p95 — round ranges overlap (before 181–219ms, after 154–199ms): within run-to-run noise, i.e. **no measurable rerank cost** (the rerank is O(window) in-memory; the SQL LIMIT and scan are untouched) |
| `profile` wall-clock — `collectEffect` 2-phase parallel collection (T2) | 2k knowledge / 40k read_events / 60k knowledge_reads / 2k hot_events | 7.952s | 7.735s (−2.7%) — ranges overlap (before 7.46–8.14s, after 7.51–8.07s): the win is structurally bounded — the 5 non-dependent SELECTs now overlap their round-trips, while app-side row processing dominates at this scale |

Read quality is the actual T1 deliverable and is not a PERF number:
the rerank changes the within-window order (spec-mandated improvement,
not byte-identical), guarded by the owning-package regression tests.

### v1.21.0 A6 scan p95 (synthetic)

Fixture: an isolated scratch DB on the compose Doltgres instance
(`flint_perf_v1210`, dropped after the run) — 2,000 synthetic
knowledge rows (md5-hashed unique bodies). The
measured operations are the two A6-metered scans themselves
(entry-to-return, the same spans `scan_stats` samples): the write-path
echo scan (`EchoScan`) and the zero-hit read suggestion scan
(`SuggestSimilarWithScanMax` over the `BuildSuggestScan` candidate
set). 40 calls per kind per run, p95 nearest-rank ceil(0.95n)-1
(HotP95), median of 3 runs:

| Scan (synthetic 2k rows) | p95 (median of 3 runs) |
|---|---|
| `EchoScan` (write path) | 45.9ms (runs 43.6–49.3ms) |
| `SuggestSimilarWithScanMax` (zero-hit read) | 9.1ms (runs 7.5–12.1ms) |

The echo scan is the costlier path by construction — it scores all
scanned rows against the incoming topic+summary on both gates (Dice
multiset + topic-only), while the suggestion scan is one pass with a
top-N cut. Both are bounded by `suggest_scan_cap` (5000 rows) and are
suggestion-only (never block the write/read outcome).

Exposure (v1.21.1): the samples persist at the existing hot_stats
commit point AND at the write/read command exits (a one-shot CLI
process otherwise dropped them), and `flint status` surfaces the
cumulative per-kind view (`scan_echo_calls` · `scan_echo_p95_us` ·
`scan_suggest_calls` · `scan_suggest_p95_us`) — every surface, not a
single channel.

## 3. Known limitations

- **Doltgres does not support pg_trgm**, so `read`'s LIKE matching cannot
  use a trigram index — the scan stays O(rows). This is a known, accepted
  limitation (R30 finding), not a defect; revisit if the engine gains
  pg_trgm. Deferred with the v1.7 candidates (see docs/PLAN.md).
  Rechecked 2026-11 (v1.18.0): the Doltgres 1.0 release still ships no
  pg_trgm — the LIKE scan stays.

## 4. Reproducing the benchmarks

Reproducing a figure means running the owning bench test against an
isolated DB with a synthetic seed — never a working deployment:

```sh
# isolated Doltgres (own instance, not your working one)
docker compose up -d
export FLINT_TEST_DSN='postgres://flint:flint@localhost:5434/flint?sslmode=disable'

# in-memory/quick benches (median of 3 runs; small-scale variants run in CI)
go test ./lib/knowledge -run '^$' -bench 'Pending|Write|FormatPAM' -benchtime 1x -count 3

# real-DB regression benches (the RealSlow suite — one pass each, -count=1;
# the import and write dedup figures in §2 are RealSlow tests, not go benches)
go test ./lib/knowledge -run 'RealSlow' -count=1 -v
```

The bench functions live in `lib/knowledge/pending_test.go` ·
`write_test.go` · `export_pam_test.go`; the real-DB figures in §2 come
from the `RealSlow` tests in `import_jsonl_test.go` (import dedup) and
`write_test.go` (write batch dedup), all added in v1.6.0 alongside the
fixes they guard. Run quick benches with `-count 3` and take the median;
a single run is not a reportable figure.
