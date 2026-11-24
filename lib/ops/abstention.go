package ops

import (
	"context"
	"math"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

const zeroReasonUnowned = "unowned"

func zeroHitUnownedPctOf(evs []ReadEventRow) float64 {
	zero, unowned := 0, 0
	for _, e := range evs {
		if !e.Zero {
			continue
		}
		zero++
		if e.ZeroReason == zeroReasonUnowned {
			unowned++
		}
	}
	if zero == 0 {
		return 0
	}
	return math.Round(float64(unowned)/float64(zero)*1000) / 10
}

func ZeroHitUnownedPct(ctx context.Context, st wire.Store, days int) (float64, error) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT zero, zero_reason FROM read_events WHERE ts >= $1 ORDER BY ts`,
		[]any{db.DaysCutoff(days, time.Now())})
	if err != nil {
		return 0, err
	}
	evs := make([]ReadEventRow, 0, len(rows))
	for _, r := range rows {
		if len(r) < 1 {
			continue
		}
		e := ReadEventRow{Zero: cellToBool(r[0])}
		if len(r) > 1 {
			if s, ok := r[1].(string); ok {
				e.ZeroReason = s
			}
		}
		evs = append(evs, e)
	}
	return zeroHitUnownedPctOf(evs), nil
}
