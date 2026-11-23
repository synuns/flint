package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func init() { wire.Register("pending", RunPending) }

type pendingItem struct {
	ID                     int
	Topic, Summary, Origin string
	ValidAt                time.Time
}

type pendingVerdict struct {
	Kind string
	Why  string
}

func CountPending(ctx context.Context, st wire.Store) (int, error) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT count(*) FROM knowledge WHERE status = 'pending' AND invalid_at IS NULL`, nil)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return 0, nil
	}
	n, _ := render.CellToInt(rows[0][0])
	return n, nil
}

func RunPending(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	jsonMode := false
	for _, a := range args {
		if a == "--json" {
			jsonMode = true
			continue
		}
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(i18n.T("err.unknown_flag")+" — "+i18n.T("usage.pending.json"), a)))
		return 3
	}
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	th := WriteThresholds(ctx, st)
	rows, err := wire.QueryOnce(ctx, st, `
		SELECT id, topic, summary, origin, valid_at
		FROM knowledge WHERE status = 'pending' AND invalid_at IS NULL
		ORDER BY valid_at, id`, nil)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	now := time.Now().UTC()

	actives := fetchActiveTopicRows(ctx, st)
	idx := buildTopicPrefixIndex(actives)
	counts := map[string]int{}

	type jsonItem struct {
		ID           int    `json:"id"`
		Verdict      string `json:"verdict"`
		AgeDays      int    `json:"age_days"`
		SummaryRunes int    `json:"summary_runes"`
		Origin       string `json:"origin"`
		Topic        string `json:"topic"`
		Why          string `json:"why,omitempty"`
	}
	jsonItems := make([]jsonItem, 0)
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}

		it := pendingItem{
			Topic:   render.Text(fmt.Sprint(r[1])),
			Summary: render.Text(fmt.Sprint(r[2])),
			Origin:  render.Text(fmt.Sprint(r[3])),
		}
		fmt.Sscan(fmt.Sprint(r[0]), &it.ID)
		if ts, ok := cellToTime(r[4]); ok {
			it.ValidAt = ts.UTC()
		}

		v := verdictFor(it, th, now, idx)
		if it.ValidAt.IsZero() {
			v = pendingVerdict{"hold", "valid_at unparsable — data check required"}
		}
		counts[v.Kind]++

		age := 0
		if !it.ValidAt.IsZero() {
			age = ageDays(now, it.ValidAt)
		}
		if jsonMode {
			jsonItems = append(jsonItems, jsonItem{
				ID: it.ID, Verdict: v.Kind, AgeDays: age,
				SummaryRunes: summaryLen(it.Summary), Origin: originLabel(it.Origin),
				Topic: OneLine(it.Topic, 80), Why: OneLine(v.Why, 120),
			})
		} else {
			fmt.Fprintf(stdout, "%d|%s|%dd|%dr|%s|%s\n",
				it.ID, verdictLabel(v.Kind), age, summaryLen(it.Summary), originLabel(it.Origin), OneLine(it.Topic, 48))
			if v.Why != "" {
				fmt.Fprintf(stdout, "  ↳ %s\n", OneLine(v.Why, 70))
			}
		}
	}
	if jsonMode {
		b, err := json.Marshal(map[string]any{
			"status": "ok",
			"items":  jsonItems,
			"counts": map[string]int{
				"pending": len(jsonItems), "promote": counts["promote"],
				"merge": counts["merge"], "expire": counts["expire"], "hold": counts["hold"],
			},
			"next": "approve --id N [--supersede OLD] · maintain --invalidate --id N --reason ...",
		})
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 1
		}
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	total := counts["promote"] + counts["merge"] + counts["expire"] + counts["hold"]
	if total == 0 {
		fmt.Fprintln(stdout, `{"status":"ok","pending":0,"next":"repopulated by session-end knowledge capture and import"}`)
		return 0
	}
	fmt.Fprintf(stdout, "{\"status\":\"ok\",\"pending\":%d,\"promote\":%d,\"merge\":%d,\"expire\":%d,\"hold\":%d}\n",
		total, counts["promote"], counts["merge"], counts["expire"], counts["hold"])
	return 0
}

func verdictFor(it pendingItem, th store.Thresholds, now time.Time, idx topicPrefixIndex) pendingVerdict {
	age := now.Sub(it.ValidAt)

	if d := th.PendingAgeDays; d > 0 && age > db.DaysSpan(d, now) {
		return pendingVerdict{"expire", fmt.Sprintf("%dd elapsed (threshold %dd) — expire review: maintain --invalidate --id %d --reason review", int(age.Hours()/24), d, it.ID)}
	}

	if sims := idx.match(topicSimilarityKey(it.Topic)); len(sims) > 0 {
		return pendingVerdict{"merge", fmt.Sprintf("similar active %d (id %s) — review approve --id %d --supersede OLD",
			len(sims), joinInts(sims), it.ID)}
	}
	if age <= db.DaysSpan(th.PendingPromoteDays, now) && len([]rune(it.Summary)) >= th.PendingPromoteMinRunes {
		return pendingVerdict{"promote", "recent write with sufficient body — approve --id " + fmt.Sprint(it.ID)}
	}
	return pendingVerdict{"hold", "short body or under observation — hold until next review"}
}

func fetchActiveTopicRows(ctx context.Context, st wire.Store) [][]any {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT id, topic FROM knowledge WHERE status = 'active' AND invalid_at IS NULL`, nil)
	if err != nil {
		return nil
	}
	return rows
}

func topicSimilarityKey(topic string) string {
	if i := strings.Index(topic, "] "); i > 0 &&
		(strings.HasPrefix(topic, "session[") || strings.HasPrefix(topic, "세션[")) {
		return topic[i+2:]
	}
	return topic
}

type topicPrefixIndex map[string][]int

func buildTopicPrefixIndex(actives [][]any) topicPrefixIndex {
	idx := topicPrefixIndex{}
	for _, r := range actives {
		if len(r) < 2 || r[0] == nil {
			continue
		}
		var id int
		if n, _ := fmt.Sscan(fmt.Sprint(r[0]), &id); n == 1 {
			if p := render.TruncateRunes(topicSimilarityKey(fmt.Sprint(r[1])), 12); p != "" {
				idx[p] = append(idx[p], id)
			}
		}
	}
	return idx
}

func (idx topicPrefixIndex) match(key string) []int {
	prefix := render.TruncateRunes(key, 12)
	if prefix == "" || idx == nil {
		return nil
	}
	return idx[prefix]
}

func matchTopicPrefix(key string, actives [][]any) []int {
	return buildTopicPrefixIndex(actives).match(key)
}

func originLabel(origin string) string {
	if strings.HasPrefix(origin, "import:") {
		return "import"
	}
	return origin
}

func verdictLabel(kind string) string {
	switch kind {
	case "promote":
		return "promote"
	case "merge":
		return "merge"
	case "expire":
		return "expire"
	default:
		return "hold"
	}
}

func ageDays(now, t time.Time) int {
	d := int(now.Sub(t).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

func summaryLen(s string) int { return len([]rune(s)) }

func OneLine(s string, max int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", "")
	return render.TruncateRunes(strings.TrimSpace(s), max)
}

var cellToTime = wire.CellToTime

func joinInts(ids []int) string {
	if len(ids) > 5 {
		ids = ids[:5]
	}
	parts := make([]string, len(ids))
	for i, v := range ids {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, ",")
}
