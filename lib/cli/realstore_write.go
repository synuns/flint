// realstore_write.go — v0.39.0 T2: write.go가 lib/knowledge로 이동하며
// realStore 어댑터 메서드 2종(DupIDByHash·DupRowByHash)만 cli에 추출.
// knowledge.DupIDStore·DupMetaStore 확장의 실구현 — realStore는 cli 소유
// 타입이라 knowledge에 둘 수 없다(사이클).
package cli

import (
	"context"

	"github.com/synuns/flint/lib/knowledge"
)

// realStore.DupIDByHash — knowledge.DupIDStore 확장의 실구현
// (db.Repo.DupIDByHash 위임).
func (s *realStore) DupIDByHash(ctx context.Context, hash string) (int, bool, error) {
	return s.repo.DupIDByHash(ctx, hash)
}

// realStore.DupRowByHash — knowledge.DupMetaStore 확장의 실구현
// (db.Repo.DupRowByHash 5값 → 메타).
func (s *realStore) DupRowByHash(ctx context.Context, hash string) (knowledge.DupMeta, bool, error) {
	id, topic, state, exists, err := s.repo.DupRowByHash(ctx, hash)
	return knowledge.DupMeta{ID: id, Topic: topic, State: state}, exists, err
}
