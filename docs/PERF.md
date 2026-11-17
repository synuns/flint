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

Note: every After figure is a measured median at its **own** scale, named
in the cell (the shipped bench scales are smaller than the R30 survey
scales; a number is never quoted without its scale). Per-row comparison
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
