// realstore_care.go — v0.40.0 T2: ops 2탄(maintain·metrics·marker·nudge·
// sessionhook) 이동에 수반하는 realStore 메서드 추출 파일(v0.39 realstore_*
// 관례 — realstore_diag.go가 이은 같은 패턴). maintain.go의 admin 위임
// 5종(MaintStore·ConflictStore 계약)과 sessionhook.go의 RecordSessionStart
// (SessionStartRecorder 계약)이 여기 옮는다.
package cli

import (
	"context"

	"github.com/synuns/flint/lib/db"
)

func (s *realStore) Counts(ctx context.Context) (db.Counts, error) {
	return s.repo.Counts(ctx)
}

func (s *realStore) DupGroups(ctx context.Context, limit int) ([]db.DupGroup, error) {
	return s.repo.DupGroups(ctx, limit)
}

func (s *realStore) Invalidate(ctx context.Context, id int, reason string) error {
	return s.repo.Invalidate(ctx, id, reason)
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
