// snapshot.go — the threshold-change accounting circuit (schema v13 A2,
// v1.19.0). Once a day (a 24h meta gate + a 1h in-process cache — the
// maybePruneAfterRead pattern), the current value of EVERY thresholds
// registry key is compared with the key's last snapshot row; changed
// keys only are appended to threshold_snapshots (append-only — the
// fired-motivation column is deferred to the v1.23.0 registry). The
// first expiry on an empty table seeds the full key set as the baseline
// (old_value empty — no prior). Retention follows
// threshold_snapshots_retention_days (the thresholds registry). All
// failures pass quietly (the metering principle) and the DB gate is
// stamped only on success so a failed pass keeps the retry open.
package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// thresholdSnapshotGateKey — the 24h-gate meta key (the last_*_at
// RFC3339 convention; a key separate from the intake-metering times).
const thresholdSnapshotGateKey = "last_threshold_snapshot_at"

// snapshotGateCheckedAt — the in-process gate-recheck cache (the
// pruneGateCheckedAt convention): the unix seconds of the last gate
// check, 0 = unchecked. Shared regardless of Repo instance; shrinks the
// read hot path to one gate SELECT per hour.
var snapshotGateCheckedAt atomic.Int64

// snapshotGateCacheTTL — the in-process gate-cache lifetime (1h, shorter
// than the 24h DB gate — single-shot CLIs and long-lived processes
// re-evaluate the gate at least hourly).
const snapshotGateCacheTTL = time.Hour

// snapshotGateDue — the gate-decision pure function (the pruneGateDue
// twin): false if last parses as RFC3339 and is within 24h; otherwise
// (absent·blank·corrupt·24h elapsed·future-dated) true — the future
// case is the gateStampDue guard (v1.19.1).
func snapshotGateDue(last string, now time.Time) bool {
	if last == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, last)
	if err != nil {
		return true
	}
	return gateStampDue(t, now)
}

// snapshotGateProbe — the gate-cache verdict (the pruneGateProbe twin):
// false within the TTL of the last check (the gate meta SELECT is
// skipped), true on elapsed or first-time (stamping the check time).
func snapshotGateProbe(now time.Time) bool {
	if last := snapshotGateCheckedAt.Load(); last != 0 &&
		now.Sub(time.Unix(last, 0)) < snapshotGateCacheTTL {
		return false
	}
	snapshotGateCheckedAt.Store(now.Unix())
	return true
}

// thresholdValueText — the registry default's text notation (the same
// rules as the TUNING-table Default derivation: int as-is, float64 with
// the minimal-digit format).
func thresholdValueText(def any) string {
	switch d := def.(type) {
	case int:
		return strconv.Itoa(d)
	case float64:
		return strconv.FormatFloat(d, 'f', -1, 64)
	}
	return ""
}

// currentThresholdValues — the current value text of every registry key
// (one meta SELECT via the registry-derived IN path): the meta override
// when present, else the registry default notation. Every key is
// present in the result — the full-key load that seeds the baseline and
// feeds the diff.
func (r *Repo) currentThresholdValues(ctx context.Context) (map[string]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT key, value FROM meta WHERE key = ANY($1)`, wire.ThresholdsKeys())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	overrides := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		overrides[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(overrides))
	for _, m := range wire.ThresholdMetas() {
		if v, ok := overrides[m.Key]; ok {
			out[m.Key] = v
		} else {
			out[m.Key] = thresholdValueText(m.Default)
		}
	}
	return out, nil
}

// lastThresholdSnapshots — each key's latest new_value (one SELECT; the
// per-key max-ts subquery keeps the winner selection in the DB — the
// former ts-ascending full read scaled with the append-only history's
// whole size, not the live key count; the (key, ts DESC) index serves
// the subquery path. Doltgres lacks DISTINCT ON — the correlated max
// subquery is the portable form). An empty table yields an empty map
// (the first-expiry seeding premise).
func (r *Repo) lastThresholdSnapshots(ctx context.Context) (map[string]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT key, new_value FROM threshold_snapshots t
		 WHERE ts = (SELECT max(ts) FROM threshold_snapshots t2 WHERE t2.key = t.key)
		 ORDER BY ts ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// snapshotThresholds — one gated pass: load the full-key current values
// (1 SELECT), load the last snapshot per key (1 SELECT), append the
// changed rows only (INSERT per changed row — 0 on a no-change pass,
// the full key set on the empty-table first expiry), then apply the
// retention delete. Round trips: gate 1 + load 1 + last 1 + INSERT N.
func (r *Repo) snapshotThresholds(ctx context.Context) error {
	current, err := r.currentThresholdValues(ctx)
	if err != nil {
		return err
	}
	last, err := r.lastThresholdSnapshots(ctx)
	if err != nil {
		return err
	}
	for _, m := range wire.ThresholdMetas() {
		prev, seen := last[m.Key]
		if seen && prev == current[m.Key] {
			continue // unchanged — no row (the no-change 0-row contract)
		}
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO threshold_snapshots (key, old_value, new_value)
			 VALUES ($1, $2, $3)`, m.Key, prev, current[m.Key]); err != nil {
			return err
		}
	}
	return r.pruneThresholdSnapshots(ctx)
}

