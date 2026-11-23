package db

import (
	"context"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/wire"
)

const DoltLogTable = "dolt_log"

func DoltLogDepth(ctx context.Context, st wire.Store) (int64, error) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT commit_order::int8 FROM `+DoltLogTable+` ORDER BY commit_order DESC LIMIT 1`, nil)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 || len(rows[0]) == 0 || rows[0][0] == nil {
		return 0, nil
	}
	n, _ := render.CellToInt(rows[0][0])
	return int64(n), nil
}
