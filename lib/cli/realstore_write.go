package cli

import (
	"context"

	"github.com/synuns/flint/lib/knowledge"
)

func (s *realStore) DupIDByHash(ctx context.Context, hash string) (int, bool, error) {
	return s.repo.DupIDByHash(ctx, hash)
}

func (s *realStore) DupRowByHash(ctx context.Context, hash string) (knowledge.DupMeta, bool, error) {
	id, topic, state, exists, err := s.repo.DupRowByHash(ctx, hash)
	return knowledge.DupMeta{ID: id, Topic: topic, State: state}, exists, err
}

func (s *realStore) UpdatePendingSummary(ctx context.Context, id int, summary string) (int64, error) {
	return s.repo.UpdatePendingSummary(ctx, id, summary)
}
