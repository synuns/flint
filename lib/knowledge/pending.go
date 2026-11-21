// pending.go — the pending knowledge review view. Relieves the bottleneck
// of the human gate (promotion) — rule-based three-stage recommendations
// (promote/merge/expire/hold) guide the review. The verdict is guidance
// only; approve·maintain do the execution (trust gate unchanged — LLM
// verdicts excluded, §13). Output is a human-readable table (inheriting the
// read format).
package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func init() { wire.Register("pending", RunPending) }

// pendingItem — one review row.
type pendingItem struct {
	ID                     int
	Topic, Summary, Origin string
	ValidAt                time.Time
}

// pendingVerdict — the rule-based verdict. Priority: expire > merge > promote > hold.
type pendingVerdict struct {
	Kind string // promote | merge | expire | hold
	Why  string
}

// CountPending — single query of the pending knowledge count (v0.34.3:
// converging the triple query duplication across status·nudge·sessionhook —
// the report.go track is excluded per v0.36). Only a query failure is an
// error; an empty response or unparseable value is 0 (the existing consumer
// convention: quiet zero handling). v0.39.0 T4 ruling: moved from
// lib/render to knowledge (the definition belongs to the pending domain —
// a render→knowledge import would be a cycle).
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

// RunPending — `flint pending` — the held knowledge list + recommendations.
// v1.0.1 (F3): unknown flags get the invalid-arg exit 3 (unified with the
// v0.36.1 maintain·import convention).
func RunPending(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	jsonMode := false
	for _, a := range args {
		if a == "--json" {
			jsonMode = true // machine-readable output for MCP·scripts
			continue
		}
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("unknown flag: %s — usage: flint pending [--json]", a)))
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
	// One query for all actives.
	// The result is built once into a prefix-key index — converging the
	// per-row full active rescan (hundreds of seconds at 50k synthetic rows)
	// to O(N+M).
	actives := fetchActiveTopicRows(ctx, st)
	idx := buildTopicPrefixIndex(actives)
	counts := map[string]int{}
	// Row structure for JSON mode — serializes the verdict data as-is.
	type jsonItem struct {
		ID           int    `json:"id"`
		Verdict      string `json:"verdict"`
		AgeDays      int    `json:"age_days"`
		SummaryRunes int    `json:"summary_runes"`
		Origin       string `json:"origin"`
		Topic        string `json:"topic"`
		Why          string `json:"why,omitempty"`
	}
	jsonItems := make([]jsonItem, 0) // [] not null when empty
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		// Re-sanitize stored values on output — neutralize control
		// characters·Cf·[flint] markers. Via the render choke point
		// (render.Text).
		it := pendingItem{
			Topic:   render.Text(fmt.Sprint(r[1])),
			Summary: render.Text(fmt.Sprint(r[2])),
			Origin:  render.Text(fmt.Sprint(r[3])),
		}
		fmt.Sscan(fmt.Sprint(r[0]), &it.ID)
		if ts, ok := cellToTime(r[4]); ok { // normal path with type-assertion fallback
			it.ValidAt = ts.UTC()
		}
		// Move the unparsable-valid_at guard ahead of the verdict·counts —
		// fixing the contradiction where the jsonMode-only override left
		// counts (original kind) and items (corrected kind) inconsistent;
		// the same applies in text mode.
		v := verdictFor(it, th, now, idx)
		if it.ValidAt.IsZero() {
			v = pendingVerdict{"hold", "valid_at unparsable — data check required"}
		}
		counts[v.Kind]++
		// Restore the corrected age of 0 for zero valid_at.
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

