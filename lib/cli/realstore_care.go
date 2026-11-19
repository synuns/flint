// realstore_care.go — realStore method extraction file accompanying the wave-2 ops
// move (maintain, metrics, marker, nudge,
// sessionhook) — the realstore_* convention, the same pattern continued from realstore_diag.go. The 5 admin delegations of maintain.go
// (the MaintStore and ConflictStore contracts) and RecordSessionStart of sessionhook.go
// (the SessionStartRecorder contract) land here.
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

// Invalidate — delegates the invalidation transition to the single db.Repo update path
// (with the rows-affected stewardship built in).
func (s *realStore) Invalidate(ctx context.Context, ids []int, reason string) (int64, error) {
	return s.repo.Invalidate(ctx, ids, reason)
}

// Supersede — delegates the supersede transition (recording superseded_by lineage).
func (s *realStore) Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error) {
	return s.repo.Supersede(ctx, oldID, newID, at)
}

// PruneInvalidated — delegates the DELETE of invalidated knowledge older than N days.
func (s *realStore) PruneInvalidated(ctx context.Context, before time.Time) (int64, error) {
	return s.repo.PruneInvalidated(ctx, before)
}

// PruneRecallEvents — delegates the DELETE of recall events past the retention period.
// (recall_event_retention_days — R42-D).
func (s *realStore) PruneRecallEvents(ctx context.Context, before time.Time) (int64, error) {
	return s.repo.PruneRecallEvents(ctx, before)
}

// MetaDelete — delegates the meta row deletion (tuning reset, reset --all).
func (s *realStore) MetaDelete(ctx context.Context, keys []string) (int64, error) {
	return s.repo.MetaDelete(ctx, keys)
}

// UpdateSessionSummary — delegates the done closing-summary sessions update.
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
