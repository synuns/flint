# Write boundary contract (v1.37.0 frozen)

## Scope

This document records the existing write-boundary implementation only —
its positions and contracts. It defines no new defense mechanism. If code
and document diverge, that is drift, not a specification change, and
`internal/doclint/write_boundary_test.go` (`TestWriteBoundaryDoc`,
`TestWriteBoundaryQueryOnly`) goes red to pin it.

## Sanitization positions

| Position | Function | Contract |
|---|---|---|
| write input | `SanitizePayload` in `lib/knowledge/write.go` | applies `SanitizeText` to `sanitizeFields` (`topic`, `summary`, `entity`, `agent`, `name`, `session_id`, `conditions`, `source_path`) and emits `store.ScanSecrets` warnings |
| single value | `SanitizeText` in `lib/store/sanitize.go` | strips control and Cf (format) runes; folds case-insensitive literal `[flint]` markers to one space via `replaceMarkerFold` |
| read output | `render.Text` in `lib/render/render.go` | passthrough to `store.SanitizeText` — all rendered read output passes this chokepoint |
| masked read output | `render.Masked`, `render.MaskedStrict` in `lib/render/render.go` | `store.SanitizeText` plus `store.MaskSecrets` / `store.MaskSecretsStrict` |

Sanitization is applied at write time (stored text is already clean) and
again at read time (`render.Text` chokepoint). Neither layer is optional —
restore is the one documented exception (below).

## `[flint]` marker = secondary mitigation

The case-insensitive `[flint]` marker folding (`replaceMarkerFold` in
`lib/store/sanitize.go`) is a secondary mitigation. It removes look-alike
boundary markers from stored and rendered text so a payload cannot forge
flint's own output framing. It is not the primary write boundary and does
not replace the sanitization or review positions above.

## K2 — no unverified promotion

Pending rows never become active automatically:

- verdicts produced by `pendingVerdict` in `lib/knowledge/pending.go`
  (`expire`, `merge`, `promote`, `hold`) are advisory — they print review
  suggestions and never mutate state.
- the only promotion entrance is `flint approve --id` (`RunApprove` in
  `lib/knowledge/approve.go`); a replacement requires an explicit
  `--supersede` different from `--id`.
- `store.Classify` in `lib/store/pipeline.go` assigns the initial
  pending/active state only — confidence below the floor lands as
  `pending` and stays there until an approve call. No code path raises
  confidence or promotes without that call.

## Query-only entrance

`lib/query/read.go` is read-only: its SQL contains no `INSERT`, `UPDATE`
or `DELETE`. Beyond that SQL layer, the read command has exactly one
sanctioned state change: gap-candidate accrual. On repeated owned misses
(`zero_reason = owned_miss`) the read entrance calls
`maybeAccrueGapCandidate` in `lib/knowledge/write.go`, which records a
`pending` gap-candidate row with the query normalized through
`NormalizeGapQuery` (NFKC · truncation · `render.Text` sanitization) and
a per-day cap (`gap_daily_max`). The candidate lands as `pending` and is
governed by the same K2 promotion boundary as any other write — it never
becomes active by itself. No other read-path code path inserts, updates
or deletes knowledge rows; any further state change must go through a
write entrance (`lib/knowledge/write.go`) or a maintenance entrance, which
run the sanitization and review positions documented above — with one
documented exception: restore, below.

## Restore exception — dump replay skips payload sanitization

`RestoreFn` in `lib/ops/backup.go` replays dump SQL as-is:
`SanitizePayload` is not called on the restore path, so stored text is
not guaranteed clean for restored rows. The integrity sidecar is an
unsigned sha256 checksum (`backup.VerifyBytes` in `lib/ops/backup.go`) —
it detects accidental corruption only and does not authenticate the
payload, so a tampered dump replays as-is. The defense for restored rows
stays at the read-path chokepoints (`render.Text`, masked variants)
documented above.
