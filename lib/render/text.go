// text.go — single source of text output helper utilities (spec §2). Keeps
// the rune-bound truncation and the single-query pending-count conventions.
package render

import (
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
)

// TruncateRunes — truncates at a rune bound (multibyte-safe). Strings at or
// under the bound and empty strings are returned unchanged. Consumer
// conventions: import body 500 runes · maintain summary prefix 40 runes ·
// pending one-line rendering bound (caller-specified).
func TruncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// CellToInt — single coercion utility from a cell (any) to int. Accepts pgx
// numeric types (int, int32, int64), float64, decimal strings, and
// pgtype.Numeric (the r109-1 fix: Doltgres returns numeric columns as
// pgtype.Numeric, which used to fall to the default and read as a silent 0
// — the depth probe was always 0 on the real engine); nil, invalid, and
// non-integral numerics yield (0, false). Consumers: knowledge read
// (rowIDs) and cli leftovers (maintain stale, report aggregation, doctor)
// — via aliases.
func CellToInt(v any) (int, bool) {
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
