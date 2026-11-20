// usage_wire.go — connects db.Repo.RecordUsage to the cli
// instrumentation (the UsageStore assertion in recordUsage) via realStore delegation. Only the
// method definition lives here (same as the MaintStore delegation pattern in maintain.go).
package cli

import "context"

// RecordUsage — realStore delegation: records one inbound usage event (kind/source contract).
func (s *realStore) RecordUsage(ctx context.Context, kind, source string) error {
	return s.repo.RecordUsage(ctx, kind, source)
}

// RecordUsageCount — realStore delegation: increments inbound usage count in one batch call
// (same seam as the UsageCountStore in write.go recordDups — dup N as a single call).
func (s *realStore) RecordUsageCount(ctx context.Context, kind, source string, count int) error {
	return s.repo.RecordUsageCount(ctx, kind, source, count)
}

var _ UsageStore = (*realStore)(nil)
var _ UsageCountStore = (*realStore)(nil)
