// realstore_care.go — v0.40.0 T2: ops 2탄(maintain·metrics·marker·nudge·
// sessionhook) 이동에 수반하는 realStore 메서드 추출 파일(v0.39 realstore_*
// 관례 — realstore_diag.go가 이은 같은 패턴). maintain.go의 admin 위임
// 5종(MaintStore·ConflictStore 계약)과 sessionhook.go의 RecordSessionStart
// (SessionStartRecorder 계약)이 여기 옮는다.
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

// Invalidate — v0.41.0 T2: 무효화 전이는 db.Repo 단일 갱신 경로 위임
// (rows-affected 후견사 내장).
func (s *realStore) Invalidate(ctx context.Context, ids []int, reason string) (int64, error) {
	return s.repo.Invalidate(ctx, ids, reason)
}

// Supersede — v0.41.0 T2: 대체 전이(계보 superseded_by 병기) 위임.
func (s *realStore) Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error) {
	return s.repo.Supersede(ctx, oldID, newID, at)
}

// PruneInvalidated — v0.41.0 T3: N일 경과 invalidated 지식 DELETE 위임.
func (s *realStore) PruneInvalidated(ctx context.Context, before time.Time) (int64, error) {
	return s.repo.PruneInvalidated(ctx, before)
}

// MetaDelete — v0.41.0 T3: meta 행 삭제(tuning reset·reset --all) 위임.
func (s *realStore) MetaDelete(ctx context.Context, keys []string) (int64, error) {
	return s.repo.MetaDelete(ctx, keys)
}

// UpdateSessionSummary — v0.41.0 T3: done 종료 요약 sessions 갱신 위임.
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
