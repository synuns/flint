// realstore_curation.go — v0.39.0 T4: approve.go·done.go가 lib/knowledge로
// 이동하며 realStore 어댑터 메서드 3종(Approve·StaleSessionRuns·
// LogPlaybookUse)만 cli에 추출. knowledge.approveStore·staleSessionStore·
// usageStore 확장의 실구현 — realStore는 cli 소유 타입이라 knowledge에
// 둘 수 없다(사이클).
package cli

import (
	"context"
)

// realStore.Approve — knowledge.approveStore 확장의 실구현
// (db.Repo.Approve 위임 — pending→active 승격 T17).
func (s *realStore) Approve(ctx context.Context, id int) error {
	return s.repo.Approve(ctx, id)
}

// realStore.StaleSessionRuns — knowledge.staleSessionStore 확장의 실구현
// (db.Repo.StaleSessionRuns 위임 — T17 미기록 연속 카운트).
func (s *realStore) StaleSessionRuns(ctx context.Context, agent string) (int, error) {
	return s.repo.StaleSessionRuns(ctx, agent)
}

// realStore.LogPlaybookUse — knowledge.usageStore 확장의 실구현
// (db.Repo.LogPlaybookUse 위임 — T37 사용 로그).
func (s *realStore) LogPlaybookUse(ctx context.Context, id int, ok bool) error {
	return s.repo.LogPlaybookUse(ctx, id, ok)
}
