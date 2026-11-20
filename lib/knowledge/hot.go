// hot.go — flint hot: the injection summary. Fixed format + a cap from meta
// hot_budget (default 2,000). The session label parser·exposure profile
// read·hot stats load/persist·output length recording are cli-residual
// ownerships, wired through injection seams (*Fn vars) — cli init
// (knowledge_alias.go) injects the real implementations (cycle prevention:
// knowledge cannot import cli).
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

	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// DefaultHotBudget — the default cap when meta hot_budget is unset (TUNING.md §1).
const DefaultHotBudget = 2000

// SessionLabelFn — the injection point for the shared parser that extracts
// the --session value from args (cli sessionhook.go sessionLabel). cli init
// injects the real implementation.
var SessionLabelFn func(args []string) string

// ExposureProfileFn — the injection point for the exposure profile read
// (cli profile.go ExposureProfile). cli init injects the real
// implementation.
var ExposureProfileFn func(getKV func(string) (string, error)) string

// RecordHotLenFn — the injection point for recording the hot output
// character count (cli metrics.go RecordHotLen). cli init injects the real
// implementation.
var RecordHotLenFn func(n int)

// LoadHotStatsFromMetaFn — the injection point for loading the hot_stats
// accumulator (cli metrics.go). cli init injects the real implementation.
var LoadHotStatsFromMetaFn func(ctx context.Context, st wire.Store)

// PersistHotStatsToMetaFn — the injection point for persisting the hot_stats
// accumulator (cli metrics.go). cli init injects the real implementation.
var PersistHotStatsToMetaFn func(ctx context.Context, st wire.Store)

// HotBudget — meta hot_budget takes precedence; unset or invalid values
// fall back to the default 2,000 (not an error).
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

// MetaGetKV — wraps a meta key lookup into the getKV shape (unset·error
// yield def). v0.34.3 T2: the query body is delegated to the meta_kv single
// entry point (wire.GetMetaKVErr).
func MetaGetKV(ctx context.Context, st wire.Store) func(string, string) (string, error) {
	return func(key, def string) (string, error) {
		v, ok, err := wire.GetMetaKVErr(ctx, st, key)
		if err != nil || !ok {
			return def, err
		}
		return v, nil
	}
}

// HotFields — the hot output columns (the fixed order shared by the format·
// budget survival decision). Corresponds to the first 4 columns of the
// HotSQL SELECT — FormatRows reads kind·ts as positional fallbacks and
// topic·summary via the readColumns mapping (positions 1·2), so the trailing
// id·valid_at·ingested_at incremental columns never leak into the output.
const HotFields = "kind,topic,summary,ts"

// Indexes of the incremental·suffix-consumption columns at the tail of the
// HotSQL SELECT (single definition — reads id·valid_at·ingested_at exposed
// by hot_view directly from the base query).
const (
	hotViewIDIdx         = 4
	hotViewValidAtIdx    = 5
	hotViewIngestedAtIdx = 6 // for provenance checks (currently unconsumed — paired with the view exposure)
)

// ── Reflecting the decay-adjusted effective recall score in hot·brief selection ranking ──

// hotViewRecallScoreIdx·hotViewLastRecalledIdx — the ranking column indexes
// that hotRankedSQL appends to the tail of the hot_view base SELECT (single
// definition — since v1.9.0 T4 the columns are the recall_events derived
// aggregates re.cnt·re.last_at; the indexes are unchanged).
const (
	hotViewRecallScoreIdx  = 7
	hotViewLastRecalledIdx = 8
)

// hotHeadlineRecallScoreIdx·hotHeadlineRecallLastIdx — the ranking column
// indexes that hotRankedSQL appends to the headline query (HotHeadlineSQL:
// exposes through id only, 5 columns) — indexes 5·6 (reusing the --full
// indexes 7·8 would make the whole 7-column rows count as zero, silently
// nullifying headline ranking).
const (
	hotHeadlineRecallScoreIdx = 5
	hotHeadlineRecallLastIdx  = 6
)

