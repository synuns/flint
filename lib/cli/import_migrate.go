package cli

import (
	"context"
	"io"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

func RunImportMigrate(st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	return knowledge.RunImportMigrate(context.Background(), st, args, stdin, stdout)
}

func (s *realStore) SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error) {
	return s.repo.SetSupersededRef(ctx, oldID, newID)
}

var _ interface {
	SetSupersededRef(ctx context.Context, oldID, newID int) (int64, error)
} = (*db.Repo)(nil)
