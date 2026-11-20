// export_csv.go — the flint export --format csv formatter.
// encoding/csv standard, header row included, LF newlines. 11 columns —
// content excluded (avoiding long multi-line cells — an index for inventory
// analysis). tags are joined with `|`.
package knowledge

import (
	"bytes"
	"encoding/csv"
	"strconv"
	"strings"
	"time"
)

// FormatCSV consumes source ExportRows (export.go).

// formatCSVHeader — the column-order contract (11 columns, content excluded).
var formatCSVHeader = []string{
	"id", "topic", "kind", "status", "confidence",
	"tags", "source", "created_at", "updated_at", "superseded_by",
	"valid_at",
}

// FormatCSV — serializes rows into CSV (header row + one record per row, LF).
// Pure function: independent of source queries (T2) and file writing (T7).
func FormatCSV(rows []ExportRow) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf) // default UseCRLF=false → LF
	if err := w.Write(formatCSVHeader); err != nil {
		return nil, err
	}
	for _, r := range rows {
		superseded := ""
		if r.SupersededBy != nil {
			superseded = strconv.Itoa(*r.SupersededBy)
		}
		rec := []string{
			csvSafeCell(strconv.Itoa(r.ID)),
			csvSafeCell(r.Topic),
			csvSafeCell(r.Kind),
			csvSafeCell(r.Status),
			csvSafeCell(strconv.FormatFloat(r.Confidence, 'g', -1, 64)),
			csvSafeCell(strings.Join(r.Tags, "|")),
			csvSafeCell(r.Source),
			csvSafeCell(r.CreatedAt.Format(time.RFC3339)),
			csvSafeCell(r.UpdatedAt.Format(time.RFC3339)),
			csvSafeCell(superseded),
			csvSafeCell(r.ValidAt.Format(time.RFC3339)), // valid-at raw value
		}
		if err := w.Write(rec); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// csvSafeCell — neutralizes CSV formula injection (applied to every cell):
// values starting with =,+,-,@ get a ' prefix (the common convention to keep
// spreadsheet apps from interpreting a cell as a formula). The ' appears only
// on neutralized values, never on originals.
func csvSafeCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@':
		return "'" + s
	}
	return s
}
