// write_test.go — the write body's contract tests moved to lib/knowledge
// (see write_test.go and import_test.go there). This file keeps only the shared race-reproduction fake
// consumed by the residual cli tests (write_v06_test.go) — dispatch-based tests (via the
// newStore seam) remain in cli regardless of the body's move (same contract as the
// knowledge copies).
package cli

import (
	"context"

	"github.com/synuns/flint/lib/store"
)

// raceDupStore — a race-reproduction fake: the pre-check ExistsHash passes everything (false),
// only the first InsertBatch call returns store.ErrDuplicate (a concurrent insert rolls the transaction back).
// Subsequent ExistsHash calls answer true only for the first racedDups lines (the recheck moment).
type raceDupStore struct {
	*fakeStore
	insertCalls int
	rechecks    int
	racedDups   int
}

func (r *raceDupStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	r.insertCalls++
	if r.insertCalls == 1 {
		return nil, store.ErrDuplicate
	}
	return r.fakeStore.InsertBatch(ctx, recs)
}

func (r *raceDupStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	if r.insertCalls == 0 {
		return false, nil // pre-check: everything passes as new
	}
	r.rechecks++
	return r.rechecks <= r.racedDups, nil // recheck: only the first racedDups lines were concurrently inserted
}
