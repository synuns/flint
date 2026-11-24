package db

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/synuns/flint/lib/wire"
)

const DoltLogTable = "dolt_log"

func cellToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case pgtype.Numeric:
		i8, err := n.Int64Value()
		if err != nil || !i8.Valid {
			return 0, false
		}
		return int(i8.Int64), true
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil
	default:
		return 0, false
	}
}

func DoltLogDepth(ctx context.Context, st wire.Store) (int64, error) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT commit_order::int8 FROM `+DoltLogTable+` ORDER BY commit_order DESC LIMIT 1`, nil)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 || len(rows[0]) == 0 || rows[0][0] == nil {
		return 0, nil
	}
	n, _ := cellToInt(rows[0][0])
	return int64(n), nil
}
