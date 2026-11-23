package db

import (
	"context"
	"fmt"
)

func (r *Repo) UpdatePendingSummary(ctx context.Context, id int, summary string) (int64, error) {
	ct, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET summary = $2
		 WHERE id = $1 AND status = 'pending' AND invalid_at IS NULL`, id, summary)
	if err != nil {
		return 0, fmt.Errorf("update pending summary: %w", err)
	}
	return ct.RowsAffected(), nil
}
