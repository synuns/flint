package cli

import (
	"context"
	"time"

	"github.com/synuns/flint/lib/db"
)

func (s *realStore) Counts(ctx context.Context) (db.Counts, error) {
	return s.repo.Counts(ctx)
}

func (s *realStore) DupGroups(ctx context.Context, limit int) ([]db.DupGroup, error) {
	return s.repo.DupGroups(ctx, limit)
}

func (s *realStore) Invalidate(ctx context.Context, ids []int, reason string) (int64, error) {
	return s.repo.Invalidate(ctx, ids, reason)
}

func (s *realStore) Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error) {
	return s.repo.Supersede(ctx, oldID, newID, at)
}

func (s *realStore) PruneInvalidated(ctx context.Context, before time.Time) (int64, error) {
	return s.repo.PruneInvalidated(ctx, before)
}

func (s *realStore) PruneRecallEvents(ctx context.Context, before time.Time) (int64, error) {
	return s.repo.PruneRecallEvents(ctx, before)
}

func (s *realStore) MetaDelete(ctx context.Context, keys []string) (int64, error) {
	return s.repo.MetaDelete(ctx, keys)
}

func (s *realStore) UpdateSessionSummary(ctx context.Context, id int, summary, tasks string) (int64, error) {
	return s.repo.UpdateSessionSummary(ctx, id, summary, tasks)
}

func (s *realStore) Merge(ctx context.Context, fromID, toID int) error {
	return s.repo.Merge(ctx, fromID, toID)
}

func (s *realStore) ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error) {
	return s.repo.ConflictGroups(ctx, limit)
}

func (s *realStore) RecordSessionStart(ctx context.Context, session string) error {
	return s.repo.RecordSessionStart(ctx, session)
}
