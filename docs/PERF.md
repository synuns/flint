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
  document. CI runs small-scale variants; the figures below use the full
  synthetic scales.
- **Honest labeling** — "before" figures are the R30 survey baseline
  (2026-11-16, pre-v1.6.0); "after" figures are measured after the v1.6.0
  fixes. Target values are marked as targets, never as results.

## 2. R30 baseline and v1.6.0 results (synthetic)

| Operation | Scale (synthetic rows) | Before (R30 baseline) | v1.6 target | After (v1.6.0, median of 3) |
|---|---|---|---|---|
| `pending` topic-prefix match (prefix-key map, O(N+M)) | 50k | 255s | < 1s | — measured at task close, see note |
| `import --format jsonl` (ANY($1) batch dedup + chunked batch INSERT + chunk commits) | 5.6k | 25.6s | seconds | — measured at task close, see note |
| `write` batch dedup (ANY($1) batch precheck) | 50k | 137s | seconds | — measured at task close, see note |
| `export --format pam` peak memory churn | 50k | 876MB | ≤ 1/4 | — measured at task close, see note |

Note: the "After" column is filled only with measured medians from the
bench tests below. A dash means "not yet closed out" — a target is never
written into the result column. (v1.6.0 ships the fixes; the close-out
numbers land with the task completion reports.)

Equivalence is part of every performance claim: each fix ships with
verdict/output equivalence regressions (identical results, byte-identical
output where applicable — the PAM checksum reproduction is first priority).

## 3. Known limitations

- **Doltgres does not support pg_trgm**, so `read`'s LIKE matching cannot
  use a trigram index — the scan stays O(rows). This is a known, accepted
  limitation (R30 finding), not a defect; revisit if the engine gains
  pg_trgm. Deferred with the v1.7 candidates (see docs/PLAN.md).

## 4. Reproducing the benchmarks

Reproducing a figure means running the owning bench test against an
isolated DB with a synthetic seed — never a working deployment:

```sh
# isolated Doltgres (own instance, not your working one)
docker compose up -d
export FLINT_TEST_DSN='postgres://flint:flint@localhost:5434/flint?sslmode=disable'

# full-scale benches (median of 3 runs; small-scale variants run in CI)
go test ./lib/knowledge -run '^$' -bench 'Pending|ImportJsonl|Write|FormatPAM' -benchtime 1x -count 3
```

The bench functions live in `lib/knowledge/pending_test.go` ·
`import_jsonl_test.go` · `write_test.go` · `export_pam_test.go` (added in
v1.6.0 alongside the fixes they guard). Run them with `-count 3` and take
the median; a single run is not a reportable figure.
