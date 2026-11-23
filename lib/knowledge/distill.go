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

type DistillRow struct {
	ID          int
	Topic       string
	Summary     string
	Conditions  string
	Tags        []string
	RecallScore int

	SurvivedSessions int
}

const DistillRowsSQL = `SELECT id, topic, summary, conditions, tags, COALESCE(re.cnt, 0)
FROM knowledge
LEFT JOIN (` + RecallAggregateSQL + `) re ON re.knowledge_id = knowledge.id
WHERE status = 'active' AND invalid_at IS NULL
  AND distilled_at IS NULL
  AND COALESCE(re.cnt, 0) >= $1
ORDER BY id`

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

func FormatInsights(rows []DistillRow, generatedAt time.Time, maxRunes int) ([]byte, int) {
	var head, body bytes.Buffer
	head.WriteString("# flint distill insights\n\n")
	head.WriteString("created: " + generatedAt.UTC().Format(time.RFC3339) + "\n")

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

	out = bytes.Replace(out, []byte(place), []byte("rows: "+strconv.Itoa(emitted)+"\n"), 1)
	return out, emitted
}
