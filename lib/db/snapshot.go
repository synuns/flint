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

const thresholdSnapshotGateKey = "last_threshold_snapshot_at"

var snapshotGateCheckedAt atomic.Int64

const snapshotGateCacheTTL = time.Hour

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

func snapshotGateProbe(now time.Time) bool {
	if last := snapshotGateCheckedAt.Load(); last != 0 &&
		now.Sub(time.Unix(last, 0)) < snapshotGateCacheTTL {
		return false
	}
	snapshotGateCheckedAt.Store(now.Unix())
	return true
}

func thresholdValueText(def any) string {
	switch d := def.(type) {
	case int:
		return strconv.Itoa(d)
	case float64:
		return strconv.FormatFloat(d, 'f', -1, 64)
	}
	return ""
}

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

func (r *Repo) lastThresholdSnapshots(ctx context.Context) (map[string]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT t.key, t.new_value FROM threshold_snapshots t
		 JOIN (SELECT key, max(ts) AS mts FROM threshold_snapshots GROUP BY key) m
		   ON m.key = t.key AND m.mts = t.ts
		 ORDER BY t.ts ASC`)
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
			continue
		}
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO threshold_snapshots (key, old_value, new_value)
			 VALUES ($1, $2, $3)`, m.Key, prev, current[m.Key]); err != nil {
			return err
		}
	}
	return r.pruneThresholdSnapshots(ctx)
}

func (r *Repo) pruneThresholdSnapshots(ctx context.Context) error {
	th, _ := r.thresholdsForPrune(ctx, "threshold_snapshots_retention_days")
	cutoff := retentionCutoffDays(th.ThresholdSnapshotsRetentionD,
		store.DefaultThresholds().ThresholdSnapshotsRetentionD, time.Now().UTC())
	_, err := r.pool.Exec(ctx,
		`DELETE FROM threshold_snapshots WHERE ts < $1`, cutoff)
	return err
}

const claimSettleDelay = 200 * time.Millisecond

func newClaimToken() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "claim-" + hex.EncodeToString(b[:])
}

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
		return
	}
	if err := r.snapshotThresholds(ctx); err != nil {

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

	_, _ = r.pool.Exec(ctx,
		`UPDATE meta SET value = $1 WHERE key = $2 AND value = $3`,
		time.Now().UTC().Format(time.RFC3339), thresholdSnapshotGateKey, token)
}
