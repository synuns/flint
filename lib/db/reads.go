// reads.go — item reference metering (schema v4). Increments the
// read_count·last_read_at of read-result·hot-injected items. No new
// indexes (spec §2 — reuses the status index; sequential scan accepted
// at personal scale). The caller (cli) quietly ignores failures.
package db

import (
	"context"
	"fmt"
	"time"
)

// BumpReads increments by 1 the references of the knowledge rows for
// ids (read_count+1, last_read_at=now()). An empty slice is a no-op;
// duplicate ids within the slice are reflected once per row since
// WHERE id=ANY matches per row.
func (r *Repo) BumpReads(ctx context.Context, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET read_count = read_count + 1, last_read_at = now()
		 WHERE id = ANY($1)`, ids); err != nil {
		return fmt.Errorf("bump reads: %w", err)
	}
	return nil
}

// RecordKnowledgeReads leaves one round of per-item recall history in
// knowledge_reads (schema v10 — the source of spec §2.1 G4 alignment·
// §2.6 recall fate). For the full id set (read-returned rows·hot
// injection/headline-exposed rows), writes one row each of
// knowledge_id·session·kind (the 'read'|'hot' surface label) with a
// single INSERT ... SELECT unnest (no query body·summary recorded — the
// no-personal-wording principle). Empty ids is a no-op; an invalid kind
// is an error. This table is a history layer separate from the
// read_count/last_read_at cache path (BumpReads) — BumpReads is
// untouched.
func (r *Repo) RecordKnowledgeReads(ctx context.Context, ids []int, session, kind string) error {
	if len(ids) == 0 {
		return nil
	}
	switch kind {
	case "read", "hot":
	default:
		return fmt.Errorf("record knowledge reads: invalid kind %q", kind)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO knowledge_reads (knowledge_id, session, kind)
		 SELECT u, $2, $3 FROM unnest($1::int[]) AS u`, ids, session, kind); err != nil {
		return fmt.Errorf("record knowledge reads: %w", err)
	}
	return nil
}

// BumpRecallScores is recall reinforcement (schema v12 — recall
// append-only): accrues one recall event per ids row into recall_events
// (INSERT-only — a recall source without write conflicts in multi-device
// synchronization. The old v11 UPDATE knowledge increment is abolished
// — knowledge rows are no longer updated on the recall path). The read
// hit path calls it together with RecordKnowledgeReads (the metering
// convention — failures quietly ignored by the caller, §13). The decay
// effective score derives from events (an aggregate join — the ranking
// switch belongs to the knowledge package) and is a layer separate from
// the read_count cache (BumpReads)·the per-item history
// (knowledge_reads). Empty slice no-op; duplicate ids once per row
// (DISTINCT — inheriting the old UPDATE id=ANY contract).
func (r *Repo) BumpRecallScores(ctx context.Context, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO recall_events (knowledge_id)
		 SELECT DISTINCT u FROM unnest($1::int[]) AS u`, ids); err != nil {
		return fmt.Errorf("bump recall scores: %w", err)
	}
	return nil
}

// PruneRecallEvents is the single path DELETEing recall events past the
// retention horizon (the growth cap of the schema v12 append-only
// source — R42-D). maintain --prune-invalidated --commit delegates to
// it with recall_event_retention_days (default 90, meta override).
// Deleting 0 rows is normal (idempotent rerun). Dolt absorption:
// recall_events is an audit-staging target (auditStageTables), so the
// same-path audit commit absorbs it immediately (unlike metering
// tables, no separate recovery needed). Ranking consistency
// (EventEffectiveScore = count−rate×days_since(last)): with the default
// rate 1.0, only rows whose effective score vanishes at the 0 floor
// have count ≤ elapsed days (even a high-frequency row whose last
// recall is 90+ days past retains an effective score if its count is
// larger — pruning its events would change the ranking). Rows with
// recent recalls get the window-cap effect where only events inside the
// retention window count (an intended growth boundary).
func (r *Repo) PruneRecallEvents(ctx context.Context, before time.Time) (int64, error) {
	n, err := transitionExecFn(ctx, r,
		`DELETE FROM recall_events WHERE recalled_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("prune_recall_events: %w", err)
	}
	return n, nil
}

// RecallReplay — one row of migration score replay (import --format
// jsonl — R42-A): the accrued row's id and the migration source's
// score·last recall time (zero value = now() fallback).
type RecallReplay struct {
	ID           int
	Score        int
	LastRecalled time.Time
}

// ReplayRecallEvents — replays the recall history of migrated rows as
// synthetic events (import --format jsonl only — R42-A). In v12 the 2
// knowledge columns recall_score·last_recalled_at are dropped and
// cannot transfer as INSERT columns, so after the InsertBatch commit
// the (score, last) pairs carried by the jsonl are accrued into
// recall_events with the same formula as the EnsureSchemaV12 backfill
// (one row per score count, recalled_at = COALESCE(last, now())) — the
// derived ranking (cnt·last_at) is restored equivalently to the source
// DB. Rows with score<=0 skip replay (same as the backfill WHERE
// convention); an empty slice·all-zero is a no-op. The accrued portion
// is automatically absorbed by the recall_events staging of the audit
// commit (Commit) (auditStageTables).
func (r *Repo) ReplayRecallEvents(ctx context.Context, items []RecallReplay) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int, 0, len(items))
	scores := make([]int, 0, len(items))
	lasts := make([]*time.Time, 0, len(items))
	for _, it := range items {
		if it.ID <= 0 || it.Score <= 0 {
			continue // score 0·key-absent rows skip replay (source never recalled)
		}
		ids = append(ids, it.ID)
		scores = append(scores, it.Score)
		var last *time.Time
		if !it.LastRecalled.IsZero() {
			t := it.LastRecalled.UTC()
			last = &t
		}
		lasts = append(lasts, last)
	}
	if len(ids) == 0 {
		return nil
	}
	// unnest 3-array + generate_series — the same implicit lateral
	// pattern as the backfill (FROM knowledge k, generate_series(1,
	// k.recall_score) g).
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO recall_events (knowledge_id, recalled_at)
		 SELECT u.id, COALESCE(u.last, now())
		 FROM unnest($1::int[], $2::int[], $3::timestamptz[]) AS u(id, score, last),
		      generate_series(1, u.score) g
		 WHERE u.score > 0`, ids, scores, lasts); err != nil {
		return fmt.Errorf("replay recall events: %w", err)
	}
	return nil
}
