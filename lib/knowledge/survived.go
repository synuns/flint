package knowledge

import (
	"context"
	"strconv"

	"github.com/synuns/flint/lib/wire"
)

func SurvivedCounts(ctx context.Context, st wire.Store, ids []int) (map[int]int, error) {
	if len(ids) == 0 {
		return map[int]int{}, nil
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT knowledge_id, count(*) FROM (
		    SELECT knowledge_id, session FROM knowledge_reads
		    WHERE knowledge_id = ANY($1)
		    GROUP BY knowledge_id, session
		    HAVING count(*) >= 2
		) repeated GROUP BY knowledge_id`, []any{ids})
	if err != nil {
		return nil, err
	}
	out := make(map[int]int, len(rows))
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		id, ok := survivedCellInt(row[0])
		if !ok {
			continue
		}
		n, ok := survivedCellInt(row[1])
		if !ok {
			continue
		}
		out[id] = n
	}
	return out, nil
}

func SnapshotSurvivedSessions(ctx context.Context, st wire.Store, counts map[int]int) {
	n := 0
	for _, c := range counts {
		if c >= 1 {
			n++
		}
	}
	BumpCurationCounter(ctx, st, "survived_sessions", n)
}

func survivedCellInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case string:
		i, err := strconv.Atoi(n)
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}