// verdictFor — the verdict rules (fixed priority):
//   - expire: valid_at elapsed > PendingAgeDays (inheriting the doctor baseline)
//   - merge: a similar active exists (topic prefix match — the same
//     approximation as approve R2)
//   - promote: within the last 3 days + body of 40 runes or more (a
//     significance approximation)
//   - hold: everything else (short recent rows etc. — accumulate more, then
//     decide)
func verdictFor(it pendingItem, th store.Thresholds, now time.Time, idx topicPrefixIndex) pendingVerdict {
	age := now.Sub(it.ValidAt)
	// the day spans share the DaysSpan clamp (the v1.19.2 overflow sweep) — a
	// pending_age_days·pending_promote_days override past the Duration ceiling
	// used to wrap the span negative (expire verdict on every row / promote
	// verdict on none).
	if d := th.PendingAgeDays; d > 0 && age > db.DaysSpan(d, now) {
		return pendingVerdict{"expire", fmt.Sprintf("%dd elapsed (threshold %dd) — expire review: maintain --invalidate --id %d --reason review", int(age.Hours()/24), d, it.ID)}
	}
	// Merge candidate — find similar actives (a lookup into the prebuilt
	// index).
	if sims := idx.match(topicSimilarityKey(it.Topic)); len(sims) > 0 {
		return pendingVerdict{"merge", fmt.Sprintf("similar active %d (id %s) — review approve --id %d --supersede OLD",
			len(sims), joinInts(sims), it.ID)}
	}
	if age <= db.DaysSpan(th.PendingPromoteDays, now) && len([]rune(it.Summary)) >= th.PendingPromoteMinRunes {
		return pendingVerdict{"promote", "recent write with sufficient body — approve --id " + fmt.Sprint(it.ID)}
	}
	return pendingVerdict{"hold", "short body or under observation — hold until next review"}
}

// fetchActiveTopicRows — one query of all active (id, topic) rows for the
// similarity verdict (removing per-row re-queries. Failure yields an empty
// set: only the merge verdict is skipped; the remaining verdict·output
// contracts are unchanged).
func fetchActiveTopicRows(ctx context.Context, st wire.Store) [][]any {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT id, topic FROM knowledge WHERE status = 'active' AND invalid_at IS NULL`, nil)
	if err != nil {
		return nil
	}
	return rows
}

// topicSimilarityKey — the key for similarity matching. Session-knowledge
// topics all share the "session[agent] " common prefix, so a 12-rune prefix
// comparison degenerates to the agent label + 1 rune — compare on the body
// key with the session prefix stripped.
func topicSimilarityKey(topic string) string {
	if i := strings.Index(topic, "] "); i > 0 &&
		(strings.HasPrefix(topic, "session[") || strings.HasPrefix(topic, "세션[")) {
		return topic[i+2:]
	}
	return topic
}

// topicPrefixIndex — similar-prefix key → active id list, from active (id,
// topic) rows (active query order preserved·same-prefix duplicates
// included). Converges matchTopicPrefix's per-row full active rescan,
// O(pending×active), into one index build + O(1) lookups. The prefix policy
// (12 runes after stripping the session common prefix)·row filter (parsable
// id·sufficient columns) are identical to the old scan implementation.
type topicPrefixIndex map[string][]int

// buildTopicPrefixIndex — builds the index once from active (id, topic)
// rows. Rows with an empty prefix key match no key (same as the original
// implementation) and are excluded.
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

// match — the active id list whose 12-rune prefix matches the key (query
// order·duplicates included).
func (idx topicPrefixIndex) match(key string) []int {
	prefix := render.TruncateRunes(key, 12)
	if prefix == "" || idx == nil {
		return nil
	}
	return idx[prefix]
}

// matchTopicPrefix — the ids of active (id, topic) rows whose 12-rune
// prefix matches the key. The single approximation implementation shared by
// approve·pending. Internally via the prefix index (scan loop abolished;
// verdict results identical to the old implementation).
func matchTopicPrefix(key string, actives [][]any) []int {
	return buildTopicPrefixIndex(actives).match(key)
}

// originLabel — abbreviated origin notation (hook/agent/import:file).
func originLabel(origin string) string {
	if strings.HasPrefix(origin, "import:") {
		return "import"
	}
	return origin
}

// verdictLabel — the display label for the verdict kind.
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

// ageDays — elapsed days (minimum 0).
func ageDays(now, t time.Time) int {
	d := int(now.Sub(t).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

// summaryLen — the summary's rune count.
func summaryLen(s string) int { return len([]rune(s)) }

// OneLine — one-line-ification (newline removal·cap — truncation delegated
// to the single TruncateRunes implementation). v0.39.0 T4 export: residual
// cli consumers (textutil_test's convention-delegation equivalence checks)
// compile unmodified via the knowledge.OneLine alias.
func OneLine(s string, max int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", "")
	return render.TruncateRunes(strings.TrimSpace(s), max)
}

// cellToTime — cell (any) → time conversion. Alias of the single
// wire.CellToTime implementation (resolving the cycle prohibition, same
// contract).
var cellToTime = wire.CellToTime

// joinInts — comma-joined (capped at 5).
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
