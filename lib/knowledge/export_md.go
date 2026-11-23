package knowledge

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/wire"
)

const SupersedeChainsSQL = `
	SELECT root_id, id FROM supersede_chains
	WHERE depth > 0
	ORDER BY root_id, depth`

func SupersedeChains(ctx context.Context, st wire.Store) (map[int][]int, error) {
	rows, err := wire.QueryOnce(ctx, st, SupersedeChainsSQL, nil)
	if err != nil {
		return nil, err
	}
	out := make(map[int][]int, len(rows))
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		root, okRoot := render.CellToInt(r[0])
		id, okID := render.CellToInt(r[1])
		if !okRoot || !okID {
			continue
		}
		out[root] = append(out[root], id)
	}
	return out, nil
}

func FormatMD(rows []ExportRow, chains ...map[int][]int) []byte {
	var chain map[int][]int
	if len(chains) > 0 {
		chain = chains[0]
	}
	var b bytes.Buffer
	b.WriteString("# flint export\n\n")
	b.WriteString("created: " + time.Now().UTC().Format(time.RFC3339) + "\n")
	b.WriteString("items: " + strconv.Itoa(len(rows)) + "\n")
	for _, r := range rows {
		b.WriteString("\n## #" + strconv.Itoa(r.ID) + " " + r.Topic + "\n\n")
		tags := strings.Join(r.Tags, "|")
		if tags == "" {
			tags = "-"
		}
		b.WriteString("status: " + r.Status +
			" · confidence: " + strconv.FormatFloat(r.Confidence, 'g', -1, 64) +
			" · tags: " + tags +
			" · source: " + r.Source +
			" · created: " + r.CreatedAt.Format(time.RFC3339))
		if r.SupersededBy != nil {
			b.WriteString(" · superseded_by: ")
			if c := chain[r.ID]; len(c) > 0 {
				for i, id := range c {
					if i > 0 {
						b.WriteString(" ")
					}
					b.WriteString("→ #" + strconv.Itoa(id))
				}
				b.WriteString(" (depth " + strconv.Itoa(len(c)) + ")")
			} else {
				b.WriteString("→ #" + strconv.Itoa(*r.SupersededBy))
			}
		}
		b.WriteString("\n\n")
		b.WriteString(r.Content)
		b.WriteString("\n")
	}
	return b.Bytes()
}
