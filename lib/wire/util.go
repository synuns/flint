package wire

import (
	"encoding/json"
	"time"
)

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

func QuoteJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

func BackupEncryptOn(v string) bool {
	return v == "true" || v == "1"
}
