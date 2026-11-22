// metrics.go — hot output-length metering (in-process stats + persistent meta hot_stats accumulation).
// A one-shot CLI persists at exit and loads at start, preserving values across runs.
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// init — wires the v1.21.0 A6 scan-duration record seams. ops owns the
// accumulator, but knowledge/query cannot import ops (import cycle), so the
// seams are fn vars wired here (the mcp Register convention).
func init() {
	knowledge.RecordEchoScanFn = RecordEchoScan
	query.RecordSuggestScanFn = RecordSuggestScan
}

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
	hotLens = hotLens[:0]
	hotBase = hotStatsJSON{}
	hotPersisted = 0
	hotMu.Unlock()
	scanMu.Lock()
	scanEcho, scanSuggest = scanAcc{}, scanAcc{}
	scanLoaded = false
	scanMu.Unlock()
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
// v1.21.0 A6: the scan_stats commit rides this same existing persist point
// (spec A6 — the audit commit path; CommitTelemetry does not stage meta).
// v1.21.1: the write/read command exits flush their own samples through
// PersistScanStatsToMeta (the one-shot process otherwise loses them).
func PersistHotStatsToMeta(ctx context.Context, st wire.Store) {
	getKV, setKV := MetaKV(ctx, st)
	if err := PersistHotStats(setKV); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] hot stats persist: %v\n", err)
	}
	persistScanStatsToMeta(getKV, setKV)
}

// scanMaxSamples — the per-kind cap on kept scan-duration samples (the
// hotMaxSamples convention). When exceeded, the oldest are dropped first.
const scanMaxSamples = 500

// ScanKindStats — one scan kind's cumulative metering (v1.21.0 A6): calls
// and the duration p95 in microseconds.
type ScanKindStats struct {
	Calls int
	P95   int
}

// scanKindJSON — one kind's persistent form: cumulative calls plus the most
// recent samples kept for p95-representative purposes (the hotStatsJSON convention).
type scanKindJSON struct {
	Calls   int   `json:"calls"`
	Samples []int `json:"samples"`
}

// scanStatsJSON — the persistent meta scan_stats format: {echo,suggest}{calls,samples}.
// p95 is derived on read (ceil(0.95n)-1, HotP95) — never stored stale.
type scanStatsJSON struct {
	Echo    scanKindJSON `json:"echo"`
	Suggest scanKindJSON `json:"suggest"`
}

// scanAcc — the in-process accumulator of one kind: the previous run's
// baseline, the kept samples, and the already-persisted front offset
// (the hotBase/hotLens/hotPersisted convention).
type scanAcc struct {
	base      scanKindJSON
	samples   []int
	persisted int
}

// scanMu — protects the scan accumulators and the lazy-load flag.
var scanMu sync.Mutex

// scanEcho·scanSuggest — the per-kind accumulators (the scan_stats JSON keys).
var (
	scanEcho    scanAcc
	scanSuggest scanAcc
	scanLoaded  bool // the baseline lazy-load-once flag (loaded at the first persist point)
)

// RecordEchoScan — records one EchoScan entry-to-return duration sample.
func RecordEchoScan(d time.Duration) { recordScan(&scanEcho, d) }

// RecordSuggestScan — records one SuggestSimilarWithScanMax entry-to-return duration sample.
func RecordSuggestScan(d time.Duration) { recordScan(&scanSuggest, d) }

// recordScan — appends the sample in microseconds, trimming the front at the
// cap while shrinking the persisted offset with it (the RecordHotLen convention).
func recordScan(acc *scanAcc, d time.Duration) {
	scanMu.Lock()
	defer scanMu.Unlock()
	acc.samples = append(acc.samples, int(d.Microseconds()))
	if over := len(acc.samples) - scanMaxSamples; over > 0 {
		acc.samples = acc.samples[over:]
		if over >= acc.persisted {
			acc.persisted = 0
		} else {
			acc.persisted -= over
		}
	}
}