// RecallAggregateSQL — the recall_events derived aggregate subquery (v1.9.0
// T4 single definition): per-knowledge event count (cnt)·last recall time
// (last_at). The ranking query (hotRankedSQL) and ops (maintain
// decay-audit) merge it with a LEFT JOIN — the derived replacement of the
// v11 knowledge.recall_score·last_recalled_at direct columns (dropped in
// schema v12). The exported constant is the single definition (the ops-side
// copy is abolished).
const RecallAggregateSQL = "SELECT knowledge_id, count(*)::int AS cnt, max(recalled_at) AS last_at FROM recall_events GROUP BY knowledge_id"

// EventEffectiveScore — the pure function for the event-count-based
// effective score (v1.9.0 T1 — recall append-only schema v12):
// max(0, count − rate×elapsed days). Never-recalled (count 0) is 0. days is
// integer elapsed time in 24h units (23h59m = 0 days); a fractional result
// floors toward 0 (int conversion). A future last_recalled·rate 0 or below
// keeps count as-is (reverse-growth·inflation defense), and count<=0 is 0
// (negative-count defense). v1.9.0 T4: the old EffectiveRecallScore
// (the v11 physical-column formula) is abolished — this function is the
// single formula definition (consumers: hot·brief ranking, ops decay-audit
// delegation).
func EventEffectiveScore(eventCount int, lastRecalled time.Time, now time.Time, decayPerDay float64) int {
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
	eff := float64(eventCount) - decayPerDay*float64(days)
	if eff < 0 {
		return 0
	}
	return int(eff)
}

// loadDecayRate — the decay coefficient prefers the meta override
// (inheriting the consumer fallback: load failure·unset·0 or below yield
// the default). It is ranking material, not a metered value, so failure is
// absorbed by the fallback (no effect on output·exit — same direction as
// §13).
func loadDecayRate(ctx context.Context, st wire.Store) float64 {
	rate := store.DefaultThresholds().DecayRate
	if th, err := wire.LoadThresholds(ctx, st); err == nil && th.DecayRate > 0 {
		rate = th.DecayRate
	}
	return rate
}

