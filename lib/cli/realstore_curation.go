// realstore_curation.go — as approve.go and done.go moved to lib/knowledge,
// only the 3 realStore adapter methods (Approve, StaleSessionRuns,
// LogPlaybookUse) were extracted into cli. Real implementations of the knowledge.approveStore,
// staleSessionStore, and usageStore extensions — realStore is a cli-owned type, so it cannot
// live in knowledge (cycle).
package cli

import (
	"context"
)

// realStore.Approve — real implementation of the knowledge.approveStore extension
// (delegates to db.Repo.Approve — pending->active promotion).
func (s *realStore) Approve(ctx context.Context, id int) error {
	return s.repo.Approve(ctx, id)
}

// realStore.StaleSessionRuns — real implementation of the knowledge.staleSessionStore extension
// (delegates to db.Repo.StaleSessionRuns — unrecorded streak count).
func (s *realStore) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	return s.repo.StaleSessionRuns(ctx, agent)
}

// realStore.LogPlaybookUse — real implementation of the knowledge.usageStore extension
// (delegates to db.Repo.LogPlaybookUse — usage log).
func (s *realStore) LogPlaybookUse(ctx context.Context, id int, ok bool) error {
	return s.repo.LogPlaybookUse(ctx, id, ok)
}
