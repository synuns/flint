package cli

import (
	"context"

	"github.com/synuns/flint/lib/db"
)

func (s *realStore) BumpReads(ctx context.Context, ids []int) error {
	return s.repo.BumpReads(ctx, ids)
}

func (s *realStore) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	return s.repo.RecordReadEvent(ctx, ev)
}

func (s *realStore) RecordHotEvent(ctx context.Context, ids string) error {
	return s.repo.RecordHotEvent(ctx, ids)
}

func (s *realStore) RecordBytes(ctx context.Context, kind string, n int) error {
	return s.repo.RecordBytes(ctx, kind, n)
}

func (s *realStore) RecordKnowledgeReads(ctx context.Context, ids []int, session, kind string) error {
	return s.repo.RecordKnowledgeReads(ctx, ids, session, kind)
}

func (s *realStore) CommitTelemetry(ctx context.Context, cmd string) error {
	return s.repo.CommitTelemetry(ctx, cmd)
}

func (s *realStore) BumpRecallScores(ctx context.Context, ids []int) error {
	return s.repo.BumpRecallScores(ctx, ids)
}
