package knowledge

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

const DefaultHotBudget = 2000

var SessionLabelFn func(args []string) string

var ExposureProfileFn func(getKV func(string) (string, error)) string

var RecordHotLenFn func(n int)

var LoadHotStatsFromMetaFn func(ctx context.Context, st wire.Store)

var PersistHotStatsToMetaFn func(ctx context.Context, st wire.Store)

var PersistScanStatsFn func(ctx context.Context, st wire.Store)

func HotBudget(getKV func(string, string) (string, error)) int {
	v, err := getKV("hot_budget", "")
	if err != nil || v == "" {
		return DefaultHotBudget
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return DefaultHotBudget
	}
	return n
}

func MetaGetKV(ctx context.Context, st wire.Store) func(string, string) (string, error) {
	return func(key, def string) (string, error) {
		v, ok, err := wire.GetMetaKVErr(ctx, st, key)
		if err != nil || !ok {
			return def, err
		}
		return v, nil
	}
}

const HotFields = "kind,topic,summary,ts"

const (
	hotViewIDIdx         = 4
	hotViewValidAtIdx    = 5
	hotViewIngestedAtIdx = 6
)

const (
	hotViewRecallScoreIdx  = 7
	hotViewLastRecalledIdx = 8

	hotViewTagsIdx = 9
)

const (
	hotHeadlineRecallScoreIdx = 5
	hotHeadlineRecallLastIdx  = 6

	hotHeadlineTagsIdx = 7
)

const RecallAggregateSQL = "SELECT knowledge_id, count(*)::int AS cnt, max(recalled_at) AS last_at FROM recall_events GROUP BY knowledge_id"

func EventEffectiveScore(eventCount int, lastRecalled time.Time, now time.Time, decayPerDay float64, survived int, relief float64) int {
	if eventCount <= 0 {
		return 0
	}
	if lastRecalled.IsZero() || decayPerDay <= 0 {
		return eventCount
	}
	days := int(now.Sub(lastRecalled).Hours() / 24)
	if days <= 0 {
		return eventCount
	}
	decay := decayPerDay*float64(days) - float64(survived)*relief
	if decay < 0 {
		decay = 0
	}
	eff := float64(eventCount) - decay
	if eff < 0 {
		return 0
	}
	return int(eff)
}

const volImmutableMarker = ":vol:immutable:"

func EffectiveScoreFor(eventCount int, lastRecalled time.Time, now time.Time, decayPerDay float64, tags string, survived int, relief float64) int {
	if strings.Contains(tags, volImmutableMarker) {
		if eventCount <= 0 {
			return 0
		}
		return eventCount
	}
	return EventEffectiveScore(eventCount, lastRecalled, now, decayPerDay, survived, relief)
}

func loadThresholdsOnce(ctx context.Context, st wire.Store) store.Thresholds {
	if th, err := wire.LoadThresholds(ctx, st); err == nil {
		return th
	}
	return store.DefaultThresholds()
}

func decayRateOf(th store.Thresholds) float64 {
	if th.DecayRate > 0 {
		return th.DecayRate
	}
	return store.DefaultThresholds().DecayRate
}

func survivedReliefOf(th store.Thresholds) float64 {
	if th.SurvivedDecayRelief > 0 {
		return th.SurvivedDecayRelief
	}
	return store.DefaultThresholds().SurvivedDecayRelief
}

func loadDecayRate(ctx context.Context, st wire.Store) float64 {
	return decayRateOf(loadThresholdsOnce(ctx, st))
}

func loadSurvivedRelief(ctx context.Context, st wire.Store) float64 {
	return survivedReliefOf(loadThresholdsOnce(ctx, st))
}

