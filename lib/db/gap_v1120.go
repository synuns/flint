// gap_v1120.go — v1.12.0 T3: the gap-candidate pending-summary refresh.
// The single db.Repo path of the knowledge-table summary UPDATE (the
// writes_through_repo guard scope — write SQL literals live only in the
// db.Repo paths).
package db

import (
	"context"
	"fmt"
)

// UpdatePendingSummary — refreshes a pending knowledge row's summary in
// place (the gap-candidate N update — an UPDATE, not a supersede: the
// row's state·lineage are untouched). The WHERE is pending-only: an
// active·invalidated·absent row updates 0 rows — a NOOP reported as
// (0, nil), not an error (the promotion-fixed integrity ruling enforced
// at the SQL level too — the caller commits only on n>0). The dolt
// commit is the caller's responsibility (the Invalidate-class
// convention). Errors return as-is; the caller absorbs them under the
// metering principle (§13).
func (r *Repo) UpdatePendingSummary(ctx context.Context, id int, summary string) (int64, error) {
	ct, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET summary = $2
		 WHERE id = $1 AND status = 'pending' AND invalid_at IS NULL`, id, summary)
	if err != nil {
		return 0, fmt.Errorf("update pending summary: %w", err)
	}
	return ct.RowsAffected(), nil
}
