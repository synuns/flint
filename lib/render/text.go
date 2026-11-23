package render

import (
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
)

func TruncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

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
