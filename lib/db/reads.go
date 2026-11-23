package db

import (
	"context"
	"fmt"
	"time"
)

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

func (r *Repo) PruneRecallEvents(ctx context.Context, before time.Time) (int64, error) {
	n, err := transitionExecFn(ctx, r,
		`DELETE FROM recall_events WHERE recalled_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("prune_recall_events: %w", err)
	}
	return n, nil
}

type RecallReplay struct {
	ID           int
	Score        int
	LastRecalled time.Time
}

func (r *Repo) ReplayRecallEvents(ctx context.Context, items []RecallReplay) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int, 0, len(items))
	scores := make([]int, 0, len(items))
	lasts := make([]*time.Time, 0, len(items))
	for _, it := range items {
		if it.ID <= 0 || it.Score <= 0 {
			continue
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
