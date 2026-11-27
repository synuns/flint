package cli

import (
	"context"
)

func (s *realStore) Approve(ctx context.Context, id int, claim string) error {
	return s.repo.Approve(ctx, id, claim)
}

func (s *realStore) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	return s.repo.StaleSessionRuns(ctx, agent)
}

func (s *realStore) LogPlaybookUse(ctx context.Context, id int, ok bool) error {
	return s.repo.LogPlaybookUse(ctx, id, ok)
}