func rankRowsByEffectiveScore(ctx context.Context, st wire.Store, rows [][]any, idIdx, scoreIdx, lastIdx, tagsIdx int, rate, relief float64, now time.Time) [][]any {

	var survived map[int]int
	if relief > 0 {
		ids := make([]int, 0, len(rows))
		seen := map[int]bool{}
		for _, r := range rows {
			if len(r) <= idIdx {
				continue
			}
			if id, ok := render.CellToInt(r[idIdx]); ok && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		survived, _ = SurvivedCounts(ctx, st, ids)
	}
	eff := make([]int, len(rows))
	for i, r := range rows {
		if len(r) <= lastIdx {
			continue
		}
		score, ok := render.CellToInt(r[scoreIdx])
		if !ok {
			continue
		}
		last, _ := wire.CellToTime(r[lastIdx])
		var tags string
		if len(r) > tagsIdx {
			if s, ok := r[tagsIdx].(string); ok {
				tags = s
			}
		}
		var sv int
		if len(r) > idIdx {
			if id, ok := render.CellToInt(r[idIdx]); ok {
				sv = survived[id]
			}
		}
		eff[i] = EffectiveScoreFor(score, last, now, rate, tags, sv, relief)
	}

	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return eff[idx[a]] > eff[idx[b]] })
	out := make([][]any, len(rows))
	for pos, src := range idx {
		out[pos] = rows[src]
	}
	return out
}

const hotRankJoinPivot = " FROM hot_view ORDER BY ts DESC, id DESC"

func hotRankedSQL(base string) string {
	i := strings.Index(base, hotRankJoinPivot)
	if i <= 0 || !strings.HasPrefix(base, "SELECT ") {
		return base
	}
	cols := strings.Split(strings.TrimPrefix(base[:i], "SELECT "), ", ")
	q := make([]string, 0, len(cols)+3)
	for _, c := range cols {
		q = append(q, "h."+c)
	}
	q = append(q, "re.cnt", "re.last_at", "vtag.tags")
	return "SELECT " + strings.Join(q, ", ") +
		" FROM hot_view h LEFT JOIN knowledge vtag ON vtag.id = h.id" +
		" LEFT JOIN (" + RecallAggregateSQL + ") re ON re.knowledge_id = h.id ORDER BY h.ts DESC, h.id DESC" +
		base[i+len(hotRankJoinPivot):]
}

func hotLineSurvivorCount(lines []string, budget int) int {
	n, off := 0, 0
	for i, ln := range lines {
		if i > 0 {
			off++
		}
		if off >= budget {
			break
		}
		off += utf8.RuneCountInString(ln)
		n++
	}
	return n
}

func injectedIDsForSurvivors(rows [][]any, survivors int) []int {
	injected := make([]int, 0, survivors)
	for i := 0; i < survivors && i < len(rows); i++ {
		r := rows[i]
		if len(r) <= hotViewIDIdx || fmt.Sprint(r[0]) == "project" {
			continue
		}
		if id, ok := render.CellToInt(r[hotViewIDIdx]); ok {
			injected = append(injected, id)
		}
	}
	return injected
}

func PendingAgeSuffix(validAt time.Time, now time.Time) string {
	if days := int64(now.Sub(validAt) / (24 * time.Hour)); days > 0 {
		return fmt.Sprintf("·+%dd", days)
	}
	return ""
}

func hotSuffixedLines(rows [][]any, now time.Time) []string {
	base := query.FormatRows(rows, HotFields, false)
	if base == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(base, "\n"), "\n")
	hasPending := false
	for _, r := range rows {
		if len(r) > 0 && fmt.Sprint(r[0]) == "pending" {
			hasPending = true
			break
		}
	}
	if !hasPending {
		return lines
	}

	if len(lines) != len(rows) {
		return lines
	}
	for i, r := range rows {
		if i >= len(lines) {
			break
		}
		if len(r) <= hotViewValidAtIdx || fmt.Sprint(r[0]) != "pending" {
			continue
		}
		if va, ok := r[hotViewValidAtIdx].(time.Time); ok {
			lines[i] += PendingAgeSuffix(va, now)
		}
	}
	return lines
}

