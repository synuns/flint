package cli

import (
	"context"

	"github.com/synuns/flint/lib/store"
)

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
		return false, nil
	}
	r.rechecks++
	return r.rechecks <= r.racedDups, nil
}
