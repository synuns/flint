package knowledge

import (
	"bytes"
	"encoding/csv"
	"strconv"
	"strings"
	"time"
)

var formatCSVHeader = []string{
	"id", "topic", "kind", "status", "confidence",
	"tags", "source", "created_at", "updated_at", "superseded_by",
	"valid_at",
}

func FormatCSV(rows []ExportRow) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
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
			csvSafeCell(r.ValidAt.Format(time.RFC3339)),
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