func hotFullOutput(lines []string) string {
	if lines == nil {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func HotHeadline(profile string, active int, pending int, recent int, topics []string) string {
	if profile == "minimal" {
		return fmt.Sprintf("[flint] active %d · pending %d", active, pending)
	}
	if len(topics) > 0 {
		return fmt.Sprintf("[flint] active %d · pending %d · recent decisions: %s — recall: flint read <query>",
			active, pending, strings.Join(topics, " · "))
	}
	return fmt.Sprintf("[flint] active %d · pending %d · recent decisions %d — expand: flint hot --full",
		active, pending, recent)
}

const hotHeadlineTopicMax = 16

const hotHeadlineTopicCount = 3

func HotTopics(rows [][]any, k int) []string {
	if k <= 0 {
		return nil
	}
	topics := make([]string, 0, k)
	seen := map[string]bool{}
	for _, r := range rows {
		if len(r) < 2 || fmt.Sprint(r[0]) != "decision" {
			continue
		}
		topic := strings.TrimSpace(fmt.Sprint(r[1]))
		if topic == "" || seen[topic] {
			continue
		}
		seen[topic] = true
		topics = append(topics, clampHeadlineTopic(topic))
		if len(topics) == k {
			break
		}
	}
	return topics
}

func clampHeadlineTopic(s string) string {
	runes := []rune(s)
	if len(runes) <= hotHeadlineTopicMax {
		return s
	}
	return string(runes[:hotHeadlineTopicMax]) + "…"
}

func hotCounts(rows [][]any) (active, pending, recent int) {
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		switch fmt.Sprint(r[0]) {
		case "project":
			active++
		case "pending":
			pending++
		case "decision":
			recent++
		}
	}
	return active, pending, recent
}

const hotSnapshotKind = "state"

const hotClampSuffixRunes = 12

func hotSnapshotRuneCap(budget int) int { return budget / 5 }

func hotSnapshotCell(s string) string {
	return strings.ReplaceAll(render.Text(s), "|", " ")
}

func hotTruncate(s string, n int) string {
	r := []rune(s)
	if n < 0 {
		return ""
	}
	if len(r) <= n {
		return s
	}
	if n == 0 {
		return ""
	}
	return string(r[:n-1]) + "…"
}

func hotSnapshotRowTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		if tt, err := time.Parse(time.RFC3339, t); err == nil {
			return tt, true
		}
	}
	return time.Time{}, false
}

func hotSnapshotStateRows(ctx context.Context, st wire.Store) []map[string]any {
	if ws, ok := st.(workStateStore); ok {
		return ws.ListOpenWorkState(ctx, 0, stateListLimit)
	}
	return nil
}

func hotRowProjectID(v any) int {
	switch t := v.(type) {
	case *int:
		if t == nil {
			return 0
		}
		return *t
	}
	n, _ := render.CellToInt(v)
	return n
}

func hotSnapshotLines(th store.Thresholds, budget int, rows []map[string]any, currentProjectID int, now time.Time) []string {
	staleDays, snapMax := th.StateStaleDays, th.StateSnapshotMax
	if staleDays <= 0 {
		staleDays = store.DefaultThresholds().StateStaleDays
	}
	if snapMax <= 0 {
		snapMax = store.DefaultThresholds().StateSnapshotMax
	}
	cutoff := now.AddDate(0, 0, -staleDays)
	fresh := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if u, ok := hotSnapshotRowTime(row["updated_at"]); ok && u.Before(cutoff) {
			continue
		}
		fresh = append(fresh, row)
	}
	if len(fresh) == 0 {
		return nil
	}
	sort.SliceStable(fresh, func(a, b int) bool {
		if currentProjectID > 0 {
			pa := hotRowProjectID(fresh[a]["project_id"])
			pb := hotRowProjectID(fresh[b]["project_id"])
			if (pa == currentProjectID) != (pb == currentProjectID) {
				return pa == currentProjectID
			}
		}
		ua, _ := hotSnapshotRowTime(fresh[a]["updated_at"])
		ub, _ := hotSnapshotRowTime(fresh[b]["updated_at"])
		return ua.After(ub)
	})
	if len(fresh) > snapMax {
		fresh = fresh[:snapMax]
	}
	cap := hotSnapshotRuneCap(budget)
	lines := make([]string, 0, len(fresh))
	used := 0
	for _, row := range fresh {
		task, _ := row["task"].(string)
		next, _ := row["next_action"].(string)
		ln := hotSnapshotKind + "|" + hotSnapshotCell(task) + "|" + hotSnapshotCell(next)
		if n := utf8.RuneCountInString(ln); used+n > cap {
			ln = hotTruncate(ln, cap-used)
			n = utf8.RuneCountInString(ln)
			if n == 0 {
				break
			}
			lines = append(lines, ln)
			used += n
			break
		}
		used += utf8.RuneCountInString(ln)
		lines = append(lines, ln)
	}
	return lines
}

