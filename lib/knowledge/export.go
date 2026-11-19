// export.go — the flint export source query (ExportRows·ExportRow·
// ExportFilter). Inherits the existing read query source:
//   - Single definition of active: status='active' AND invalid_at IS NULL
//     (the activeKnowledgeCount·BuildReadStatus common rule). pending is
//     included only under Status:"all".
//   - Tag filter ':t:' strpos exact matching (appendReadFilters convention).
//   - Direct query on the base table knowledge — the active_knowledge view
//     does not expose superseded_by·invalid_at (same rationale as
//     BuildReadStatus).
//   - No LIMIT (full export — read's token-budget clamp is for the search
//     path only).
//
// Schema-measured mapping (schema changes forbidden — invariant in this patch):
//   - content ← summary, entities ← entity (single column), created_at ← ingested_at.
//   - updated_at column absent → derived as COALESCE(invalid_at, ingested_at)
//     (rows are immutable commits, so invalid_at is the last update time).
//     The Since cutoff uses the same definition (PAM §16 incremental borrow,
//     zero value = everything).
//   - Kind is always "knowledge" (source-table discriminator — not a DB column).
//   - Source is the origin column (agent|human|import:pam:* — the record left
//     by the done hook·PAM import) verbatim. Round-trip re-import hashes
//     include the origin key, so exporting it empty would re-accrue
//     origin≠"agent" rows as duplicates on re-import (preserving the source
//     column is the round-trip contract).
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

// ExportRow — one export row (see the package comment above for the source mapping).
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
	ValidAt        time.Time // valid-at raw value (round-trip preserved)
	SupersededBy   *int
	RecallScore    int        // derived event count (recall_events cnt — v1.9.0 T4)
	LastRecalledAt *time.Time // derived last recall time (recall_events last_at, nil=never recalled)
}

// ExportFilter — the ExportRows filter. Status "active" (default·zero value)
// |"all" (everything including invalidated·pending). Tag is ':t:' exact
// matching. Since is an updated_at (COALESCE(invalid_at, ingested_at))
// cutoff — zero value means everything.
type ExportFilter struct {
	Status string
	Tag    string
	Since  time.Time
}

// buildExportQuery — assembles the export query SQL. Condition order =
// placeholder order (tag → since — the test seam consumes this order as a
// contract).
func buildExportQuery(f ExportFilter) (string, []any) {
	var where []string
	var args []any
	if f.Status != "all" { // active default (incl. zero value) — single definition of active
		where = append(where, "status = 'active'", "invalid_at IS NULL")
	}
	if f.Tag != "" {
		args = append(args, f.Tag)
		where = append(where, fmt.Sprintf("strpos(tags, ':' || $%d || ':') > 0", len(args)))
	}
	if !f.Since.IsZero() {
		// No direct time.Time binding — an RFC3339Nano string (the same
		// format as the import_jsonl valid_at convention. Avoids Doltgres/PG
		// parameter type mismatch — cutoff results are identical before and
		// after stringification).
		args = append(args, f.Since.UTC().Format(time.RFC3339Nano))
		where = append(where, fmt.Sprintf("COALESCE(invalid_at, ingested_at) >= $%d", len(args)))
	}
	// The valid_at column sits after superseded_by and before ingested_at —
	// the source of jsonl round-trip identity. The two score columns are
	// appended at the tail (existing cell-index contract unchanged — test
	// seam column references inherited). v1.9.0 T4: values come from the
	// recall_events derived aggregate (re.cnt·re.last_at) — v11 knowledge
	// physical columns removed, output schema field names unchanged.
	sql := "SELECT id, topic, summary, tags, entity, domain, confidence, origin, status, " +
		"content_hash, superseded_by, valid_at, ingested_at, COALESCE(invalid_at, ingested_at), " +
		"re.cnt, re.last_at FROM knowledge" +
		" LEFT JOIN (" + RecallAggregateSQL + ") re ON re.knowledge_id = knowledge.id"
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	return sql + " ORDER BY id", args
}

// ExportRows — the export source query. Invalid Status yields
// *store.SchemaError (caller maps to exit 3); query failure propagates
// unchanged (MapExit maps to exit 2).
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

// mapExportRow — SELECT column-order cell tuple → ExportRow. Accepts both
// the pgx-measured types (int32/int64·float64·time.Time) and fake string
// cells.
func mapExportRow(r []any) ExportRow {
	cell := func(i int) any {
		if i < len(r) {
			return r[i]
		}
		return nil
	}
	row := ExportRow{Kind: "knowledge"} // source-table discriminator (see comment above)
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
	row.Source = cellString(cell(7)) // origin column — round-trip hash symmetry (see comment above)
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
	// Two score cells — derived aggregate values (never-recalled is NULL →
	// 0/nil fallback, same as legacy jsonl).
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

// parseExportTags — ':a:b:' normalized form → ["a","b"]. Empty values·empty
// fragments yield nil.
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

// cellString — nil → "", strings as-is, everything else via fmt.Sprint.
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

// cellFloat — lenient numeric cell conversion (pgx float64·integer types·
// string parsing).
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

// cellTime — lenient time cell conversion (time.Time preferred; strings
// parsed as RFC3339 family — unparseable yields the zero value).
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