// pruneThresholdSnapshots — deletes snapshot rows past
// threshold_snapshots_retention_days (the thresholds registry; absent·
// corrupt·0 fall back to the DefaultThresholds 90 — the
// thresholdsForPrune convention).
func (r *Repo) pruneThresholdSnapshots(ctx context.Context) error {
	th, _ := r.thresholdsForPrune(ctx, "threshold_snapshots_retention_days")
	cutoff := retentionCutoffDays(th.ThresholdSnapshotsRetentionD,
		store.DefaultThresholds().ThresholdSnapshotsRetentionD, time.Now().UTC())
	_, err := r.pool.Exec(ctx,
		`DELETE FROM threshold_snapshots WHERE ts < $1`, cutoff)
	return err
}

// claimSettleDelay — the post-claim wait before the survival check
// (v1.19.1 CI round 2). Doltgres commits same-row concurrent claims
// LAST-WRITE-WINS without conflict and reports RowsAffected=1 to every
// passer (measured: PR-216 CI — 4 hard-synchronized first-expiry
// passes each saw the claim land and each seeded the baseline, 4×52
// rows; the same race reproduced locally at GOMAXPROCS=4). RowsAffected
// alone is therefore NOT a single-winner verdict on Doltgres. The
// settle window lets every concurrently fired claim COMMIT first; the
// survival SELECT then observes the final value and admits exactly the
// last committer. Once-a-day path — the delay is free. A claim fired
// later than the window would still win over the earlier survivor —
// but it also re-closes the gate for its own pass, so the invariant
// that holds is: at most the LAST claimant of any overlapping burst
// seeds, and non-overlapping claims are serialized by the gate itself.
const claimSettleDelay = 200 * time.Millisecond

// newClaimToken — a unique claim value. The gate value carries the
// claim TOKEN until the pass succeeds and the final RFC3339 stamp
// replaces it — a freshness reader inside the sub-second claim window
// sees an unparseable value, treated as "never stamped" (warn-only,
// self-healing: a leftover token fails snapshotGateDue and the next
// pass retries).
func newClaimToken() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "claim-" + hex.EncodeToString(b[:])
}

// claimThresholdSnapshotGate — the conditional pre-claim of the 24h
// gate (v1.19.1): commit a unique token onto the gate row (absent row:
// INSERT ... DO NOTHING; present row: an UPDATE pinned to the
// previously-read value), let any concurrent burst settle, then admit
// the pass only if the token SURVIVED as the row's final value (the
// last committer wins the LWW burst; every earlier claimant sees its
// token gone and skips). Returns the surviving token, "" when another
// process holds the pass. Errors claim nothing ("" — the quiet path).
func (r *Repo) claimThresholdSnapshotGate(ctx context.Context, last string) string {
	token := newClaimToken()
	if last == "" {
		tag, err := r.pool.Exec(ctx,
			`INSERT INTO meta (key, value) VALUES ($1, $2)
			 ON CONFLICT (key) DO NOTHING`, thresholdSnapshotGateKey, token)
		if err != nil || tag.RowsAffected() != 1 {
			return ""
		}
	} else {
		tag, err := r.pool.Exec(ctx,
			`UPDATE meta SET value = $1 WHERE key = $2 AND value = $3`,
			token, thresholdSnapshotGateKey, last)
		if err != nil || tag.RowsAffected() != 1 {
			return ""
		}
	}
	if sleepCtx(ctx, claimSettleDelay) != nil {
		return ""
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM meta WHERE key = $1 AND value = $2`,
		thresholdSnapshotGateKey, token).Scan(&n); err != nil || n != 1 {
		return ""
	}
	return token
}

// maybeSnapshotThresholds — the read-path hook (the RecordReadEvent
// tail, one line). Gate-checked per hour in-process, gated per day in
// the DB. Concurrency contract (v1.19.1): the stamp write runs BEFORE
// the pass as a commit-then-verify claim (claimThresholdSnapshotGate)
// — of N processes passing an open gate together, only the one whose
// token survives the settle seeds, so the empty-table first expiry
// writes the full key set exactly once. A failed pass rolls the claim
// back to the pre-claim value (the stamp-only-on-success contract
// keeps the retry open); a rollback failure passes quietly — a
// leftover token self-heals (unparseable → due on the next pass). All
// failures pass quietly (the metering principle).
func (r *Repo) maybeSnapshotThresholds(ctx context.Context) {
	if !snapshotGateProbe(time.Now().UTC()) {
		return
	}
	var last string
	_ = r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = $1`, thresholdSnapshotGateKey).Scan(&last)
	if !snapshotGateDue(last, time.Now().UTC()) {
		return
	}
	token := r.claimThresholdSnapshotGate(ctx, last)
	if token == "" {
		return // another process's claim survived the settle — its pass runs
	}
	if err := r.snapshotThresholds(ctx); err != nil {
		// Roll the claim back to the pre-claim state so the failed pass
		// keeps the retry open (stamp-only-on-success). Guarded by the
		// claim token — a later legitimate stamp is never clobbered by
		// this stale rollback.
		if last == "" {
			_, _ = r.pool.Exec(ctx,
				`DELETE FROM meta WHERE key = $1 AND value = $2`,
				thresholdSnapshotGateKey, token)
		} else {
			_, _ = r.pool.Exec(ctx,
				`UPDATE meta SET value = $1 WHERE key = $2 AND value = $3`,
				last, thresholdSnapshotGateKey, token)
		}
		return
	}
	// Finalize the surviving claim to the RFC3339 stamp (guarded by the
	// token; a failure leaves the token, which self-heals as due).
	_, _ = r.pool.Exec(ctx,
		`UPDATE meta SET value = $1 WHERE key = $2 AND value = $3`,
		time.Now().UTC().Format(time.RFC3339), thresholdSnapshotGateKey, token)
}