func RunHot(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {

	session := SessionLabelFn(args)
	sessionGiven := false
	full := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" {
			sessionGiven = true
			if i+1 < len(args) {
				i++
			}
			continue
		}
		if args[i] == "--full" {
			full = true
			continue
		}

		if strings.HasPrefix(args[i], "-") {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(i18n.T("err.unknown_flag")+" — "+i18n.T("usage.hot"), wire.MaskToken(args[i]))))
			return 3
		}
	}
	if err := validateSessionLabel(sessionGiven, session); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	sql := query.HotSQL(session)
	if !full {

		sql = query.HotHeadlineSQL(session)
	}

	sql = hotRankedSQL(sql)
	rows, err := wire.QueryOnce(ctx, st, sql, nil)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

	rows = render.Cells(rows)

	scoreIdx, lastIdx, tagsIdx := hotViewRecallScoreIdx, hotViewLastRecalledIdx, hotViewTagsIdx
	if !full {
		scoreIdx, lastIdx, tagsIdx = hotHeadlineRecallScoreIdx, hotHeadlineRecallLastIdx, hotHeadlineTagsIdx
	}
	th := loadThresholdsOnce(ctx, st)
	rows = rankRowsByEffectiveScore(ctx, st, rows, hotViewIDIdx, scoreIdx, lastIdx, tagsIdx,
		decayRateOf(th), survivedReliefOf(th), time.Now().UTC())

	LoadHotStatsFromMetaFn(ctx, st)

	budget := HotBudget(MetaGetKV(ctx, st))
	projSession := session
	if projSession == "" {
		if label, ok := wire.ReadSessionLabel(); ok {
			projSession = label
		}
	}
	projectID := 0
	if projSession != "" {
		if id, err := st.LatestProjectForSession(ctx, projSession); err == nil {
			projectID = id
		}
	}
	snap := hotSnapshotLines(th, budget, hotSnapshotStateRows(ctx, st), projectID, time.Now().UTC())

	var lines []string
	if full {
		lines = hotSuffixedLines(rows, time.Now().UTC())
		snapRunes := 0
		for _, ln := range snap {
			snapRunes += utf8.RuneCountInString(ln) + 1
		}
		limit := budget - snapRunes
		if snapRunes > 0 {
			limit -= hotClampSuffixRunes
		}
		if limit < 0 {
			limit = 0
		}
		clamped := query.ClampHot(hotFullOutput(lines), limit)
		out := clamped
		for _, ln := range snap {
			out += ln + "\n"
		}
		fmt.Fprint(stdout, out)
		RecordHotLenFn(len([]rune(clamped)))

		recordBytes(ctx, st, "hot", len(out))
	} else {
		kv := MetaGetKV(ctx, st)
		profile := ExposureProfileFn(func(key string) (string, error) { return kv(key, "") })
		active, pending, recent := hotCounts(rows)
		line := HotHeadline(profile, active, pending, recent, HotTopics(rows, hotHeadlineTopicCount))
		fmt.Fprintln(stdout, line)
		for _, ln := range snap {
			fmt.Fprintln(stdout, ln)
		}
		RecordHotLenFn(len([]rune(line)))

		n := len(line) + 1
		for _, ln := range snap {
			n += len(ln) + 1
		}
		recordBytes(ctx, st, "hot", n)
	}
	PersistHotStatsToMetaFn(ctx, st)

	if !full {
		RecordUsage(ctx, st, "hot", "")

		recordKnowledgeReads(ctx, st, injectedIDsForSurvivors(rows, len(rows)), session, "hot")
	} else {
		injected := injectedIDsForSurvivors(rows, hotLineSurvivorCount(lines, budget))
		InstrumentReads(ctx, st, "hot", injected, false, false)

		recordKnowledgeReads(ctx, st, injected, session, "hot")

		if rec, ok := st.(HotEventRecorder); ok {
			_ = rec.RecordHotEvent(ctx, idsCSV(injected))
		}
	}

	commitTelemetry(ctx, st, "hot")
	return 0
}

type HotEventRecorder interface {
	RecordHotEvent(ctx context.Context, ids string) error
}
