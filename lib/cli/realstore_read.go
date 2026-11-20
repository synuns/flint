// realstore_read.go — as read.go and hot.go moved to lib/knowledge,
// only the realStore adapter methods (BumpReads, RecordReadEvent, RecordHotEvent) were
// extracted into cli. Real implementations of the knowledge.readBumper, ReadEventStore, and HotEventRecorder
// extensions — realStore is a cli-owned type, so it cannot live in knowledge (cycle).
// The RecordReadUsage adapter is gone (read_stats blob and daily read
// writes abolished — read_events is the single source of read metering).
package cli

import (
	"context"

	"github.com/synuns/flint/lib/db"
)

// realStore.BumpReads — real implementation of the knowledge.readBumper extension
// (delegates to db.Repo.BumpReads; reference metering).
func (s *realStore) BumpReads(ctx context.Context, ids []int) error {
	return s.repo.BumpReads(ctx, ids)
}

// realStore.RecordReadEvent — real implementation of the knowledge.ReadEventStore extension
// (delegates to db.Repo.RecordReadEvent; effect metering).
// Converged to passing a single db.ReadEvent struct value.
func (s *realStore) RecordReadEvent(ctx context.Context, ev db.ReadEvent) error {
	return s.repo.RecordReadEvent(ctx, ev)
}

// realStore.RecordHotEvent — real implementation of the knowledge.HotEventRecorder extension
// (delegates to db.Repo.RecordHotEvent).
func (s *realStore) RecordHotEvent(ctx context.Context, ids string) error {
	return s.repo.RecordHotEvent(ctx, ids)
}

// realStore.RecordBytes — real implementation of the knowledge.BytesRecorder extension
// (delegates to db.Repo.RecordBytes; byte metering).
func (s *realStore) RecordBytes(ctx context.Context, kind string, n int) error {
	return s.repo.RecordBytes(ctx, kind, n)
}

// realStore.RecordKnowledgeReads — real implementation of the knowledge.KnowledgeReadRecorder
// extension (delegates to db.Repo.RecordKnowledgeReads; per-item recall history).
func (s *realStore) RecordKnowledgeReads(ctx context.Context, ids []int, session, kind string) error {
	return s.repo.RecordKnowledgeReads(ctx, ids, session, kind)
}

// realStore.CommitTelemetry — real implementation of the knowledge.TelemetryCommitter extension
// (delegates to db.Repo.CommitTelemetry; separate telemetry-table commit).
func (s *realStore) CommitTelemetry(ctx context.Context, cmd string) error {
	return s.repo.CommitTelemetry(ctx, cmd)
}

// realStore.BumpRecallScores — real implementation of the knowledge.RecallBumper extension
// (delegates to db.Repo.BumpRecallScores; recall reinforcement with recall_score+1 and
// last_recalled_at=now).
func (s *realStore) BumpRecallScores(ctx context.Context, ids []int) error {
	return s.repo.BumpRecallScores(ctx, ids)
}
