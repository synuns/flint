// survived.go — the survived signal (v1.14.0 T3): the number of sessions
// that recalled a knowledge item at least twice, derived from the existing
// knowledge_reads instrumentation (no new tables — the no-new-metering
// constraint). Kind-agnostic: read·hot rows both count, because recall
// exposure itself is the signal. The consumer (decay relief, T4) queries
// this at ranking time and quietly ignores failures (non-interference
// §13 — a missing signal degrades to no relief, never blocks recall).
package knowledge

import (
	"context"
	"strconv"

	"github.com/synuns/flint/lib/wire"
)

// SurvivedCounts returns, for each id in ids, the number of distinct
// sessions with 2+ knowledge_reads rows for that id (one survived point
// per session). Ids with no repeated-recall session are absent from the
// map (callers read absence as 0). An empty ids slice is a no-op: no
// query, empty map. Query failures propagate to the caller, which
// quietly ignores them (§13).
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

// SnapshotSurvivedSessions — the decay-audit observation snapshot
// (v1.17.0 T4): the number of ids whose SurvivedCounts value is at least
// 1 is bumped once as `daily:survived_sessions:<UTC date>` (the
// BumpDailyFlatKey convention — PruneDaily's daily:* regex cleans it at
// the retention boundary). The consumer is maintain --decay-audit, which
// passes its ranking-time SurvivedCounts result here. n<=0 issues no
// query and failures are quiet (§13 — a lost observation beats a failed
// audit run).
func SnapshotSurvivedSessions(ctx context.Context, st wire.Store, counts map[int]int) {
	n := 0
	for _, c := range counts {
		if c >= 1 {
			n++
		}
	}
	BumpCurationCounter(ctx, st, "survived_sessions", n)
}

// survivedCellInt — lenient integer cell conversion (pgx int32·int64·int
// and numeric strings; anything else yields !ok and the row is skipped).
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
