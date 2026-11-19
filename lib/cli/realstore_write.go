// realstore_write.go — as write.go moved to lib/knowledge, only the two
// realStore adapter methods (DupIDByHash, DupRowByHash) were extracted into cli.
// Real implementations of the knowledge.DupIDStore and DupMetaStore extensions — realStore is a cli-owned
// type, so it cannot live in knowledge (cycle).
package cli

import (
	"context"

	"github.com/synuns/flint/lib/knowledge"
)

// realStore.DupIDByHash — real implementation of the knowledge.DupIDStore extension
// (delegates to db.Repo.DupIDByHash).
func (s *realStore) DupIDByHash(ctx context.Context, hash string) (int, bool, error) {
	return s.repo.DupIDByHash(ctx, hash)
}

// realStore.DupRowByHash — real implementation of the knowledge.DupMetaStore extension
// (db.Repo.DupRowByHash 5 values -> meta).
func (s *realStore) DupRowByHash(ctx context.Context, hash string) (knowledge.DupMeta, bool, error) {
	id, topic, state, exists, err := s.repo.DupRowByHash(ctx, hash)
	return knowledge.DupMeta{ID: id, Topic: topic, State: state}, exists, err
}
