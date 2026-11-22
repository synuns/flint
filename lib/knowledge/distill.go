// distill.go — the flint distill source query and markdown formatter
// (v1.22.0 T6, ADR-0018). DistillRows selects the insight candidates —
// active (status='active' AND invalid_at IS NULL — the single definition
// of active, the export convention), never-emitted (distilled_at IS NULL
// — the row-level idempotence stamp, schema v17), and above the score
// floor (COALESCE(re.cnt, 0) >= min — the recall_events derived
// aggregate, the recall_score basis of the G2 ruling). FormatInsights
// renders the per-run aggregate markdown (one insights-<timestamp>.md
// per run — the caller owns the file lifecycle); every emitted string
// passes render.Text (the output sanitization chokepoint, ADR-0006).
package knowledge

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/wire"
)

// DistillRow — one insight row of the distill output (see the package
// comment for the selection basis). RecallScore is the derived
// recall_events count (never-recalled is NULL → 0). SurvivedSessions
// (v1.24.0 T2 — the v1.22.0 survived-score carry-over, minimum
// fulfillment) is the SurvivedCounts signal merged into the listing: the
// number of distinct sessions with 2+ knowledge_reads of the row
// (absence = 0; a lookup failure degrades quietly — the score switchover
// itself is deferred until observation, the G2 ruling).
type DistillRow struct {
	ID          int
	Topic       string
	Summary     string
	Conditions  string
	Tags        []string
	RecallScore int
	// SurvivedSessions — merged per row via SurvivedCounts (reused
	// verbatim — no new metering surface).
	SurvivedSessions int
}

// DistillRowsSQL — the insight-candidate selection. The floor rides as
// the single bound argument ($1); ORDER BY id keeps the emitted file
// stable (the row id stable-sort contract).
const DistillRowsSQL = `SELECT id, topic, summary, conditions, tags, COALESCE(re.cnt, 0)
FROM knowledge
LEFT JOIN (` + RecallAggregateSQL + `) re ON re.knowledge_id = knowledge.id
WHERE status = 'active' AND invalid_at IS NULL
  AND distilled_at IS NULL
  AND COALESCE(re.cnt, 0) >= $1
ORDER BY id`

// DistillRows — the distill source query (the ExportRows convention:
// direct base-table query — the active_knowledge view does not expose
// distilled_at). Query failure propagates unchanged (MapExit maps to
// exit 2). v1.24.0 T2: the selected rows get their SurvivedCounts merged
// (one batched lookup — a failure degrades quietly to 0s, §13).
func DistillRows(ctx context.Context, st wire.Store, minScore float64) ([]DistillRow, error) {
	rows, err := wire.QueryOnce(ctx, st, DistillRowsSQL, []any{minScore})
	if err != nil {
		return nil, err
	}
	out := make([]DistillRow, 0, len(rows))
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		row := mapDistillRow(r)
		out = append(out, row)
		ids = append(ids, row.ID)
	}
	if counts, serr := SurvivedCounts(ctx, st, ids); serr == nil {
		for i := range out {
			if c, ok := counts[out[i].ID]; ok {
				out[i].SurvivedSessions = c
			}
		}
	}
	return out, nil
}

// mapDistillRow — SELECT column-order cell tuple → DistillRow (lenient
// cell conversion, the mapExportRow convention: pgx-measured types and
// fake string cells both accepted).
func mapDistillRow(r []any) DistillRow {
	cell := func(i int) any {
		if i < len(r) {
			return r[i]
		}
		return nil
	}
	row := DistillRow{}
	if id, ok := render.CellToInt(cell(0)); ok {
		row.ID = id
	}
	row.Topic = cellString(cell(1))
	row.Summary = cellString(cell(2))
	row.Conditions = cellString(cell(3))
	row.Tags = parseExportTags(cellString(cell(4)))
	if s, ok := render.CellToInt(cell(5)); ok {
		row.RecallScore = s
	}
	return row
}

// FormatInsights — serializes the selected rows into the per-run
// aggregate markdown (pure function — no source query, no file write).
// Header (title, created UTC, emitted row count) + a section per row
// (`## #<id> <topic>` + meta line + summary). Input order is preserved
// verbatim (the caller passes the id-sorted selection). Every string
// field passes render.Text — [flint] markers and control characters are
// neutralized before the file reaches disk (ADR-0018 sanitization).
//
// v1.22.1 r97 file cap: maxRunes > 0 bounds the WHOLE file (header
// included, runes not bytes). Rows are appended whole while they fit; the
// first row that would cross the cap and everything after it are left
// out and the emitted count is returned so the caller stamps only the
// emitted ids (the remainder defers to the next run — ADR-0018). A
// maxRunes <= 0 disables the cap (every row fits — the defensive
// no-threshold shape).
func FormatInsights(rows []DistillRow, generatedAt time.Time, maxRunes int) ([]byte, int) {
	var head, body bytes.Buffer
	head.WriteString("# flint distill insights\n\n")
	head.WriteString("created: " + generatedAt.UTC().Format(time.RFC3339) + "\n")
	// placeholder upper bound: emitted <= len(rows), so the final count
	// line never grows past the budget the cap check already approved.
	place := "rows: " + strconv.Itoa(len(rows)) + "\n"
	head.WriteString(place)
	emitted := 0
	for _, r := range rows {
		var sec bytes.Buffer
		tags := make([]string, len(r.Tags))
		for i, t := range r.Tags {
			tags[i] = render.Text(t)
		}
		joined := strings.Join(tags, "|")
		if joined == "" {
			joined = "-"
		}
		conditions := render.Text(r.Conditions)
		if conditions == "" {
			conditions = "-"
		}
		sec.WriteString("\n## #" + strconv.Itoa(r.ID) + " " + render.Text(r.Topic) + "\n\n")
		// v1.24.0 T2 — survived_sessions rides the meta line after
		// conditions (the v1.22.0 line shape stays a prefix; the count is
		// the SurvivedCounts signal, the minimum fulfillment of the
		// survived-score carry-over).
		sec.WriteString("recall_score: " + strconv.Itoa(r.RecallScore) +
			" · tags: " + joined +
			" · conditions: " + conditions +
			" · survived_sessions: " + strconv.Itoa(r.SurvivedSessions) + "\n\n")
		sec.WriteString(render.Text(r.Summary))
		sec.WriteString("\n")
		if maxRunes > 0 && utf8.RuneCount(head.Bytes())+utf8.RuneCount(body.Bytes())+utf8.RuneCount(sec.Bytes()) > maxRunes {
			break
		}
		body.Write(sec.Bytes())
		emitted++
	}
	out := append(head.Bytes(), body.Bytes()...)
	// the header's row count names the emitted rows (never longer than
	// the placeholder the cap check budgeted for).
	out = bytes.Replace(out, []byte(place), []byte("rows: "+strconv.Itoa(emitted)+"\n"), 1)
	return out, emitted
}
