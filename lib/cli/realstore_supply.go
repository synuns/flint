package cli

import (
	"context"
	"io"

	"github.com/synuns/flint/lib/backup"
)

func (s *realStore) Dump(ctx context.Context, w io.Writer) error {
	return backup.Dump(ctx, dsnFromEnv(), w)
}
