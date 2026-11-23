package knowledge

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

type ExportRow struct {
	ID             int
	Topic          string
	Content        string
	Kind           string
	Status         string
	Confidence     float64
	Tags           []string
	Entities       []string
	Domain         string
	Source         string
	ContentHash    string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ValidAt        time.Time
	SupersededBy   *int
	RecallScore    int
	LastRecalledAt *time.Time
}

type ExportFilter struct {
	Status string
	Tag    string
	Since  time.Time
}

func buildExportQuery(f ExportFilter) (string, []any) {
	var where []string
	var args []any
	if f.Status != "all" {
		where = append(where, "status = 'active'", "invalid_at IS NULL")
	}
	if f.Tag != "" {
		args = append(args, f.Tag)
		where = append(where, fmt.Sprintf("strpos(tags, ':' || $%d || ':') > 0", len(args)))
	}
	if !f.Since.IsZero() {

		args = append(args, f.Since.UTC().Format(time.RFC3339Nano))
		where = append(where, fmt.Sprintf("COALESCE(invalid_at, ingested_at) >= $%d", len(args)))
	}

	sql := "SELECT id, topic, summary, tags, entity, domain, confidence, origin, status, " +
		"content_hash, superseded_by, valid_at, ingested_at, COALESCE(invalid_at, ingested_at), " +
		"re.cnt, re.last_at FROM knowledge" +
		" LEFT JOIN (" + RecallAggregateSQL + ") re ON re.knowledge_id = knowledge.id"
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	return sql + " ORDER BY id", args
}

func ExportRows(ctx context.Context, st wire.Store, f ExportFilter) ([]ExportRow, error) {
	switch f.Status {
	case "", "active", "all":
	default:
		return nil, &store.SchemaError{Field: "status", Expect: "active|all"}
	}
	sql, args := buildExportQuery(f)
	rows, err := wire.QueryOnce(ctx, st, sql, args)
	if err != nil {
		return nil, err
	}
	out := make([]ExportRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapExportRow(r))
	}
	return out, nil
}

func mapExportRow(r []any) ExportRow {
	cell := func(i int) any {
		if i < len(r) {
			return r[i]
		}
		return nil
	}
	row := ExportRow{Kind: "knowledge"}
	if id, ok := render.CellToInt(cell(0)); ok {
		row.ID = id
	}
	row.Topic = cellString(cell(1))
	row.Content = cellString(cell(2))
	row.Tags = parseExportTags(cellString(cell(3)))
	if e := cellString(cell(4)); e != "" {
		row.Entities = []string{e}
	}
	row.Domain = cellString(cell(5))
	row.Confidence = cellFloat(cell(6))
	row.Source = cellString(cell(7))
	row.Status = cellString(cell(8))
	row.ContentHash = cellString(cell(9))
	if cell(10) != nil {
		if id, ok := render.CellToInt(cell(10)); ok {
			row.SupersededBy = &id
		}
	}
	row.CreatedAt = cellTime(cell(12))
	row.UpdatedAt = cellTime(cell(13))
	row.ValidAt = cellTime(cell(11))

	if s, ok := render.CellToInt(cell(14)); ok {
		row.RecallScore = s
	}
	if c := cell(15); c != nil {
		if t := cellTime(c); !t.IsZero() {
			row.LastRecalledAt = &t
		}
	}
	return row
}

func parseExportTags(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ":")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cellString(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	default:
		return fmt.Sprint(s)
	}
}

func cellFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0
		}
		return f
	default:
		return 0
	}
}

func cellTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		for _, layout := range []string{
			time.RFC3339Nano, time.RFC3339,
			"2006-01-02 15:04:05.999999999-07", "2006-01-02 15:04:05.999999",
		} {
			if pt, err := time.Parse(layout, t); err == nil {
				return pt
			}
		}
	}
	return time.Time{}
}
