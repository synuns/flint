package ops

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/wire"
)

const activeRecallBurstLimit = 5

var burstItemsFn = RecallBurstItems

var activeRecallItemSQL = `
	SELECT COALESCE(k.topic, ''), COALESCE(k.summary, ''),
	       COALESCE(k.origin, ''), COALESCE(k.confidence, 0)
	FROM knowledge k WHERE k.id = $1`

func cellFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0
		}
		return f
	}
	return 0
}

func activeRecallNoPipe(s string) string {
	return strings.ReplaceAll(s, "|", " ")
}

func activeRecallLine(item BurstItem, topic, summary, source string, confidence float64) string {
	prov := query.FormatProvenanceSuffix(render.Text(source), confidence)
	parts := []string{
		"recall-active",
		"id=" + strconv.Itoa(item.ID),
		"reason=" + activeRecallNoPipe(render.Text(item.Reason)),
		activeRecallNoPipe(render.Text(topic)),
		activeRecallNoPipe(render.Text(summary)),
	}
	return strings.Join(parts, "|") + prov
}

func InjectActiveRecall(ctx context.Context, st wire.Store, session string, stdout io.Writer, hotUsed int) int {
	budget := knowledge.HotBudget(knowledge.MetaGetKV(ctx, st))
	remaining := budget - hotUsed
	if remaining <= 0 {
		return 0
	}
	items, err := burstItemsFn(ctx, st, activeRecallBurstLimit)
	if err != nil || len(items) == 0 {
		return 0
	}
	for _, item := range items {
		rows, err := wire.QueryOnce(ctx, st, activeRecallItemSQL, []any{item.ID})
		if err != nil || len(rows) == 0 || len(rows[0]) < 4 {
			continue
		}
		topic, _ := rows[0][0].(string)
		summary, _ := rows[0][1].(string)
		source, _ := rows[0][2].(string)
		confidence := cellFloat(rows[0][3])
		line := activeRecallLine(item, topic, summary, source, confidence)
		cost := len(line) + 1
		if cost > remaining {
			break
		}
		fmt.Fprintln(stdout, line)
		remaining -= cost
	}
	return 0
}