// rankRowsByEffectiveScore — stable-sorts query rows by decay-adjusted
// effective score, descending (v1.9.0 T4 — the formula has the single
// EventEffectiveScore definition: effective score =
// max(0, cnt − rate×elapsed days)). Rows without the scoreIdx·lastIdx
// columns (old view shape·fakes)·rows whose cnt cell cannot convert (NULL —
// project entries·zero recall events) count as zero. The stable sort
// inherits the existing SQL order (hot: ts DESC, id DESC / brief:
// RankOrder) as the tie order — an all-zero·ranking-column-free
// environment is byte-identical to the existing output.
func rankRowsByEffectiveScore(rows [][]any, scoreIdx, lastIdx int, rate float64, now time.Time) [][]any {
	eff := make([]int, len(rows))
	for i, r := range rows {
		if len(r) <= lastIdx {
			continue
		}
		score, ok := render.CellToInt(r[scoreIdx])
		if !ok {
			continue // cnt cell absent·NULL → zero score
		}
		last, _ := wire.CellToTime(r[lastIdx]) // last NULL+cnt>0 (fake shape) keeps cnt (T1 contract)
		eff[i] = EventEffectiveScore(score, last, now, rate)
	}
	// sort.SliceStable swaps only the idx slice, so the precomputed eff
	// array stays in place — the less closure reads the current ranking's
	// values via eff[idx[a]]·eff[idx[b]], and the idx reassembly (out)
	// fixes the final row order (partial sorting abolished).
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

// hotRankJoinPivot — the fixed FROM/ORDER BY clause of query.HotSQL·
// HotHeadlineSQL (the pivot for the ranking join substitution — single
// definition derived).
const hotRankJoinPivot = " FROM hot_view ORDER BY ts DESC, id DESC"

// hotRankedSQL — appends cnt·last_at to the SELECT tail of the single
// hot_view query (single definition = query.HotSQL·HotHeadlineSQL) via a
// recall_events derived aggregate join (v1.9.0 T4 — the switch following
// the removal of the v11 knowledge direct columns. Even with the recall
// source moved to append-only events, the injection query keeps the
// single-query contract). Columns are h.-qualified (post-join ambiguity
// removed); ORDER BY·LIMIT inherit the original definition verbatim — if
// the pivot is not found (a future SQL change) the original is returned,
// ranking is silently skipped, and existing behavior is inherited.
func hotRankedSQL(base string) string {
	i := strings.Index(base, hotRankJoinPivot)
	if i <= 0 || !strings.HasPrefix(base, "SELECT ") {
		return base
	}
	cols := strings.Split(strings.TrimPrefix(base[:i], "SELECT "), ", ")
	q := make([]string, 0, len(cols)+2)
	for _, c := range cols {
		q = append(q, "h."+c)
	}
	q = append(q, "re.cnt", "re.last_at")
	return "SELECT " + strings.Join(q, ", ") +
		" FROM hot_view h LEFT JOIN (" + RecallAggregateSQL + ") re ON re.knowledge_id = h.id ORDER BY h.ts DESC, h.id DESC" +
		base[i+len(hotRankJoinPivot):]
}

// hotLineSurvivorCount — the per-line survival count against the 'displayed
// string' (N4). Measured on the final text including the --full suffix
// (·+Nd): the start offset of line k equals the rune count of lines[:k]
// (including the separating newlines), and only lines whose offset is under
// budget survive. It reproduces the same string that ClampHot truncates, so
// there is no inconsistency where a line pushed out by its suffix gets cut
// from the actual output yet still counted as surviving and incremented.
func hotLineSurvivorCount(lines []string, budget int) int {
	n, off := 0, 0
	for i, ln := range lines {
		if i > 0 {
			off++ // separating newline
		}
		if off >= budget {
			break
		}
		off += utf8.RuneCountInString(ln)
		n++
	}
	return n
}

// injectedIDsForSurvivors — collects the actually injected knowledge ids
// directly from the hot_view base rows (single definition): takes the id
// cell (hotViewIDIdx) of the surviving survivor rows excluding project
// entries (id NULL). The injection output and the increment source are the
// same query·same order (ORDER BY ts DESC, id DESC tie-break), so
// injected set = increment set holds structurally — equivalent even after
// abolishing the alignment contract with a companion query that re-gathered
// the three-entry conditions. A missing id cell (old view·fake)·unconvertible
// values are silently skipped (a cut line's id is not incremented).
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

// PendingAgeSuffix — the pending-entry wait-days suffix (v0.12.1 R2): empty
// string when valid_at elapsed is under 24 hours, otherwise floored days as
// "·+Nd".
func PendingAgeSuffix(validAt time.Time, now time.Time) string {
	if days := int64(now.Sub(validAt) / (24 * time.Hour)); days > 0 {
		return fmt.Sprintf("·+%dd", days)
	}
	return ""
}

// hotSuffixedLines — the per-line display strings of the full --full output
// (suffix included, N4). Output assembly and survival accounting share this
// string so that the ClampHot truncation target — the actual output text —
// and the increment survival decision (hotLineSurvivorCount) stay aligned.
// Only pending line endings get the wait-days suffix — valid_at is read
// directly from the hot_view base column (hotViewValidAtIdx, single
// definition) (companion query abolished). A missing cell·non-time.Time
// value·embedded newlines (safety guard) skip the suffix and return the
// base-format line (existing output inherited unchanged). Empty rows yield
// nil. now is UTC.
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
	// Safety guard: if a field value embeds a newline, one FormatRows row
	// splits into several lines and the lines[i]↔rows[i] correspondence
	// breaks — skip the suffix entirely when the counts differ so no suffix
	// lands on the wrong line (existing output inherited unchanged).
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

// hotFullOutput — assembles the per-line display strings (suffix included)
// into the full --full output text. nil (empty rows) inherits the existing
// empty output ("").
func hotFullOutput(lines []string) string {
	if lines == nil {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// HotHeadline — the one-line hot summary headline (R2). minimal gets only
// the active·pending counts; every other profile (unset·corrupt included)
// gets the normal wording. v0.18.0 R2: normal carries the K most recent
// decision topics + a recall hint — if injection carried only counts, the
// next session would not know what to search and read would starve
// (observed: read:write imbalance). A session without topics falls back to
// the existing count+expand wording (the wording enumeration is data — the
// same class as the expand guidance, §13 boundary respected).
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

// hotHeadlineTopicMax — the headline topic rune cap (… suffix when
// truncated).
const hotHeadlineTopicMax = 16

// hotHeadlineTopicCount — how many topics to list in the headline (top K).
const hotHeadlineTopicCount = 3

// HotTopics — picks the top K decision topics from hot_view query rows
// (newest first). Same-topic dedup (only the newest kept)·rune truncation.
// Topics serve as search queries, so the original prefix is preserved (16
// runes + …).
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

// clampHeadlineTopic — truncates a topic to the rune cap (… suffix when
// over).
func clampHeadlineTopic(s string) string {
	runes := []rune(s)
	if len(runes) <= hotHeadlineTopicMax {
		return s
	}
	return string(runes[:hotHeadlineTopicMax]) + "…"
}

// hotCounts — extracts the headline counts from hot_view query rows (the
// same data source as the full output): active = project entries, pending =
// pending, recent decisions = 7 days.
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

// RunHot — queries hot_view, then: with --full prints the existing full
// output (byte-invariant); otherwise prints only the profile-specific
// one-line headline (counts extracted from the same query; full assembly
// skipped). Metering·exit·--session are invariant across both paths.
// T38 wiring (F-T56-1): loads the meta hot_stats accumulator at start and
// persists it just before exit (preserving single-shot CLI process
// separation). Both are metering, so failures are quietly ignored (§13 —
// no effect on hot output·exit).
func RunHot(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// The session label goes through the shared parser (SessionLabelFn —
	// injected cli sessionLabel) — a single source with the session-start
	// boundary record. The --full check does not re-read as a flag the token
	// consumed as the session value (removing the existing full=true side
	// effect of the contrived input `--session --full` — the same direction
	// as read's flag-shaped-value defense). The --session label is validated
	// by the same shared guard as read (validateSessionLabel) — empty
	// values·'-'-prefixed typos get the invalid-arg exit 3.
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
		// Unknown `-`-prefixed args get the invalid-arg exit 3 — abolishing
		// the existing quiet ignore (exit 0). A quiet success hides mistyped
		// flags.
		if strings.HasPrefix(args[i], "-") {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("unknown flag: %s — usage: flint hot [--session ID] [--full]", args[i])))
			return 3
		}
	}
	if err := validateSessionLabel(sessionGiven, session); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	sql := query.HotSQL(session)
	if !full {
		// The headline path uses the count-distortion-prevention cap (50) —
		// injection (--full) keeps the budget-cap contract unchanged.
		sql = query.HotHeadlineSQL(session)
	}
	// Merge the ranking columns (derived cnt·last_at — recall_events
	// aggregates) into the single query — no companion query (order =
	// increment structure inherited).
	sql = hotRankedSQL(sql)
	rows, err := wire.QueryOnce(ctx, st, sql, nil)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// Output re-sanitization (ADR-0006): the same defense as read — so
	// [flint] markers·control characters introduced after storage
	// (restore·direct SQL·pre-sanitization data) cannot forge the
	// session-start injection boundary. hot is the foremost output path
	// injected directly into the prompt by hooks. Sanitization converges on
	// the same render choke point as read (render.Cells).
	rows = render.Cells(rows)
	// Reflect the decay-adjusted effective recall score in selection
	// ranking — effective DESC stable sort (ties·zero scores inherit the
	// existing SQL order ts DESC, id DESC). Rows without ranking columns
	// (old view·fake)·never-recalled (cnt NULL) count as zero, so existing
	// order·output survive as-is. Format·budget·increments are downstream
	// consumers of these rows, keeping the order = increment contract
	// unchanged. The ranking column indexes follow the per-path query shape
	// (--full 9 columns·headline 7 columns).
	scoreIdx, lastIdx := hotViewRecallScoreIdx, hotViewLastRecalledIdx
	if !full {
		scoreIdx, lastIdx = hotHeadlineRecallScoreIdx, hotHeadlineRecallLastIdx
	}
	rows = rankRowsByEffectiveScore(rows, scoreIdx, lastIdx,
		loadDecayRate(ctx, st), time.Now().UTC())
	// Load the baseline just before recording, after a successful query
	// (the query-failure path skips metering).
	LoadHotStatsFromMetaFn(ctx, st)
	// The budget is computed commonly for both paths: --full truncation
	// rules and the increment survival decision share the same budget
	// (metering invariant).
	budget := HotBudget(MetaGetKV(ctx, st))
	// Per-line text including suffixes (N4) — shared by the --full output
	// and the increment survival decision.
	var lines []string
	if full {
		lines = hotSuffixedLines(rows, time.Now().UTC())
		clamped := query.ClampHot(hotFullOutput(lines), budget)
		fmt.Fprint(stdout, clamped)
		RecordHotLenFn(len([]rune(clamped)))
		// G5 byte metering: the final output length 'after' ClampHot
		// truncation (budget-compliance measurement — not pre-truncation,
		// spec §1). Quiet (§13).
		recordBytes(ctx, st, "hot", len(clamped))
	} else {
		kv := MetaGetKV(ctx, st)
		profile := ExposureProfileFn(func(key string) (string, error) { return kv(key, "") })
		active, pending, recent := hotCounts(rows)
		line := HotHeadline(profile, active, pending, recent, HotTopics(rows, hotHeadlineTopicCount))
		fmt.Fprintln(stdout, line)
		RecordHotLenFn(len([]rune(line)))
		// G5 byte metering: one headline line + newline — the total written
		// (denominator consistency). Quiet (§13).
		recordBytes(ctx, st, "hot", len(line)+1)
	}
	PersistHotStatsToMetaFn(ctx, st)
	// Metering: increment references only for knowledge ids actually
	// injected (left in the post-budget-truncation output) + one hot
	// intake. Quiet (§13). Knowledge injection happens only on the --full
	// full-output path (N3) — the headline path exposes counts only, so it
	// records just one intake without increments that would distort
	// read_count·last_read_at and freeze stale decisions. Survival is
	// decided against the suffix-included text (N4): if a suffix pushed a
	// line out and it was cut from the actual output, it is not
	// incremented. The incremented ids are picked directly from the id
	// column of the base hot_view query (single definition).
	if !full {
		RecordUsage(ctx, st, "hot", "")
		// The headline (non --full) exposed-id set is also left in recall
		// history with kind=hot (spec §2.1 — only the --full path injects,
		// but the headline is also an exposure surface that induces recall,
		// so it belongs in the G4 account). Ids are recovered from the
		// headline query's id column (the HotSQL single-definition
		// convention — project entries with id NULL excluded; a missing
		// cell·unconvertible values are silently skipped). The read_count
		// cache (BumpReads) stays untouched on the headline path.
		recordKnowledgeReads(ctx, st, injectedIDsForSurvivors(rows, len(rows)), session, "hot")
	} else {
		injected := injectedIDsForSurvivors(rows, hotLineSurvivorCount(lines, budget))
		InstrumentReads(ctx, st, "hot", injected, false, false)
		// The injected-id set is also left in recall history with kind=hot
		// (injected = incremented = recorded set — same query·same order
		// source). Quiet (§13).
		recordKnowledgeReads(ctx, st, injected, session, "hot")
		// Record the actually injected knowledge ids in hot_events as an
		// exposure record — the source for hot-exposure correction of
		// dead_pct. Failure is quiet (§13 metering principle).
		if rec, ok := st.(HotEventRecorder); ok {
			_ = rec.RecordHotEvent(ctx, idsCSV(injected))
		}
	}
	// A separate commit for the metering table (once on the hot completion
	// path — common to headline·--full). Failure is quiet (§13 — staging
	// survives, so the next call recovers it).
	commitTelemetry(ctx, st, "hot")
	return 0
}

// HotEventRecorder — the hot --full injection exposure recording interface
// (v0.20.0). realStore implements it via db.Repo.RecordHotEvent
// (cli/realstore_read.go); a non-implementing Store (fake) skips it.
type HotEventRecorder interface {
	RecordHotEvent(ctx context.Context, ids string) error
}
