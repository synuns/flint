// metrics.go — hot output-length metering (in-process stats + persistent meta hot_stats accumulation).
// A one-shot CLI persists at exit and loads at start, preserving values across runs.
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/wire"
)

// hotMaxSamples — the cap on kept samples. When exceeded, the oldest are dropped first.
const hotMaxSamples = 500

// hotMu — protects hotLens against concurrent access (including multiple MCP sessions).
var hotMu sync.Mutex

// hotLens — the slice of hot output-length (rune count) samples.
var hotLens []int

// RecordHotLen — records the character count right after hot output.
func RecordHotLen(n int) {
	hotMu.Lock()
	defer hotMu.Unlock()
	hotLens = append(hotLens, n)
	if over := len(hotLens) - hotMaxSamples; over > 0 {
		hotLens = hotLens[over:]
		// if a truncation shaves the front of persisted, the offset shrinks too —
		// left as-is, as many pending samples as were truncated go missing and are under-reported.
		// if the truncation crosses the persisted boundary,
		// all remaining kept samples become unrecorded -> offset 0.
		if over >= hotPersisted {
			hotPersisted = 0
		} else {
			hotPersisted -= over
		}
	}
}

// HotStats — calls (baseline + kept sample count), p95 (approximation: ascending-sorted index
// ceil(0.95*n)-1), and max. The baseline is the previous run's cumulative loaded by LoadHotStats.
func HotStats() (calls int, p95 int, max int) {
	hotMu.Lock()
	defer hotMu.Unlock()
	calls = hotBase.Calls + len(hotPending())
	if calls == 0 {
		return 0, 0, 0
	}
	lens := append(append([]int(nil), hotBase.Lens...), hotPending()...)
	sort.Ints(lens)
	if len(lens) == 0 {
		return calls, 0, 0
	}
	return calls, HotP95(lens), lens[len(lens)-1]
}

// hotPending — the in-memory samples not yet added to the baseline (unrecorded).
// The caller must hold hotMu.
func hotPending() []int {
	if hotPersisted <= 0 {
		return hotLens
	}
	if hotPersisted >= len(hotLens) {
		return nil
	}
	return hotLens[hotPersisted:]
}

// HotP95 — the approximate p95 from sorted samples (index ceil(0.95*n)-1).
// Exported: the cli report.go effect-metric computation consumes it.
func HotP95(sorted []int) int {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := (n*95 + 99) / 100 // ceil(0.95*n)
	if idx > n {
		idx = n
	}
	return sorted[idx-1]
}

// hotStatsJSON — the persistent meta hot_stats format (calls, sum, p95 representative lens).
type hotStatsJSON struct {
	Calls int   `json:"calls"`
	Sum   int   `json:"sum"`
	Lens  []int `json:"lens"`
}

// hotBase — the cumulative value up to the previous run, loaded at start (0 when unloaded or corrupt).
var hotBase hotStatsJSON

// hotPersisted — the number of samples at the front of hotLens already added to the baseline (saved).
// Even after persist, the in-memory samples remain but are excluded from double counting.
var hotPersisted int

// LoadHotStats — loads meta hot_stats at start. Returns cumulative calls and p95;
// on missing key, error, or corrupt JSON it falls back to defaults (ok=false, baseline reset).
// It does not touch hotPersisted — the exclusion of already-saved in-memory samples from
// double counting is preserved (it is 0 anyway in separate-process runs).
func LoadHotStats(getKV func(string) (string, error)) (calls int, p95 int, ok bool) {
	v, err := getKV("hot_stats")
	hotMu.Lock()
	defer hotMu.Unlock()
	hotBase = hotStatsJSON{}
	if err != nil || v == "" {
		return 0, 0, false
	}
	var st hotStatsJSON
	if json.Unmarshal([]byte(v), &st) != nil {
		return 0, 0, false // corrupt -> default fallback
	}
	hotBase = st
	lens := make([]int, len(st.Lens))
	copy(lens, st.Lens)
	sort.Ints(lens)
	return st.Calls, HotP95(lens), true
}

