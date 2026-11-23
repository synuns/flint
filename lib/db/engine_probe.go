// engine_probe.go — the engine-warning diagnostic probe, lib/db-owned since
// v1.25.1 (r108-2). The dialect sealing gate names lib/db the sole owner of
// raw dialect strings; the doctor depth check used to split-assemble its SQL
// in ops (`"dolt_" + "log"`), which dodged the gate without transferring
// ownership. The SQL lives here now — doctor calls it, never restates it.
package db

import (
	"context"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/wire"
)

// DoltLogTable — the dolt system log table name. The raw dialect string
// exists only here; consumers reference the constant (messages included)
// instead of re-spelling it.
const DoltLogTable = "dolt_log"

// DoltLogDepth — the O(1) history-depth probe: the max commit_order of the
// dolt log (commit_order is monotonic, so LIMIT 1 answers the depth; count(*)
// over an ever-growing log is exactly the cost the warning exists to flag).
// The r109-1 fix rides the select expression: Doltgres types commit_order
// numeric, and pgx hands numeric back as pgtype.Numeric — the ::int8 cast
// keeps the wire type int8 so the value coerces (render.CellToInt also
// learned pgtype.Numeric as the second layer of the same fix). An empty log
// answers depth 0, nil; a query failure propagates — the caller grades it (a
// failed probe is a warn finding, never a disguised clean verdict). The read
// goes through wire.QueryOnce (the reader-retry convention, spec §7).
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
