package cli

import (
	"context"
)

func (s *realStore) Approve(ctx context.Context, id int) error {
	return s.repo.Approve(ctx, id)
}

func (s *realStore) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	return s.repo.StaleSessionRuns(ctx, agent)
}

func (s *realStore) LogPlaybookUse(ctx context.Context, id int, ok bool) error {
	return s.repo.LogPlaybookUse(ctx, id, ok)
}
