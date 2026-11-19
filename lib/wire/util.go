// util.go — the single source of pure functions. Owns cellToTime
// (cell -> time), quoteJSON (a JSON string literal), and backupEncryptOn (the
// meta verdict). wire is the contract layer both knowledge and ops import —
// consumers delegate via var aliases, behavior unchanged.
package wire

import (
	"encoding/json"
	"time"
)

// CellToTime — cell (any) -> time conversion (pgx timestamptz = time.Time;
// fakes and strings use RFC3339). Unmappable cells are (zero, false).
func CellToTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		p, err := time.Parse(time.RFC3339, t)
		return p, err == nil
	default:
		return time.Time{}, false
	}
}

// QuoteJSON — wraps a path or other string as a JSON string literal (on marshal
// failure, the empty-literal fallback — inheriting the behavior contract).
func QuoteJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// BackupEncryptOn — the single source of the meta backup_encrypt verdict
// (knowledge/ops consumers delegate via var aliases). True only for "true" and "1".
func BackupEncryptOn(v string) bool {
	return v == "true" || v == "1"
}