// PersistHotStats — stores the cumulative meta hot_stats at exit.
// The previous run's baseline (loaded by LoadHotStats) + unsaved in-memory samples.
// lens keeps the most recent samples up to hotMaxSamples for p95-representative purposes.
// On a successful save, the summed portion moves to the baseline but the in-memory samples stay
// (double counting is excluded via the hotPersisted offset — supporting a query right after RecordHotLen).
func PersistHotStats(setKV func(string, string) error) error {
	hotMu.Lock()
	cur := append([]int(nil), hotPending()...)
	snapLen := len(hotLens)
	base := hotBase
	hotMu.Unlock()

	st := hotStatsJSON{Calls: base.Calls + len(cur), Sum: base.Sum + hotSum(cur)}
	st.Lens = append(append([]int(nil), base.Lens...), cur...)
	if over := len(st.Lens) - hotMaxSamples; over > 0 {
		st.Lens = st.Lens[over:]
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := setKV("hot_stats", string(b)); err != nil {
		return err
	}
	hotMu.Lock()
	hotBase = st
	if hotPersisted < snapLen {
		hotPersisted = snapLen
	}
	hotMu.Unlock()
	return nil
}

// ResetMetrics — resets the in-process stats (supporting test isolation — inheriting the cli
// metrics_test resetMetrics convention). Also clears the persistent baseline.
func ResetMetrics() {
	hotMu.Lock()
	defer hotMu.Unlock()
	hotLens = hotLens[:0]
	hotBase = hotStatsJSON{}
	hotPersisted = 0
}

// HotLensSnapshot — a snapshot of the kept samples (supporting test assertions — the cli
// metrics_test TestHotRecordsRuneLen convention). Returns a copy — read without a direct reference.
func HotLensSnapshot() []int {
	hotMu.Lock()
	defer hotMu.Unlock()
	return append([]int(nil), hotLens...)
}

// hotSum — the sum of the sample lengths.
func hotSum(lens []int) int {
	s := 0
	for _, n := range lens {
		s += n
	}
	return s
}

// MetaKV — assembles the meta-table getKV/setKV closure pair. Unifies the inline queries
// that were duplicated across the 4 hot/read metering wiring sites. getKV returns ("", nil) for
// a missing key; transport errors pass through as err (used by callers for fallback and
// (wire.GetMetaKVErr, wire.SetMetaKV) is delegated to — these closures keep only the
// error-distinction contract. The same pattern as knowledge's own identical assembly
// (cli goes via an alias).
func MetaKV(ctx context.Context, st wire.Store) (func(string) (string, error), func(string, string) error) {
	getKV := func(key string) (string, error) {
		v, ok, err := wire.GetMetaKVErr(ctx, st, key)
		if err != nil || !ok {
			return "", err
		}
		return v, nil
	}
	setKV := func(key, value string) error {
		return wire.SetMetaKV(ctx, st, key, value)
	}
	return getKV, setKV
}

// LoadHotStatsFromMeta — loads meta hot_stats at command start (wiring).
// A lookup failure or absence matches the LoadHotStats fallback (baseline 0, no warning).
func LoadHotStatsFromMeta(ctx context.Context, st wire.Store) {
	getKV, _ := MetaKV(ctx, st)
	LoadHotStats(getKV)
}

// PctOfFn — the single-source seam for the integer percentage (floored). The default is a
// formula equivalent to cli report.go's pctOf (preventing a standalone-ops defect); the cli init
// injects pctOf so production keeps a single source (the QuoteJSONFn convention).
var PctOfFn = func(n, total int) int {
	if total <= 0 {
		return 0
	}
	return n * 100 / total
}

// ReadZeroPct — the zero ratio (integer percent). calls 0 is 0 (the division guard).
// The rounding body delegates to pctOf (the single source — consistent flooring with the
// effect metrics) via the PctOfFn seam.
func ReadZeroPct(calls, zero int) int {
	return PctOfFn(zero, calls)
}

// LoadReadStatsFromMeta — loads the read metering for status exposure (wiring).
// The meta read_stats blob is abolished — switched to a read_events-derived computation
// (the function name is kept for cli alias compatibility). The cumulative range is the
// events_retention_days retention window. Lookup failure, missing table, or an empty window is 0/0 (it must not break status — same as the old fallback).
func LoadReadStatsFromMeta(ctx context.Context, st wire.Store) (calls int, zero int) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT count(*), count(CASE WHEN zero THEN 1 END) FROM read_events`, nil)
	if err != nil {
		return 0, 0
	}
	if len(rows) > 0 {
		ReadCounters(rows[0], &calls, &zero)
	}
	return calls, zero
}

// PersistHotStatsToMeta — stores the cumulative meta hot_stats just before command exit (wiring).
// Failure is silently ignored, at most one stderr line (non-interference §13 — metering must never change the outcome).
func PersistHotStatsToMeta(ctx context.Context, st wire.Store) {
	_, setKV := MetaKV(ctx, st)
	if err := PersistHotStats(setKV); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] hot stats persist: %v\n", err)
	}
}

// KnowledgeSummaryLens — the id -> summary length (bytes) map over all knowledge.
// The shared source of the mean write/summary bytes for token accounting (the cost savings
// lower bound and the hit injection cost) and of the hot injection byte derivation
// (DerivedHotStats). Lookup failures and unmappable cells are silently skipped (§13 — metering demotion).
func KnowledgeSummaryLens(ctx context.Context, st wire.Store) map[int]int {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT id, length(summary) FROM knowledge`, nil)
	if err != nil {
		return nil
	}
	out := map[int]int{}
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		if n, ok := render.CellToInt(r[1]); ok {
			out[id] = n
		}
	}
	return out
}