// ScanStats — the cumulative scan metering per kind: calls (baseline + unsaved
// samples) and the p95 over baseline+pending samples (nearest-rank, HotP95).
// Quiet zeros when a kind is empty.
func ScanStats() (echo, suggest ScanKindStats) {
	scanMu.Lock()
	defer scanMu.Unlock()
	return scanKindStatsOf(&scanEcho), scanKindStatsOf(&scanSuggest)
}

// scanKindStatsOf — the per-kind cumulative view. The caller must hold scanMu.
func scanKindStatsOf(acc *scanAcc) ScanKindStats {
	pend := scanPending(acc)
	calls := acc.base.Calls + len(pend)
	if calls == 0 {
		return ScanKindStats{}
	}
	all := append(append([]int(nil), acc.base.Samples...), pend...)
	sort.Ints(all)
	return ScanKindStats{Calls: calls, P95: HotP95(all)}
}

// scanPending — the in-memory samples not yet added to the baseline.
// The caller must hold scanMu (the hotPending convention).
func scanPending(acc *scanAcc) []int {
	if acc.persisted <= 0 {
		return acc.samples
	}
	if acc.persisted >= len(acc.samples) {
		return nil
	}
	return acc.samples[acc.persisted:]
}

// LoadScanStats — loads the meta scan_stats baseline at the persist point.
// Missing key, error, or corrupt JSON falls back to defaults (ok=false,
// baseline reset — the LoadHotStats convention). In-memory pending samples
// are untouched, so a mid-session load merges instead of dropping.
// v1.21.1 (r91/r93/r94): the loaded payload is sanitized first — negative
// calls clamp to 0 and negative samples drop (a hand-edited or corrupted
// meta row must not poison the p95).
func LoadScanStats(getKV func(string) (string, error)) bool {
	v, err := getKV("scan_stats")
	scanMu.Lock()
	defer scanMu.Unlock()
	scanEcho.base, scanSuggest.base = scanKindJSON{}, scanKindJSON{}
	if err != nil || v == "" {
		return false
	}
	var st scanStatsJSON
	if json.Unmarshal([]byte(v), &st) != nil {
		return false // corrupt -> default fallback
	}
	scanEcho.base, scanSuggest.base = sanitizeScanKind(st.Echo), sanitizeScanKind(st.Suggest)
	return true
}

// sanitizeScanKind — clamps a loaded kind to sane values: negative calls
// become 0, negative samples are dropped outright (a duration sample can
// never be negative; keeping one would skew the nearest-rank p95 floor).
func sanitizeScanKind(k scanKindJSON) scanKindJSON {
	if k.Calls < 0 {
		k.Calls = 0
	}
	if len(k.Samples) > 0 {
		kept := k.Samples[:0]
		for _, s := range k.Samples {
			if s >= 0 {
				kept = append(kept, s)
			}
		}
		k.Samples = kept
	}
	return k
}

// PersistScanStats — stores the cumulative meta scan_stats: the previous
// baseline + unsaved in-memory samples (the most recent scanMaxSamples kept
// per kind — the PersistHotStats convention). On success the summed portion
// moves to the baseline while the in-memory samples stay (double counting is
// excluded via the persisted offset).
func PersistScanStats(setKV func(string, string) error) error {
	scanMu.Lock()
	echo := scanPersistKind(&scanEcho)
	suggest := scanPersistKind(&scanSuggest)
	scanMu.Unlock()

	st := scanStatsJSON{Echo: echo.st, Suggest: suggest.st}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := setKV("scan_stats", string(b)); err != nil {
		return err
	}
	scanMu.Lock()
	if echo.snapLen > scanEcho.persisted {
		scanEcho.persisted = echo.snapLen
	}
	if suggest.snapLen > scanSuggest.persisted {
		scanSuggest.persisted = suggest.snapLen
	}
	scanEcho.base, scanSuggest.base = echo.st, suggest.st
	scanMu.Unlock()
	return nil
}

// scanSnapshot — one kind's cumulative snapshot for persist: merged calls and
// capped samples, plus the kept-sample length for the offset update.
type scanSnapshot struct {
	st      scanKindJSON
	snapLen int
}

