package cli

import (
	"context"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/wire"
)

var cliServeNewStoreFn = func(ctx context.Context) (wire.Store, error) {
	return newStore(ctx, wire.DSNFromEnv())
}

func init() {
	ops.SetServeNewStoreFn(func(ctx context.Context) (wire.Store, error) {
		return cliServeNewStoreFn(ctx)
	})
}