// DerivedHotStats — the derived computation of hot metering (the consumption-side switch
// from the spec §1.3 hot_stats blob). The call count is the number of distinct call groups
// of knowledge_reads kind='hot' (a single call's full set of ids remains under one ts and
// session — DISTINCT ts, session); p95 is the per-event sum distribution of hot_events
// injected ids x knowledge summary lengths (nearest-rank — the same formula as HotP95;
// the retention window is managed by PruneReadEvents). While neither kreads nor hot_events
// has data (right after an upgrade), it reports the old hot_stats blob via the legacy-read
// fallback (continuity with pre-migration values — the same migration convention as the flat-key fallback). Lookup failures and corruption are 0 (§13).
func DerivedHotStats(ctx context.Context, st wire.Store) (calls, p95 int) {
	invocations := 0
	if rows, err := wire.QueryOnce(ctx, st,
		`SELECT count(*) FROM (SELECT DISTINCT ts, session FROM knowledge_reads WHERE kind = 'hot') g`, nil); err == nil &&
		len(rows) > 0 && len(rows[0]) > 0 {
		if n, ok := render.CellToInt(rows[0][0]); ok {
			invocations = n
		}
	}
	var samples []int
	if evs, err := wire.QueryOnce(ctx, st, `SELECT ids FROM hot_events`, nil); err == nil {
		lens := KnowledgeSummaryLens(ctx, st)
		for _, r := range evs {
			if len(r) == 0 {
				continue
			}
			s, ok := r[0].(string)
			if !ok {
				continue
			}
			ids := knowledge.ParseIDsCSV(s)
			if len(ids) == 0 {
				continue
			}
			sum := 0
			for _, id := range ids {
				sum += lens[id]
			}
			samples = append(samples, sum)
		}
	}
	if invocations == 0 && len(samples) == 0 {
		return legacyHotStats(ctx, st)
	}
	sort.Ints(samples)
	return invocations, HotP95(samples)
}

// legacyHotStats — legacy-reads the old hot_stats blob (the DerivedHotStats fallback).
// The same query and computation as the old collectStats — absent or corrupt is 0.
func legacyHotStats(ctx context.Context, st wire.Store) (calls, p95 int) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('hot_stats')`, nil)
	if err != nil {
		return 0, 0
	}
	for _, r := range rows {
		if len(r) < 2 || fmt.Sprint(r[0]) != "hot_stats" {
			continue // ignore other observation keys mixed into the same query (the old collectStats contract)
		}
		var hs struct {
			Calls int   `json:"calls"`
			Lens  []int `json:"lens"`
		}
		if json.Unmarshal([]byte(fmt.Sprint(r[1])), &hs) != nil {
			continue // corrupt -> stays 0 (inheriting the old-path contract)
		}
		lens := append([]int(nil), hs.Lens...)
		sort.Ints(lens)
		return hs.Calls, HotP95(lens)
	}
	return 0, 0
}

// ReadBytesP95Of — the read surface's output-byte p95 (nearest-rank; <5 samples is 0 —
// spec §2.3). The samples are the recorded rows of the read_events bytes column (bytes>0 —
// excluding the 0 of unextended legacy rows), over the whole retention window (managed by PruneReadEvents). A lookup failure is 0.
func ReadBytesP95Of(ctx context.Context, st wire.Store) int {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT bytes FROM read_events WHERE bytes > 0`, nil)
	if err != nil {
		return 0
	}
	var samples []int
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		if n, ok := render.CellToInt(r[0]); ok && n > 0 {
			samples = append(samples, n)
		}
	}
	return p95NearestRank(samples)
}