// scanPersistKind — builds one kind's cumulative snapshot.
// The caller must hold scanMu.
func scanPersistKind(acc *scanAcc) scanSnapshot {
	cur := append([]int(nil), scanPending(acc)...)
	out := scanSnapshot{snapLen: len(acc.samples)}
	out.st = scanKindJSON{Calls: acc.base.Calls + len(cur)}
	out.st.Samples = append(append([]int(nil), acc.base.Samples...), cur...)
	if over := len(out.st.Samples) - scanMaxSamples; over > 0 {
		out.st.Samples = out.st.Samples[over:]
	}
	return out
}

// persistScanStatsToMeta — the scan_stats commit riding the existing hot_stats
// persist point (wiring). The stored baseline is lazily loaded exactly once
// here (the first commit of the process) so cross-run accumulation holds with
// zero added queries on the echo/suggest paths. Failure is silent, at most one
// stderr line (§13).
func persistScanStatsToMeta(getKV func(string) (string, error), setKV func(string, string) error) {
	scanMu.Lock()
	loaded := scanLoaded
	scanLoaded = true
	scanMu.Unlock()
	if !loaded {
		LoadScanStats(getKV)
	}
	if err := PersistScanStats(setKV); err != nil {
		fmt.Fprintf(os.Stderr, "[flint] scan stats persist: %v\n", err)
	}
}

// ScanStatsDirty — whether either kind has unsaved in-memory samples. The
// write/read exit persist is gated on this (v1.21.1 A6 completion): a
// command that produced no scan costs zero added round trips.
func ScanStatsDirty() bool {
	scanMu.Lock()
	defer scanMu.Unlock()
	return len(scanPending(&scanEcho)) > 0 || len(scanPending(&scanSuggest)) > 0
}

// scanFlushBackoff — the retry spacing of the exit flush under parallel
// writers: every writer's exit upserts the SAME meta scan_stats row, so a
// Doltgres 40001 against another writer's just-committed transaction is
// the expected loser path (the CompareSetMetaKV race-loser principle).
// Short — the process is at its exit.
var scanFlushBackoff = []time.Duration{25 * time.Millisecond, 100 * time.Millisecond, 400 * time.Millisecond}

// PersistScanStatsToMeta — the scan_stats commit at the write/read command
// exits (v1.21.1 A6 completion): a CLI one-shot process that ran an echo
// or suggestion scan would otherwise lose its samples at exit. No-op
// without pending samples (the round-trip minimum). Under parallel writes
// the exit upsert can lose the shared-row race (40001): retried briefly,
// then given up SILENTLY — no stderr line at all, unlike the hot persist
// point: the e2e parallel harness reads the command's combined output as
// ONE JSON line, so even a diagnostic line corrupts the result (§13
// non-interference — measured on the v1.21.1 CI, run 45048320543). The
// next command's flush merges over the surviving baseline, so a lost
// round only trims metering samples, never the write/read outcome.
func PersistScanStatsToMeta(ctx context.Context, st wire.Store) {
	if !ScanStatsDirty() {
		return
	}
	getKV, setKV := MetaKV(ctx, st)
	scanMu.Lock()
	loaded := scanLoaded
	scanLoaded = true
	scanMu.Unlock()
	if !loaded {
		LoadScanStats(getKV)
	}
	for i := 0; ; i++ {
		if err := PersistScanStats(setKV); err == nil {
			return
		} else if errors.Is(err, store.ErrServerDown) || i >= len(scanFlushBackoff) {
			return // quiet §13 — the next command's flush recovers
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(scanFlushBackoff[i]):
		}
	}
}

// ScanStatsFromMetaFn — the status-display seam over the meta scan_stats
// baseline (the HotStatsFn convention; tests swap the loader).
var ScanStatsFromMetaFn = ScanStatsFromMeta

// ScanStatsFromMeta — loads the meta scan_stats baseline and returns the
// cumulative per-kind view for the status scan row (v1.21.1 A6). A fresh
// load every call — status is its own process, so the in-memory samples
// are empty and the previous run's cumulative is the truth (the
// LoadHotStatsFromMeta principle). Quiet zeros on any failure.
func ScanStatsFromMeta(getKV func(string) (string, error)) (echo, suggest ScanKindStats) {
	LoadScanStats(getKV)
	return ScanStats()
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
