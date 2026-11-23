package cli

import "context"

func (s *realStore) RecordUsage(ctx context.Context, kind, source string) error {
	return s.repo.RecordUsage(ctx, kind, source)
}

func (s *realStore) RecordUsageCount(ctx context.Context, kind, source string, count int) error {
	return s.repo.RecordUsageCount(ctx, kind, source, count)
}

var _ UsageStore = (*realStore)(nil)
var _ UsageCountStore = (*realStore)(nil)
