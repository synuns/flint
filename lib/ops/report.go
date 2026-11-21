// report.go — `flint report`: reports the N-day (default 7, cap 14) accrual and maintenance
// report as one-line JSON. A maintenance path (offline CLI), non-interfering with the hot path (§2).
// All data is read via wire.Store.Query — meta daily:* and hot_stats, and the
// knowledge tallies (counts, stale, period invalidations). Missing keys are 0; corrupt JSON is skipped.
// The read_stats blob is abolished — daily read, zero, and near merge read_events-derived
// values into the residual daily:* (mergeDerivedReadDaily), and fallback_pct is computed from the
// read_events window tally (CollectEffect). Metric values are equivalent to before the migration (equivalence tests).
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// reportDaysDefault — --days default 7. The cap is the retention window (thresholds
// daily_retention_days).
const reportDaysDefault = 7

// ReportDay — one day's intake row. Source (hook/manual/mcp) and dup are incremented separately from write.
// The 5 dup_* fields are per-source metering (unrecorded days 0 — a missing daily JSON key
// decodes as the zero value).
type ReportDay struct {
	Date          string `json:"date"`
	Write         int    `json:"write"`
	Hook          int    `json:"hook"`
	Manual        int    `json:"manual"`
	Mcp           int    `json:"mcp"`
	Dup           int    `json:"dup"`
	DupHook       int    `json:"dup_hook"`
	DupManual     int    `json:"dup_manual"`
	DupMcp        int    `json:"dup_mcp"`
	DupCorrection int    `json:"dup_correction"`
	DupGap        int    `json:"dup_gap"`
	Hot           int    `json:"hot"`
	Read          int    `json:"read"`
	// daily zero and near counts (unrecorded days decode
	// as the zero value; old daily JSON compatible).
	ReadZero int `json:"read_zero"`
	ReadNear int `json:"read_zero_near"`
	// G5 byte metering — the brief call count (counter decoding) and
	// the three surfaces' output bytes (unrecorded days decode as 0; old daily JSON compatible).
	Brief      int `json:"brief"`
	ReadBytes  int `json:"read_bytes"`
	HotBytes   int `json:"hot_bytes"`
	BriefBytes int `json:"brief_bytes"`
}

// ReportOutput — the one-line JSON output. Dates outside the window, missing keys, and corrupt keys are filled as 0 rows.
type ReportOutput struct {
	Days    []ReportDay `json:"days"`
	ZeroPct int         `json:"zero_pct"`
	HotP95  int         `json:"hot_p95"`
	Counts  struct {
		Pending     int `json:"pending"`
		Active      int `json:"active"`
		Invalidated int `json:"invalidated"`
	} `json:"counts"`
	Stale struct {
		Days90    int `json:"days90"`
		NeverRead int `json:"never_read"`
	} `json:"stale"`
	InvalidatedPeriod int `json:"invalidated_period"`
	// observability metrics — appended only at the end to keep the existing field-and-order
	// contract (the status reflection-test convention).
	DupPct      int `json:"dup_pct"`
	FallbackPct int `json:"fallback_pct"`
	// per-source dup period sums — existing fields and order unchanged, appended at the end.
	DupHook   int `json:"dup_hook"`
	DupManual int `json:"dup_manual"`
	// the zero_near ratio — existing fields and order unchanged, appended at the end.
	ZeroNearPct int `json:"zero_near_pct"`
	// the effect-metering section (the single computation source of the docs/METRICS.md §4 definitions).
	// Existing fields and order unchanged, appended at the end.
	Effect EffectOutput `json:"effect"`
	// curation result counters summed over the window (the spec §2.4 curation object).
	// Recorded as flat daily:<kind>:<date> keys; report exposes the window sum.
	Curation CurationCounters `json:"curation"`
	// rule diagnostics D (spec §3). A single data model: text (Sentence),
	// --json (this array), and --html (cards) — all three views render from this field
	// (no data split across views — user principle). Normal silence: the empty array.
	Diagnosis []DiagnosisItem `json:"diagnosis"`
}

// CurationCounters — the --days window sums of the curation (approve), maintenance (maintain),
// and accrual (import) result counters (spec §2.4). All counts and row counts — no personal data collected.
// Unrecorded kinds are 0 (the missing-flat-key convention).
type CurationCounters struct {
	ApproveDone         int `json:"approve_done"`
	ApproveNotFound     int `json:"approve_not_found"`
	ApproveSupersedeErr int `json:"approve_supersede_err"`
	MaintainSupersede   int `json:"maintain_supersede"`
	MaintainExpire      int `json:"maintain_expire"`
	MaintainPruneRows   int `json:"maintain_prune_rows"`
	ImportAdded         int `json:"import_added"`
	ImportSkipped       int `json:"import_skipped"`
}

// EffectOutput — the "helpfulness" metrics (the computation of the METRICS §2 GSM table). hit_pct, near_pct,
// dup_prevented·read_events_n·read_write_pairs·pair_pct·fallback_pct·
// recall_led_pct, and chain_pct use the --days window; reuse_pct and dead_pct are point-in-time metrics
// (not period). All are correlational proxies (Tier 1 and 2) — read them as a set, not as standalone
// proof (METRICS §5).
type EffectOutput struct {
	HitPct         int `json:"hit_pct"`          // G1: 1−zero/read
	NearPct        int `json:"near_pct"`         // G1: near-recall headroom (Σnear/read)
	FallbackPct    int `json:"fallback_pct"`     // G1: in-window fallback rate (separate from the cumulative key)
	DupPrevented   int `json:"dup_prevented"`    // G2: Σdup (absolute volume)
	ReusePct       int `json:"reuse_pct"`        // G3: share of recalled active (search-based)
	DeadPct        int `json:"dead_pct"`         // G3: 0 recalls and 30d+ (excluding hot exposure)
	ReadEventsN    int `json:"read_events_n"`    // G4 denominator: in-window read events
	ReadWritePairs int `json:"read_write_pairs"` // G4: a write within 30m after a read in the same session
	PairPct        int `json:"pair_pct"`         // G4: pairs/read_events_n
	// session boundary (D) and usage chaining (B).
	RecallLedPct int `json:"recall_led_pct"` // G4: share of writing sessions where recall came first
	ChainPct     int `json:"chain_pct"`      // G4: returned->used chain rate (24h)
	// profile observation hint (the D1 reduced form) — below the sample floor it is
	// "deferred" (the observation-first principle — no guessing, per METRICS §5: reporting only).
	ProfileHint string `json:"profile_hint"`
	// hit-quality approximation (first_rank-based, fallback excluded).
	ExactPct int     `json:"exact_pct"` // first topic-exact return rate (exact-match recalls)
	Top1Pct  int     `json:"top1_pct"`  // exact-rank-1 rate (among reads with an exact match)
	MRR      float64 `json:"mrr"`       // mean reciprocal rank (among reads with an exact match, 0..1)
	// 7-day window before/after trend (cross-section -> trend, a single read_events table).
	Trend *TrendOutput `json:"trend,omitempty"`
	// G5 minimal injection — the three surfaces' output bytes (summed daily counters).
	Bytes EffectBytes `json:"bytes"`
	// zero-hit decomposition (spec §2.2), recall fate (§2.6), hot->read chaining
	// (§2.1), and token accounting cost (§2.3). Additive, appended at the end.
	ZeroUnownedPct   int        `json:"zero_unowned_pct"`    // unowned share of zeros (denominator = in-window reads)
	ZeroOwnedMissPct int        `json:"zero_owned_miss_pct"` // owned_miss share of zeros (same denominator)
	SurvivedPct      int        `json:"survived_pct"`        // share of 7d+ recalled knowledge still active
	BadRecallPct     int        `json:"bad_recall_pct"`      // share of 7d+ recalled knowledge superseded/invalidated
	HotReadChainPct  int        `json:"hot_read_chain_pct"`  // share of hot exposures followed within 30m by a read of the same knowledge
	Cost             EffectCost `json:"cost"`
}

// the cost label wording contract (spec §2.3 — following the research conclusion). The wording pins savings
// as a 'lower bound' estimate of a deterministic counterfactual, and hit cost is labeled
// as an 'injection cost', not a savings claim. No token or dollar conversion (METRICS §5).
const (
	CostSavingsLabel = "savings lower-bound estimate"
	CostHitLabel     = "injection cost"

	costModeAlwaysOn = "always-on" // hot — unconditionally injected at every session start
	costModeOnInvoke = "on-invoke" // read and brief — output only on invocation
)

// EffectCost — G5 token accounting (spec §2.3). Per-surface tallies (bytes_14d, avg, p95, mode) and
// two global computations: savings_lower_bound_bytes = Σdup x mean write bytes (the dedup
// logical-physical formula — a deterministic lower bound on bytes not stored thanks to
// duplicate prevention), and hit_injection_cost_bytes = Σhit x mean summary bytes (the injection
// cost of recall returns — not a savings claim). p95 is nearest-rank; surfaces with fewer than 5 samples or no
// per-event source (brief — only daily sums exist) are 0.
type EffectCost struct {
	Read                   CostSurface `json:"read"`
	Hot                    CostSurface `json:"hot"`
	Brief                  CostSurface `json:"brief"`
	SavingsLowerBoundBytes int         `json:"savings_lower_bound_bytes"`
	SavingsLabel           string      `json:"savings_label"`
	HitInjectionCostBytes  int         `json:"hit_injection_cost_bytes"`
	HitCostLabel           string      `json:"hit_cost_label"`
}

// CostSurface — one surface's window bytes, mean, p95, and injection-mode notation.
type CostSurface struct {
	Mode     string `json:"mode"`
	Bytes14d int    `json:"bytes_14d"`
	AvgBytes int    `json:"avg_bytes"`
	P95Bytes int    `json:"p95_bytes"`
}

// EffectBytes — G5 minimal injection: the measured output bytes of the three surfaces read, hot
// (injection), and brief. The bytes_14d name inherits the *_14d convention (the actual window is
// report --days — the same false-name defense principle as status §4.7, respecting the retention cap). The
// avg_bytes denominator is the call counter on the same daily row (read, hot, brief),
// floor-truncated integer; a zero denominator is 0. No token conversion factor estimation (METRICS §5 — bytes as-is).
type EffectBytes struct {
	Read  SurfaceBytes `json:"read"`
	Hot   SurfaceBytes `json:"hot"`
	Brief SurfaceBytes `json:"brief"`
}

// SurfaceBytes — one surface's window byte sum and per-call average.
type SurfaceBytes struct {
	Bytes14d int `json:"bytes_14d"`
	AvgBytes int `json:"avg_bytes"`
}

// avgBytes — mean bytes per call (floored). calls <= 0 is 0 (the zero-denominator convention).
func avgBytes(bytes, calls int) int {
	if calls <= 0 {
		return 0
	}
	return bytes / calls
}

// TrendOutput — the last 7 days (cur) vs the prior 7 days (prev). delta = cur - prev.
type TrendOutput struct {
	HitPct      TrendPoint `json:"hit_pct"`
	FallbackPct TrendPoint `json:"fallback_pct"`
	Top1Pct     TrendPoint `json:"top1_pct"`
}

// TrendPoint — previous period, current period, and delta (integer pct).
type TrendPoint struct {
	Prev  int `json:"prev"`
	Cur   int `json:"cur"`
	Delta int `json:"delta"`
}

// the effect-metering decision window has moved to the thresholds
// registry (meta overrides) — the single source of defaults remains there.
// store.DefaultThresholds(dead_days 30·pair_window_minutes 30·
// chain_window_hours 24, TUNING §2).

// reportSessionLookback — the session-assignment lookup margin: includes sessions started just
// before the window start, so the first in-window event never misses its assignment. Events belonging
// to longer sessions are unassigned — the fallback pairing (conservative).
const reportSessionLookback = 24 * time.Hour

// (the cli alias DailyRow=ReportDay is deprecated — ops already has
// status.go's DailyRow (the type passed through the WindowedDailyFn seam) as the prior
// definition. Residual cli consumers are bridged by cli/ops_alias_3.go's ReportDay alias.)

// DupPct — period duplication rate: Σdup/(Σwrite+Σdup) over the whole-period sums,
// floored integer %. Zero denominator or an empty period is 0.
func DupPct(days []ReportDay) int {
	var write, dup int
	for _, d := range days {
		write += d.Write
		dup += d.Dup
	}
	if write+dup == 0 {
		return 0
	}
	return int(100.0 * float64(dup) / float64(write+dup))
}

// FallbackPct — fallback ratio: read_fallback_calls/read_calls, floored integer %.
// Zero denominator is 0.
func FallbackPct(fallbackCalls int, readCalls int) int {
	if readCalls == 0 {
		return 0
	}
	return int(100.0 * float64(fallbackCalls) / float64(readCalls))
}

// RunReport — parses --days then queries the tallies and outputs. Success 0; argument error 1;
// storage errors go through MapExit (server-down 2 etc.).
func RunReport(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// positional args not allowed (the wire.ParseArgs spec table).
	// value validation reproduces occurrence order (AllOccs) — the existing encounter-order
	// error precedence stays as-is.
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--html"}, // static single-file dashboard
			{Name: "--text"}, // human-readable text view (the default one-line JSON unchanged)
			{Name: "--out", Value: true},
			{Name: "--days", Value: true},
		},
		Usage: "usage: flint report [--days N] [--html] [--out FILE] [--text]",
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	days := reportDaysDefault
	htmlOut := ""
	textOut := res.Given("--text")
	for _, o := range res.AllOccs() {
		switch o.Name {
		case "--html":
			htmlOut = "report.html"
		case "--out":
			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("report: --out requires a path — usage: flint report [--days N] [--html] [--out FILE] [--text]")))
				return 3
			}
			// a typo guard against a value absorbing the flag (the same contract
			// as backup --out). A --days value failing Atoi already exits 3.
			if err := knowledge.ValidateFlagValue("out", o.Value); err != nil {
				fmt.Fprintln(stdout, wire.ErrLine(err))
				return 3
			}
			htmlOut = o.Value
			if !strings.HasSuffix(htmlOut, ".html") {
				htmlOut += ".html"
			}
		case "--days":
			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("report: --days requires a number — usage: flint report [--days N] [--html] [--out FILE] [--text]")))
				return 3
			}
			n, err := strconv.Atoi(o.Value)
			if err != nil || n < 1 {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("report: invalid days %q — usage: flint report [--days N] [--html] [--out FILE] [--text]", o.Value)))
				return 3
			}
			days = n
		}
	}
	// F6 (performance): thresholds are loaded once per command scope — the --days cap, the stale
	// cutoff (collectCounts), and the effect decision window (collectEffect) each issuing
	// their own meta IN query (up to 3 per run) now share 1 load. The fallback keeps the
	// WriteThresholds convention (lookup failure -> DefaultThresholds) unchanged. The standalone HTML
	// path also loads — since the clamp below applies to the default --days too, skipping HTML
	// would leave an empty-day tile asymmetry only in HTML (one load for the cap decision,
	// separate from the collection chain's own load).
	th := knowledge.WriteThresholds(ctx, st)
	// the cap clamp applies to the default (7) too — resolving the asymmetry
	// where, with an override daily_retention_days < 7, the default 7 used to create only empty rows
	// outside the retention window (the same rule as an explicit --days). Applied to JSON and HTML alike.
	if max := th.DailyRetentionD; max > 0 && days > max {
		days = max // the cap = the retention window (no data exists beyond it)
	}

	if htmlOut != "" { // HTML path — reuses the same collection chain
		return runReportHTML(ctx, st, days, htmlOut, stdout)
	}
	out := ReportOutput{Days: make([]ReportDay, 0, days)}
	if err := collectDaily(ctx, st, days, &out); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// v1.9.3 T2 — parallelized collection. The independence findings (mutually exclusive write fields):
	//    collectCuration writes only out.Curation.
	//    collectStats writes only out.HotP95 (by the DerivedHotStats demotion contract it
	//                                     returns no error — the slot is for priority preservation).
	//    collectCounts writes only out.Counts, out.Stale, and out.InvalidatedPeriod.
	//    collectEffect writes only the out.Effect subfields and out.FallbackPct,
	//                                     and only reads the collectDaily products (the Days-derived
	//                                     HitPct, NearPct, DupPrevented, Effect.Bytes).
	// Since the four's write fields do not overlap, concurrent writes to the same out land only on
	// distinct memory locations (the Go memory model — field-level independence). The shared sources
	// (daily:*, hot_events, knowledge, etc.) are all SELECTs, so there is no read contention either.
	// collectDaily (the Days source — the input of downstream computations) stays sequential as above;
	// out.DupPct and the sum post-processing run sequentially after everything completes (the existing order).
	//
	// error selection is deterministic: after everything completes, following the existing sequential priority
	// stats > counts > effect (daily was already returned above; curation has no error
	// by the demotion contract), the first error is chosen — even with multiple failures,
	// the result matches the sequential path's ErrLine and exit (decide after a full join — the delay is
	// negligible; keeping the machine channel unchanged takes priority).
	//
	// wall-clock measurement (isolated scratch DSN, synthetic seeded knowledge 500,
	// read_events 2000, knowledge_reads 800, median of 3):
	// sequential 121ms -> parallel 83ms (local Doltgres synthetic demo — environment-dependent).
	var g errgroup.Group
	var statErr, countErr, effectErr error
	g.Go(func() error { collectCuration(ctx, st, days, &out); return nil })
	g.Go(func() error { statErr = collectStats(ctx, st, &out); return nil })
	g.Go(func() error { countErr = collectCounts(ctx, st, days, th, &out); return nil })
	g.Go(func() error { effectErr = collectEffect(ctx, st, days, th, &out); return nil })
	g.Wait()
	for _, err := range []error{statErr, countErr, effectErr} {
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
	}
	// observability metrics — dup_pct from the daily rows; fallback_pct from CollectEffect's
	// read_events window tally (the same computation as effect.fallback_pct).
	out.DupPct = DupPct(out.Days)
	// per-source dup period sums — summed from the daily rows (unrecorded days sum as 0).
	for _, d := range out.Days {
		out.DupHook += d.DupHook
		out.DupManual += d.DupManual
	}
	// rule diagnostics D (spec §3): pure-function verdicts from the collected sources
	// (effect, counts, daily). hot_budget (the tier-A self-reference criterion) is the meta
	// loaded value (default 2000 when unset — the HotBudget fallback).
	out.Diagnosis = Diagnose(out, th, knowledge.HotBudget(knowledge.MetaGetKV(ctx, st)))
	if textOut { // text render — consumes the JSON collection output as-is (--html already returned above — higher precedence)
		printReportText(stdout, out, days)
		return 0
	}
	b, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

// printReportText — `flint report --text` — the human-readable
// text view. Renders the same collection output (ReportOutput) as the default one-line JSON and the
// --html cards (all three views share one source — the text leg of the Diagnosis field comment contract.
// text creates no metrics of its own). Diagnostics are output only as Diagnose's Sentence()
// lines — 0 findings means the section is not printed (inheriting the normal-silence contract). The summary keeps
// only the key metrics (staying concise — no elaborate format design).
func printReportText(w io.Writer, out ReportOutput, days int) {
	var write, read, dup int
	for _, d := range out.Days {
		write += d.Write
		read += d.Read
		dup += d.Dup
	}
	fmt.Fprintf(w, "flint report · %dd window (%s)\n", days, timeNowUTC())
	fmt.Fprintf(w, "writes %d · reads %d · dup prevented %d (dup %d%%) · zero hits %d%% · fallback %d%%\n",
		write, read, dup, out.DupPct, out.ZeroPct, out.FallbackPct)
	fmt.Fprintf(w, "knowledge: active %d · pending %d · invalidated %d · never-read %d · hot p95 %d\n",
		out.Counts.Active, out.Counts.Pending, out.Counts.Invalidated, out.Stale.Days90, out.HotP95)
	if len(out.Diagnosis) == 0 {
		return // normal silence — no diagnostics section
	}
	fmt.Fprintln(w, "diagnosis")
	for _, d := range out.Diagnosis {
		fmt.Fprintf(w, "- %s\n", d.Sentence())
	}
}

// collectDaily — merges the meta daily:* keys into the N-day window (today included, UTC).
// Out-of-window keys are excluded (8 seeded days still yield 7 rows); corrupt JSON falls back to a 0 row (skip).
// The window-merge body is extracted as the shared WindowedDaily reader — unifying it with status's
// windowed metrics (R4) and parser (removing the divergence risk on format changes).
func collectDaily(ctx context.Context, st wire.Store, days int, out *ReportOutput) error {
	dayRows, err := WindowedDaily(ctx, st, days)
	if err != nil {
		return err
	}
	out.Days = dayRows
	// zero_pct and zero_near_pct are computed from the --days window's
	// daily sums (removing the cumulative read_stats dependency). The days array is window-marked
	// first, so out-of-window rows are excluded from the sums too (floored integer %; zero denominator 0).
	var sumRead, sumZero, sumNear, sumDup int
	// G5 — the three surfaces' byte sums and avg denominators (each kind's call counter).
	var sumHot, sumBrief, sumReadB, sumHotB, sumBriefB int
	for _, d := range out.Days {
		sumRead += d.Read
		sumZero += d.ReadZero
		sumNear += d.ReadNear
		sumDup += d.Dup
		sumHot += d.Hot
		sumBrief += d.Brief
		sumReadB += d.ReadBytes
		sumHotB += d.HotBytes
		sumBriefB += d.BriefBytes
	}
	out.ZeroPct = ReadZeroPct(sumRead, sumZero)
	out.ZeroNearPct = ReadZeroPct(sumRead, sumNear)
	// the effect views G1 and G2 — derived from the same sums (a single derivation chain —
	// CollectEffect does not re-sum them).
	out.Effect.HitPct = PctOf(sumRead-sumZero, sumRead)
	out.Effect.NearPct = PctOf(sumNear, sumRead)
	out.Effect.DupPrevented = sumDup
	// G5 minimal injection — derived from the same sums (the same retention window
	// and out-of-window exclusion as the count metrics; denominator 0 -> avg 0).
	out.Effect.Bytes = EffectBytes{
		Read:  SurfaceBytes{Bytes14d: sumReadB, AvgBytes: avgBytes(sumReadB, sumRead)},
		Hot:   SurfaceBytes{Bytes14d: sumHotB, AvgBytes: avgBytes(sumHotB, sumHot)},
		Brief: SurfaceBytes{Bytes14d: sumBriefB, AvgBytes: avgBytes(sumBriefB, sumBrief)},
	}
	return nil
}

// WindowedDaily — merges the meta daily:* keys into N-day window rows (today included, UTC)
// and returns them (oldest first — today is the last row). Out-of-window keys excluded; corrupt JSON
// falls back to a 0 row. The single source for both report (collectDaily) and status
// (statusWindowedRead) (dissolving the 3-way daily-parser duplication).
func WindowedDaily(ctx context.Context, st wire.Store, days int) ([]ReportDay, error) {
	rows, err := wire.QueryOnce(ctx, st, `SELECT key, value FROM meta WHERE key LIKE 'daily:%'`, nil)
	if err != nil {
		return nil, err
	}
	daysOut := make([]ReportDay, 0, days)
	window := make(map[string]int, days)
	now := time.Now().UTC()
	for i := days - 1; i >= 0; i-- { // oldest first — today is the last row
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		window[d] = len(daysOut)
		daysOut = append(daysOut, ReportDay{Date: d})
	}
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		var key string
		if _, err := fmt.Sscanf(fmt.Sprint(r[0]), "daily:%s", &key); err != nil {
			continue
		}
		idx, ok := window[key]
		if !ok {
			continue // outside the window (retained excess, etc.)
		}
		var d ReportDay
		if json.Unmarshal([]byte(fmt.Sprint(r[1])), &d) != nil {
			continue // corrupt -> stays 0
		}
		d.Date = daysOut[idx].Date
		daysOut[idx] = d
	}
	// flat-key reads (daily:<kind>:<date> integer values). Flat takes precedence per field:
	// if a flat key exists, it overrides the old JSON value
	// (consistent with the readDailyFlat contract — both formats coexist during migration).
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		key := fmt.Sprint(r[0])
		if !strings.HasPrefix(key, "daily:") {
			continue
		}
		rest := key[len("daily:"):]
		i := strings.Index(rest, ":")
		if i < 0 {
			continue // the old daily:<date> JSON form — handled in the pass above
		}
		kind, date := rest[:i], rest[i+1:]
		idx, ok := window[date]
		if !ok {
			continue // outside the window
		}
		setter, ok := dailyFlatFields[kind]
		if !ok {
			continue // non-daily row metrics like the curation counters (curationKinds)
		}
		n, perr := strconv.Atoi(strings.TrimSpace(fmt.Sprint(r[1])))
		if perr != nil {
			continue // corrupt -> keep the existing value (JSON/0)
		}
		setter(&daysOut[idx], n)
	}
	mergeDerivedReadDaily(ctx, st, daysOut)
	return daysOut, nil
}

// dailyFlatFields — the kind -> ReportDay field mapping for the daily:<kind>:<date> flat keys
// (every kind that RecordUsageCount and RecordBytes record).
var dailyFlatFields = map[string]func(*ReportDay, int){
	"write":          func(d *ReportDay, n int) { d.Write = n },
	"hook":           func(d *ReportDay, n int) { d.Hook = n },
	"manual":         func(d *ReportDay, n int) { d.Manual = n },
	"mcp":            func(d *ReportDay, n int) { d.Mcp = n },
	"dup":            func(d *ReportDay, n int) { d.Dup = n },
	"dup_hook":       func(d *ReportDay, n int) { d.DupHook = n },
	"dup_manual":     func(d *ReportDay, n int) { d.DupManual = n },
	"dup_mcp":        func(d *ReportDay, n int) { d.DupMcp = n },
	"dup_correction": func(d *ReportDay, n int) { d.DupCorrection = n },
	"dup_gap":        func(d *ReportDay, n int) { d.DupGap = n },
	"hot":            func(d *ReportDay, n int) { d.Hot = n },
	"read":           func(d *ReportDay, n int) { d.Read = n },
	"brief":          func(d *ReportDay, n int) { d.Brief = n },
	"read_zero":      func(d *ReportDay, n int) { d.ReadZero = n },
	"read_zero_near": func(d *ReportDay, n int) { d.ReadNear = n },
	"read_bytes":     func(d *ReportDay, n int) { d.ReadBytes = n },
	"hot_bytes":      func(d *ReportDay, n int) { d.HotBytes = n },
	"brief_bytes":    func(d *ReportDay, n int) { d.BriefBytes = n },
}

// mergeDerivedReadDaily — the read path no longer writes the daily read counters
// (writes 4->2). Daily read, read_zero, and read_zero_near are derived by grouping read_events
// by UTC date and merged field-by-field as a max with the residual daily:* values —
// on the migration day (when both sources point at the same events) the values are equal,
// so the metrics stay continuous without double counting. A lookup failure keeps the residual daily
// values (the metering-demotion principle — it must not break the report).
func mergeDerivedReadDaily(ctx context.Context, st wire.Store, days []ReportDay) {
	if len(days) == 0 {
		return
	}
	// UTC midnight of the window's first day — a cutoff covering the entire WindowedDaily day row
	// (a Go-computed timestamptz parameter — the convention; no SQL interval arithmetic).
	cutoff, err := time.Parse("2006-01-02", days[0].Date)
	if err != nil {
		return
	}
	rows, qerr := wire.QueryOnce(ctx, st,
		`SELECT ts, zero, near FROM read_events WHERE ts >= $1`, []any{cutoff})
	if qerr != nil {
		return
	}
	type readCnt struct{ read, zero, near int }
	byDay := map[string]*readCnt{}
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		ts, ok := CellToTime(r[0])
		if !ok {
			continue // unmappable cells are silently skipped (the metering convention)
		}
		key := ts.UTC().Format("2006-01-02")
		c := byDay[key]
		if c == nil {
			c = &readCnt{}
			byDay[key] = c
		}
		c.read++
		if cellToBool(r[1]) {
			c.zero++
		}
		if cellToBool(r[2]) {
			c.near++
		}
	}
	for i := range days {
		c := byDay[days[i].Date]
		if c == nil {
			continue
		}
		days[i].Read = max(days[i].Read, c.read)
		days[i].ReadZero = max(days[i].ReadZero, c.zero)
		days[i].ReadNear = max(days[i].ReadNear, c.near)
	}
}

// collectStats — hot p95. Consumes the hot_stats blob (spec §1.3):
// p95 derives from the per-event sum distribution of hot_events injected ids x knowledge summary
// lengths (DerivedHotStats — metrics.go). While neither kreads nor hot_events has data
// (right after an upgrade), it reports the old blob via the legacy-read fallback (the same value
// as before the migration). read_stats (the fallback_pct denominator calls) is abolished:
// fallback_pct comes from CollectEffect's read_events window tally. Absent or corrupt is 0.
func collectStats(ctx context.Context, st wire.Store, out *ReportOutput) error {
	_, p95 := DerivedHotStats(ctx, st)
	out.HotP95 = p95
	return nil
}

// curationKinds — the curation result counter kinds (spec §2.4).
// Recorded as flat daily:<kind>:<date> keys.
var curationKinds = []string{
	"approve_done", "approve_not_found", "approve_supersede_err",
	"maintain_supersede", "maintain_expire", "maintain_prune_rows",
	"import_added", "import_skipped",
}

// collectCuration — the --days window sum of the curation result counters (the spec §2.4 curation
// object). Reads only the flat daily keys (a new metering — the old JSON has no such kinds).
// Lookup failures and corrupt keys are silently 0 (the §13 metering principle — it must not break the report).
func collectCuration(ctx context.Context, st wire.Store, days int, out *ReportOutput) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key LIKE 'daily:%'`, nil)
	if err != nil {
		return
	}
	window := map[string]bool{}
	now := time.Now().UTC()
	for i := days - 1; i >= 0; i-- {
		window[now.AddDate(0, 0, -i).Format("2006-01-02")] = true
	}
	known := map[string]bool{}
	for _, k := range curationKinds {
		known[k] = true
	}
	sums := map[string]int{}
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		key := fmt.Sprint(r[0])
		if !strings.HasPrefix(key, "daily:") {
			continue
		}
		rest := key[len("daily:"):]
		i := strings.Index(rest, ":")
		if i < 0 {
			continue
		}
		kind, date := rest[:i], rest[i+1:]
		if !known[kind] || !window[date] {
			continue
		}
		n, perr := strconv.Atoi(strings.TrimSpace(fmt.Sprint(r[1])))
		if perr != nil {
			continue
		}
		sums[kind] += n
	}
	out.Curation = CurationCounters{
		ApproveDone:         sums["approve_done"],
		ApproveNotFound:     sums["approve_not_found"],
		ApproveSupersedeErr: sums["approve_supersede_err"],
		MaintainSupersede:   sums["maintain_supersede"],
		MaintainExpire:      sums["maintain_expire"],
		MaintainPruneRows:   sums["maintain_prune_rows"],
		ImportAdded:         sums["import_added"],
		ImportSkipped:       sums["import_skipped"],
	}
}

// CollectCounts — the 3 knowledge tallies. Pending and active (no invalid_at), and
// invalidated (total); stale (days90 and never_read separated, §2 ruling); and the count of invalidations
// within the period (N days). The invalidation state is determined by the presence of invalid_at, not status
// (reversible invalidation). Doltgres compatibility: CASE aggregation instead of FILTER (the pattern.go convention).
// The cutoff is computed in Go and passed as a timestamptz parameter — an SQL ($1 * interval)
// product makes Doltgres infer $1 as interval, failing pgx encoding, and even passing with
// an ::int cast the evaluation breaks (operator does not exist: timestamptz < interval, observed
// in e2e). The same defect and the same remedy as maintain --stale. Note: the [90] and [days]
// int-argument assertions in report_test.go's TestReportSQLDoltgresCompat verify this very parameter-multiplication
// convention — a ruling target (not modified here since the file is not owned).
//
// F6 (performance): CollectCounts is the public wrapper (direct callers — e.g. the report_html.go
// composition — load their own with the existing signature). The RunReport chain uses
// collectCounts, injected with the once-per-command-scope loaded value (th).
func CollectCounts(ctx context.Context, st wire.Store, days int, out *ReportOutput) error {
	return collectCounts(ctx, st, days, knowledge.WriteThresholds(ctx, st), out)
}

func collectCounts(ctx context.Context, st wire.Store, days int, th store.Thresholds, out *ReportOutput) error {
	rows, err := wire.QueryOnce(ctx, st, `
		SELECT count(CASE WHEN status = 'pending' AND invalid_at IS NULL THEN 1 END),
		       count(CASE WHEN status = 'active' AND invalid_at IS NULL THEN 1 END),
		       count(CASE WHEN invalid_at IS NOT NULL THEN 1 END)
		  FROM knowledge`, nil)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		ReadCounters(rows[0], &out.Counts.Pending, &out.Counts.Active, &out.Counts.Invalidated)
	}
	// DaysCutoff clamps the days (the v1.19.2 overflow sweep) — a stale_days
	// override past the Duration ceiling used to wrap the cutoff into the
	// future, counting every active row stale.
	staleCutoff := db.DaysCutoff(th.StaleDays, time.Now())
	rows, err = wire.QueryOnce(ctx, st, `
		/* stale */ SELECT count(CASE WHEN (last_read_at IS NOT NULL AND last_read_at < $1)
		                         OR (read_count = 0 AND ingested_at < $1) THEN 1 END),
		       count(CASE WHEN read_count = 0 AND ingested_at < $1 THEN 1 END)
		  FROM knowledge
		 WHERE status = 'active' AND invalid_at IS NULL`,
		[]any{staleCutoff})
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		ReadCounters(rows[0], &out.Stale.Days90, &out.Stale.NeverRead)
	}
	periodCutoff := db.DaysCutoff(days, time.Now())
	rows, err = wire.QueryOnce(ctx, st, `
		SELECT count(*) FROM knowledge WHERE invalid_at >= $1`,
		[]any{periodCutoff})
	if err != nil {
		return err
	}
	if len(rows) > 0 && len(rows[0]) > 0 {
		ReadCounters(rows[0], &out.InvalidatedPeriod)
	}
	return nil
}

// ReadCounters — reads the numeric columns of tally rows (int32/int64/string, etc.) as int.
// Conversion shares the single render.CellToInt utility (read.go — preventing a 3rd implementation).
// Unmappable cells (nil, non-numeric) keep the existing value (0).
func ReadCounters(row []any, dst ...*int) {
	for i, p := range dst {
		if i >= len(row) {
			return
		}
		if n, ok := render.CellToInt(row[i]); ok {
			*p = n
		}
	}
}

// PctOf — n/total as an integer percentage (floored). total 0 is 0 (the ReadZeroPct guard convention).
func PctOf(n, total int) int {
	if total <= 0 {
		return 0
	}
	return n * 100 / total
}

// CollectEffect — computes the effect-metering section (the docs/METRICS.md §4 definitions).
// G1 (hit, near) and G2 (dup_prevented) are derived from the collectDaily sums
// (not re-summed here); G3 (reuse, dead) from the knowledge point-in-time tally + the hot exposure
// correction (E); G4 from the session-boundary assignment pairing (D) and usage chaining (B) —
// all computed with Go-side joins, avoiding the Doltgres parameter-inference risk of SQL interval
// products.
//
// G3 is not merged into CollectCounts's stale query —
// stale's 'one argument (timestamptz)' contract is verified by regression tests, and
// the dead cutoff (30 days) takes a different argument, breaking that contract. At the cost of
// one small extra query on the maintenance path, the two concerns stay separated.
//
// Metering principle (§13, METRICS §4): a lookup failure on an event log (read_events,
// session_starts, hot_events) only demotes the affected metric to 0/fallback, never breaking the whole
// report. A knowledge lookup failure propagates (the other report queries would fail
// too).
//
// F6 (performance): CollectEffect is the public wrapper (direct callers — e.g. the report_html.go
// composition — load their own with the existing signature). The RunReport chain uses
// collectEffect, injected with the once-per-command-scope loaded value (th).
func CollectEffect(ctx context.Context, st wire.Store, days int, out *ReportOutput) error {
	return collectEffect(ctx, st, days, knowledge.WriteThresholds(ctx, st), out)
}

func collectEffect(ctx context.Context, st wire.Store, days int, th store.Thresholds, out *ReportOutput) error {
	now := time.Now()
	// windowStart shares the DaysCutoff clamp (the v1.19.2 overflow sweep) — a
	// huge --days value clamps to the ceiling span instead of wrapping negative.
	windowStart := db.DaysCutoff(days, now)
	// decision-window thresholds — caller-injected (th, the once-per-command-scope
	// shared load — the load-failure fallback is already applied by WriteThresholds).
	// The spans share the db.MinutesSpan/HoursSpan clamps (v1.19.3, the R77-R78
	// sweep): a pair_window_min/chain_window_h override past the Duration ceiling
	// wraps the window negative (no pair would ever hit, no chain would ever count).
	pairWindow := db.MinutesSpan(th.PairWindowMin, store.DefaultThresholds().PairWindowMin)
	chainWindow := db.HoursSpan(th.ChainWindowH, store.DefaultThresholds().ChainWindowH)

	// ── G3 (+ exposure-consistency guard) ──
	deadCutoff := db.DaysCutoff(th.DeadDays, now)
	// The fetch window is max(days, 14 days) — fixing the defect where the trend's prev
	// window (the prior 7 days) was cut off by the reporting-window cutoff and trend stayed
	// permanently blank at the default --days 7. The tallies keep each metric's own window (Σ is after windowStart).
	fetchStart := windowStart
	if min14 := now.Add(-14 * 24 * time.Hour); min14.Before(fetchStart) {
		fetchStart = min14
	}

	// v1.16.0 T2 — phase 1: the 5 non-dependent SELECTs run concurrently (exposed, the
	// active/reused count, dead ids, read events, kreads). They write only their own local
	// slots; every out write happens after the join. Error selection stays deterministic —
	// after the join, the original sequential priority count > deadIDs (both propagate)
	// > read events (whole-effect demotion -> nil) picks the first failure. In the old
	// sequential chain a count/dead failure skipped the later lookups; here they may have
	// issued anyway — lookups only, no out writes, so the output is byte-identical.
	var (
		exposedRows [][]any
		countRows   [][]any
		deadIDs     [][]any
		evs         []ReadEventRow
		recalls     []RecallRow
		countErr    error
		deadErr     error
		evsErr      error
	)
	var g errgroup.Group
	// the recent hot exposure set — hot --full raises the injected ids' read_count via BumpReads,
	// so on the normal path exposed knowledge is outside the read_count=0 candidate set
	// to begin with. This guard patches the mismatch that arises when the reference increment and
	// the exposure record go through separate paths and only the increment fails
	// (metering loss). A lookup failure is the empty set (guard skipped, demoted).
	g.Go(func() error {
		exposedRows, _ = wire.QueryOnce(ctx, st,
			`SELECT ids FROM hot_events WHERE ts >= $1`, []any{deadCutoff})
		return nil
	})
	g.Go(func() error {
		countRows, countErr = wire.QueryOnce(ctx, st, `
			SELECT count(*),
			       count(CASE WHEN read_count > 0 THEN 1 END)
			  FROM knowledge
			 WHERE status = 'active' AND invalid_at IS NULL`, nil)
		return nil
	})
	g.Go(func() error {
		deadIDs, deadErr = wire.QueryOnce(ctx, st, `
			/* dead */ SELECT id FROM knowledge
			 WHERE status = 'active' AND invalid_at IS NULL
			   AND read_count = 0 AND ingested_at < $1`, []any{deadCutoff})
		return nil
	})
	// ── G4 ── event logs — on failure, all of G4 demotes to 0 (evsErr -> nil below).
	g.Go(func() error {
		evs, evsErr = queryReadEvents(ctx, st, fetchStart)
		return nil
	})
	// the G4-consistent recall source (kreads) — lookup failures demote to blank inside the helper.
	g.Go(func() error {
		recalls = queryKnowledgeReads(ctx, st, windowStart.Add(-reportSessionLookback))
		return nil
	})
	g.Wait()
	if countErr != nil {
		return countErr
	}
	if deadErr != nil {
		return deadErr
	}
	exposed := map[int]bool{}
	for _, r := range exposedRows {
		if len(r) > 0 {
			if s, ok := r[0].(string); ok {
				for _, id := range knowledge.ParseIDsCSV(s) {
					exposed[id] = true
				}
			}
		}
	}
	var active, reused int
	if len(countRows) > 0 {
		ReadCounters(countRows[0], &active, &reused)
	}
	out.Effect.ReusePct = PctOf(reused, active)
	dead := 0
	for _, r := range deadIDs {
		if len(r) == 0 {
			continue
		}
		if id, ok := render.CellToInt(r[0]); ok && !exposed[id] {
			dead++
		}
	}
	out.Effect.DeadPct = PctOf(dead, active)
	// the G3 products above are set before the read-events demotion (the original
	// sequential order — reuse_pct/dead_pct survive an event-log failure).
	if evsErr != nil {
		return nil
	}

	// only in-reporting-window events enter the G4 metric denominators (the fetch spans 14 days for the trend — above).
	inWin := make([]ReadEventRow, 0, len(evs))
	for _, e := range evs {
		if !e.Ts.Before(windowStart) {
			inWin = append(inWin, e)
		}
	}
	out.Effect.ReadEventsN = len(inWin)
	fallbacks := 0
	for _, e := range inWin {
		if e.Fallback {
			fallbacks++
		}
	}
	out.Effect.FallbackPct = PctOf(fallbacks, len(inWin)) // the denominator is also the reporting window
	// the top-level fallback_pct also moves to the same computation (the old cumulative
	// read_fallback_calls/read_stats calls denominator is abolished — a single source
	// including the HTML path. Equivalence: the same window and the same event source, so the value is unchanged).
	out.FallbackPct = out.Effect.FallbackPct
	// hit-quality approximation (first_rank, the fallback-excluded contract). The exact/top1
	// definitions are shared with trendOf as a single implementation.
	q := qualityOf(inWin)
	out.Effect.ExactPct = PctOf(q.exact, len(inWin))
	out.Effect.Top1Pct = PctOf(q.top1, q.exact)
	out.Effect.MRR = math.Round(q.rrSum/math.Max(float64(q.exact), 1)*1000) / 1000
	// the 7-day window trend (cur = the last 7 days, prev = the prior 7). Insufficient-sample
	// windows (0 events) demote to 0 (§13 — no interpretation of an empty window).
	cur, prev := TrendWindow(now, evs)
	out.Effect.Trend = cur.Diff(prev)

	// ── observability extensions ── zero-hit decomposition (§2.2), token-accounting cost (§2.3),
	// recall fate (§2.6), hot->read chaining (§2.1), and the G4-consistent source (kreads). Each lookup
	// failure demotes only that metric to 0 (the §13 metering principle — reporting unchanged).
	collectZeroDecompose(out, inWin)

	// v1.16.0 T2 — phase 2: the post-join collectors run concurrently. The writes/session
	// gate (at least one source exists) is evaluated after the phase-1 join, as before.
	// Field exclusivity: collectCost writes only out.Effect.Cost (its out.Effect.Bytes /
	// DupPrevented reads are collectDaily products — already complete before the parallel
	// report section), collectRecallFate only SurvivedPct/BadRecallPct, and the two queries
	// write only their own locals — no overlap. Both queries demote by contract (werr/serr),
	// so no error priority is needed here. Phase 3 (the recall lead, ProfileHint, pairing,
	// chaining) stays sequential below.
	var (
		writes []WriteRow
		werr   error
		starts []time.Time
		serr   error
	)
	// the recall lead (kreads) needs writes even without read events — the existing contract
	// of skipping the pairing writes lookup when the events are empty or the lookup fails
	// (the zero guard) is opened as an exception only when the kreads source exists.
	gated := len(inWin) > 0 || len(recalls) > 0
	g.Go(func() error { collectCost(ctx, st, out, inWin); return nil })
	g.Go(func() error { collectRecallFate(ctx, st, out, recalls, now); return nil })
	g.Go(func() error {
		if gated {
			writes, werr = queryWrites(ctx, st, windowStart)
		}
		return nil
	})
	// session boundaries (D) — on lookup failure or absence, the time-window fallback (old
	// environments, early days). The same gate as the writes condition above — the lookup runs only
	// when at least one source exists (inheriting the zero-guard contract).
	g.Go(func() error {
		if gated {
			starts, serr = queryTimes(ctx, st,
				`SELECT ts FROM session_starts WHERE ts >= $1 ORDER BY ts`,
				windowStart.Add(-reportSessionLookback))
		}
		return nil
	})
	g.Wait()
	hasSessions := serr == nil && len(starts) > 0
	collectHotReadChain(out, recalls, windowStart, pairWindow)

	// the recall-lead session ratio (D, consistent with spec §2.1 G4) — recomputed from the
	// knowledge_reads source (regardless of kind — including hot injection and headline exposure)
	// so that hot-led sessions count as recall-led. When kreads is empty (right after an upgrade),
	// it inherits the old read_events-assignment computation (the equivalent fallback).
	if hasSessions {
		if len(recalls) > 0 {
			recallTimes := make([]time.Time, 0, len(recalls))
			for _, r := range recalls {
				recallTimes = append(recallTimes, r.Ts)
			}
			out.Effect.RecallLedPct = recallLedPctTimes(starts, recallTimes, writes, windowStart)
		} else {
			out.Effect.RecallLedPct = recallLedPct(starts, inWin, writes, windowStart)
		}
	}
	// even for an empty window the hint concludes with "deferred (insufficient samples)"
	// — preventing a permanent blank.
	out.Effect.ProfileHint = ProfileHint(th, out.Effect)
	if len(inWin) == 0 {
		return nil // denominator 0 — pairing and chaining unnecessary (the zero-guard convention)
		// the G4 tallies (pairs, chain) all use the reporting-window (inWin)
		// source — no leakage from the 14-day trend fetch.
	}
	if werr != nil {
		return nil // the same demotion — pairing impossible, pairs 0
	}

	// pairing — a write within 30m after a read in the same session (both assigned).
	// Unassigned events and session-less environments use the time-window fallback.
	writeTimes := make([]time.Time, 0, len(writes))
	sessWriteTimes := map[int][]time.Time{}
	for _, w := range writes {
		writeTimes = append(writeTimes, w.Ts)
		if hasSessions {
			if si := sessionIndexOf(starts, w.Ts); si >= 0 {
				sessWriteTimes[si] = append(sessWriteTimes[si], w.Ts)
			}
		}
	}
	pairs := 0
	for _, e := range inWin { // pair only the reporting-window events
		cands := writeTimes
		if hasSessions {
			if si := sessionIndexOf(starts, e.Ts); si >= 0 {
				cands = sessWriteTimes[si]
			}
		}
		if TimeWindowHit(cands, e.Ts, pairWindow) {
			pairs++
		}
	}
	out.Effect.ReadWritePairs = pairs
	out.Effect.PairPct = PctOf(pairs, len(inWin))

	// usage chaining (B) — the share of reads whose returned id is followed within 24h by
	// a re-return of the same id or a same-topic write. Without id-bearing events, the topics
	// lookup is skipped (denominator certainly 0 — removing a needless scan). The topic map is a full knowledge
	// lookup (invalidated and superseded knowledge is also needed for chain matching — it may have
	// been active at read time). A lookup failure demotes only the (b) path.
	// (the recall lead and ProfileHint are in the session-assignment block above —
	// the hint-guard location contract is kept as-is.)
	hasIDs := false
	for _, e := range inWin {
		if len(e.Ids) > 0 {
			hasIDs = true
			break
		}
	}
	if !hasIDs {
		return nil
	}
	topics := map[int]string{}
	if rows, err := wire.QueryOnce(ctx, st,
		`SELECT id, topic FROM knowledge`, nil); err == nil {
		for _, r := range rows {
			if len(r) < 2 {
				continue
			}
			if id, ok := render.CellToInt(r[0]); ok {
				topics[id] = fmt.Sprint(r[1])
			}
		}
	}
	out.Effect.ChainPct = ChainPct(inWin, writes, topics, chainWindow) // reporting-window source
	return nil
}

// ProfileHint — the D1 reduced form: an observation-based profile hint (reporting only —
// no automatic switching, METRICS §5). Below the sample floor (10 recall events — inheriting
// the zero-near sample convention) it is "deferred"; after that, recall_led_pct-based guidance.
func ProfileHint(th store.Thresholds, e EffectOutput) string {
	// the sample floor inherits the zero-near sample convention (zero_near_min_read) — tunable.
	if e.ReadEventsN < th.ZeroNearMinRead {
		return "insufficient sample — verdict deferred"
	}
	if e.RecallLedPct >= 50 {
		return "keep normal — recall-led majority"
	}
	return "low recall lead — consider the minimal profile (advisory only, no automatic switch)"
}

// ReadEventRow — the computed view of one read_events row (G4).
// Adds Bytes (output bytes), ZeroReason ('unowned'|'owned_miss'|"),
// and Hits (the raw returned-row count) — the sources of token-accounting cost and the zero-hit decomposition.
type ReadEventRow struct {
	Ts         time.Time
	Zero       bool
	Fallback   bool
	FirstRank  int
	Ids        []int
	Bytes      int
	ZeroReason string
	Hits       int
}

// cellToBool — cell-to-bool conversion (accepting both the string "true" and bool).
func cellToBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return fmt.Sprint(v) == "true"
}

// TrendStat — (hit%, fallback%, top1%) of in-window events.
type TrendStat struct {
	HitPct, FallbackPct, Top1Pct int
	N                            int
}

// TrendWindow — the cur (last 7 days) and prev (prior 7 days) window stats.
func TrendWindow(now time.Time, evs []ReadEventRow) (cur, prev TrendStat) {
	cut := now.Add(-7 * 24 * time.Hour)
	prevCut := now.Add(-14 * 24 * time.Hour)
	var ce, pe []ReadEventRow
	for _, e := range evs {
		switch {
		case !e.Ts.Before(cut):
			ce = append(ce, e)
		case e.Ts.Before(cut) && !e.Ts.Before(prevCut):
			pe = append(pe, e)
		}
	}
	return trendOf(ce), trendOf(pe)
}

// qualityCounts — first_rank-based quality tallying (exact match, rank-1, and reciprocal-rank sums).
// A single source of the definitions: shared by both the effect metrics and trendOf.
type qualityCounts struct {
	exact, top1 int
	rrSum       float64
}

func qualityOf(evs []ReadEventRow) qualityCounts {
	var q qualityCounts
	for _, e := range evs {
		if e.FirstRank > 0 {
			q.exact++
			q.rrSum += 1.0 / float64(e.FirstRank)
			if e.FirstRank == 1 {
				q.top1++
			}
		}
	}
	return q
}

// trendOf — the stats of an event slice (an empty window is 0).
func trendOf(evs []ReadEventRow) TrendStat {
	if len(evs) == 0 {
		return TrendStat{}
	}
	hit, fb := 0, 0
	for _, e := range evs {
		if !e.Zero {
			hit++
		}
		if e.Fallback {
			fb++
		}
	}
	q := qualityOf(evs)
	return TrendStat{
		HitPct: PctOf(hit, len(evs)), FallbackPct: PctOf(fb, len(evs)),
		Top1Pct: PctOf(q.top1, q.exact), N: len(evs),
	}
}

// diff — the current-period trend point versus the previous (an empty window demotes to 0 — no comparison without samples).
func (cur TrendStat) Diff(prev TrendStat) *TrendOutput {
	c, p := cur, prev
	if c.N == 0 || p.N == 0 {
		return nil
	}
	tp := func(a, b int) TrendPoint { return TrendPoint{Prev: b, Cur: a, Delta: a - b} }
	return &TrendOutput{
		HitPct:      tp(c.HitPct, p.HitPct),
		FallbackPct: tp(c.FallbackPct, p.FallbackPct),
		Top1Pct:     tp(c.Top1Pct, p.Top1Pct),
	}
}

// WriteRow — the computed view of one in-window knowledge commit (pairing and chaining).
type WriteRow struct {
	Ts    time.Time
	Topic string
}

// queryReadEvents — reads the in-window read events in ascending ts order (chaining's
// after-determination assumes order). Unmappable cells are silently skipped (the
// render.CellToInt convention — missing metering must not break the report).
func queryReadEvents(ctx context.Context, st wire.Store, cutoff time.Time) ([]ReadEventRow, error) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT ts, zero, fallback, ids, first_rank, bytes, zero_reason, hits FROM read_events WHERE ts >= $1 ORDER BY ts`,
		[]any{cutoff})
	if err != nil {
		return nil, err
	}
	out := make([]ReadEventRow, 0, len(rows))
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		t, ok := CellToTime(r[0])
		if !ok {
			continue
		}
		// column order (ts, zero, fallback, ids, first_rank) —
		// the two leftover legacy-SELECT (ts,fallback,ids) parse blocks produced fallback=zero
		// contamination and unparsed ids (chain collapse). Consolidated into a single path.
		// bytes, zero_reason, and hits are appended at the end (old 5-column rows get
		// the defaults 0 and empty — unextended-DB and fake compatible).
		e := ReadEventRow{Ts: t, Zero: cellToBool(r[1]), Fallback: cellToBool(r[2])}
		if len(r) > 3 {
			if s, ok := r[3].(string); ok {
				e.Ids = knowledge.ParseIDsCSV(s)
			}
		}
		if len(r) >= 5 {
			if n, ok := render.CellToInt(r[4]); ok {
				e.FirstRank = n
			}
		}
		if len(r) >= 6 {
			if n, ok := render.CellToInt(r[5]); ok {
				e.Bytes = n
			}
		}
		if len(r) >= 7 {
			if s, ok := r[6].(string); ok {
				e.ZeroReason = s
			}
		}
		if len(r) >= 8 {
			if n, ok := render.CellToInt(r[7]); ok {
				e.Hits = n
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// queryWrites — reads the in-window knowledge commits (time, topic) in ascending ts order.
func queryWrites(ctx context.Context, st wire.Store, cutoff time.Time) ([]WriteRow, error) {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT ingested_at, topic FROM knowledge WHERE ingested_at >= $1 ORDER BY ingested_at`,
		[]any{cutoff})
	if err != nil {
		return nil, err
	}
	out := make([]WriteRow, 0, len(rows))
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		t, ok := CellToTime(r[0])
		if !ok {
			continue
		}
		out = append(out, WriteRow{Ts: t, Topic: fmt.Sprint(r[1])})
	}
	return out, nil
}

// queryTimes — reads a 1-column time query as a time slice (the session boundary lookup).
// Unmappable cells are silently skipped (missing metering must not break the report).
func queryTimes(ctx context.Context, st wire.Store, sql string, cutoff time.Time) ([]time.Time, error) {
	rows, err := wire.QueryOnce(ctx, st, sql, []any{cutoff})
	if err != nil {
		return nil, err
	}
	out := make([]time.Time, 0, len(rows))
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		if t, ok := CellToTime(r[0]); ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// CellToTime — cell (any) -> time conversion. An alias of the single wire.CellToTime
// implementation (keeping the export for cli-consumer compatibility — the same contract).
var CellToTime = wire.CellToTime

// sessionIndexOf — the index of the session containing ts in starts (ascending): the last
// start <= ts. Before the first start, -1 (unassigned — a fallback-pairing target).
func sessionIndexOf(starts []time.Time, ts time.Time) int {
	return sort.Search(len(starts), func(i int) bool { return starts[i].After(ts) }) - 1
}

// TimeWindowHit — true if any time falls inside (0 < Δ <= window) after base.
// Assumes times sorted ascending.
func TimeWindowHit(times []time.Time, base time.Time, window time.Duration) bool {
	i := sort.Search(len(times), func(i int) bool { return times[i].After(base) })
	return i < len(times) && !times[i].After(base.Add(window))
}

// recallLedPct — the in-window started sessions' recall-lead rate: among writing (write >= 1)
// sessions, the share where the first read precedes the first write (recall drove production).
// Sessions started outside the window (the margin portion) are excluded; unassigned events
// are not attributed to any session. The equivalent fallback of the re-computed
// kreads-source path (recallLedPctTimes) for kreads-less environments (inheriting the old read_events assignment).
func recallLedPct(starts []time.Time, evs []ReadEventRow, writes []WriteRow, windowStart time.Time) int {
	times := make([]time.Time, 0, len(evs))
	for _, e := range evs {
		times = append(times, e.Ts)
	}
	return recallLedPctTimes(starts, times, writes, windowStart)
}

// recallLedPctTimes — the recall-lead rate from a recall-time slice source (kreads, regardless
// of kind — consistent with spec §2.1 G4). The verdict body is identical to recallLedPct (session
// assignment — a single implementation differing only in the time source).
func recallLedPctTimes(starts []time.Time, recallTimes []time.Time, writes []WriteRow, windowStart time.Time) int {
	type sess struct {
		firstRead, firstWrite time.Time
		hasRead, hasWrite     bool
	}
	sessions := map[int]*sess{}
	for i, s := range starts {
		if !s.Before(windowStart) {
			sessions[i] = &sess{}
		}
	}
	for _, ts := range recallTimes {
		if si := sessionIndexOf(starts, ts); si >= 0 {
			if ss := sessions[si]; ss != nil && (!ss.hasRead || ts.Before(ss.firstRead)) {
				ss.firstRead, ss.hasRead = ts, true
			}
		}
	}
	for _, w := range writes {
		if si := sessionIndexOf(starts, w.Ts); si >= 0 {
			if ss := sessions[si]; ss != nil && (!ss.hasWrite || w.Ts.Before(ss.firstWrite)) {
				ss.firstWrite, ss.hasWrite = w.Ts, true
			}
		}
	}
	active, led := 0, 0
	for _, ss := range sessions {
		if !ss.hasWrite {
			continue
		}
		active++
		if ss.hasRead && ss.firstRead.Before(ss.firstWrite) {
			led++
		}
	}
	return PctOf(led, active)
}

// p95MinSamples — the per-surface minimum sample count for reporting p95 (spec §2.3 — denominator < 5 is 0).
const p95MinSamples = 5

// p95NearestRank — nearest-rank p95 (after sorting, ceil(0.95n)-1 — the same formula
// as HotP95). Fewer than 5 samples or an empty slice is 0 (the zero-denominator guard convention).
func p95NearestRank(samples []int) int {
	if len(samples) < p95MinSamples {
		return 0
	}
	s := append([]int(nil), samples...)
	sort.Ints(s)
	return HotP95(s)
}

// RecallRow — the computed view of one knowledge_reads row (schema v10).
type RecallRow struct {
	KnowledgeID int
	Ts          time.Time
	Kind        string // 'read'|'hot'
}

// queryKnowledgeReads — reads the per-item recall history after the cutoff in ascending ts
// order (the shared source of recall fate, chaining, and the lead — the cutoff is window + margin,
// by the caller). Unmappable cells and lookup failures are silently skipped/blank (the §13 metering convention).
func queryKnowledgeReads(ctx context.Context, st wire.Store, cutoff time.Time) []RecallRow {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT knowledge_id, ts, kind FROM knowledge_reads WHERE ts >= $1 ORDER BY ts`,
		[]any{cutoff})
	if err != nil {
		return nil
	}
	out := make([]RecallRow, 0, len(rows))
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok {
			continue
		}
		ts, ok := CellToTime(r[1])
		if !ok {
			continue
		}
		out = append(out, RecallRow{KnowledgeID: id, Ts: ts, Kind: fmt.Sprint(r[2])})
	}
	return out
}

// collectZeroDecompose — zero-hit decomposition (spec §2.2 CRAG): the zero_reason tally of
// in-window read events. The unowned (active knowledge < floor — effectively not held)
// and owned_miss (held but not found) ratios — the denominator is the same as zero_pct's (the in-window
// read count); the two metrics' sum is consistent with zero_pct once reasons are recorded.
func collectZeroDecompose(out *ReportOutput, inWin []ReadEventRow) {
	var unowned, ownedMiss int
	for _, e := range inWin {
		switch e.ZeroReason {
		case "unowned":
			unowned++
		case "owned_miss":
			ownedMiss++
		}
	}
	out.Effect.ZeroUnownedPct = PctOf(unowned, len(inWin))
	out.Effect.ZeroOwnedMissPct = PctOf(ownedMiss, len(inWin))
}

// collectCost — the token-accounting cost object (spec §2.3). The savings lower bound = Σdup x the mean
// write bytes (the dedup logical-physical formula — a deterministic lower bound on the unrecorded
// duplicates prevented); the hit injection cost = Σhit x the mean summary bytes (not a savings claim).
// The mean is the average summary length over all knowledge (floored) — no personal wording
// recorded, byte tallies only. Read p95 is the in-window event bytes distribution (rows with
// bytes > 0 recorded); hot p95 is hot_events-derived (DerivedHotStats — retention window); brief is 0, having no per-event source.
func collectCost(ctx context.Context, st wire.Store, out *ReportOutput, inWin []ReadEventRow) {
	c := EffectCost{
		Read: CostSurface{Mode: costModeOnInvoke,
			Bytes14d: out.Effect.Bytes.Read.Bytes14d, AvgBytes: out.Effect.Bytes.Read.AvgBytes},
		Hot: CostSurface{Mode: costModeAlwaysOn,
			Bytes14d: out.Effect.Bytes.Hot.Bytes14d, AvgBytes: out.Effect.Bytes.Hot.AvgBytes},
		Brief: CostSurface{Mode: costModeOnInvoke,
			Bytes14d: out.Effect.Bytes.Brief.Bytes14d, AvgBytes: out.Effect.Bytes.Brief.AvgBytes},
		SavingsLabel: CostSavingsLabel,
		HitCostLabel: CostHitLabel,
	}
	samples := make([]int, 0, len(inWin))
	hitSum := 0
	for _, e := range inWin {
		if e.Bytes > 0 {
			samples = append(samples, e.Bytes)
		}
		hitSum += e.Hits
	}
	c.Read.P95Bytes = p95NearestRank(samples)
	_, hotP95 := DerivedHotStats(ctx, st)
	c.Hot.P95Bytes = hotP95
	if lens := KnowledgeSummaryLens(ctx, st); len(lens) > 0 {
		sum := 0
		for _, v := range lens {
			sum += v
		}
		avg := sum / len(lens)
		c.SavingsLowerBoundBytes = out.Effect.DupPrevented * avg
		c.HitInjectionCostBytes = hitSum * avg
	}
	out.Effect.Cost = c
}

// recallFateDays — the elapsed days for the recall-fate verdict (spec §2.6 — fate after 7 days).
const recallFateDays = 7

// collectRecallFate — recall fate (spec §2.6): of the in-window recalled knowledge, the current
// state of those whose first recall was 7+ days ago — survived (no invalid_at or superseded_by)
// vs bad (superseded/invalidated). Without verdict subjects (under 7 days elapsed), 0.
// The denominator is the number of verdict-subject ids — absent rows (exceptional deletions) are also included as subjects.
func collectRecallFate(ctx context.Context, st wire.Store, out *ReportOutput, recalls []RecallRow, now time.Time) {
	first := map[int]time.Time{}
	for _, r := range recalls {
		if prev, ok := first[r.KnowledgeID]; !ok || r.Ts.Before(prev) {
			first[r.KnowledgeID] = r.Ts
		}
	}
	cutoff := db.DaysCutoff(recallFateDays, now)
	judged := map[int]bool{}
	for id, ts := range first {
		if !ts.After(cutoff) {
			judged[id] = true
		}
	}
	if len(judged) == 0 {
		return // no verdict subjects — stays 0
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT id, invalid_at, superseded_by FROM knowledge`, nil)
	if err != nil {
		return // metering demotion — stays 0 (§13)
	}
	survived, bad := 0, 0
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		id, ok := render.CellToInt(r[0])
		if !ok || !judged[id] {
			continue
		}
		if r[1] != nil || r[2] != nil {
			bad++
		} else {
			survived++
		}
	}
	out.Effect.SurvivedPct = PctOf(survived, len(judged))
	out.Effect.BadRecallPct = PctOf(bad, len(judged))
}

// collectHotReadChain — hot->read chaining (spec §2.1 gap 8): the share of hot exposures
// (injection and headline) followed within the pairing window (default 30m — inheriting the
// pair_window_minutes SSOT) by a read of the same knowledge. The denominator is the in-window hot exposure row count (per item).
func collectHotReadChain(out *ReportOutput, recalls []RecallRow, windowStart time.Time, window time.Duration) {
	var hotRows, chained int
	for _, h := range recalls {
		if h.Kind != "hot" || h.Ts.Before(windowStart) {
			continue
		}
		hotRows++
		for _, rr := range recalls {
			if rr.Kind != "read" || rr.KnowledgeID != h.KnowledgeID {
				continue
			}
			if d := rr.Ts.Sub(h.Ts); d > 0 && d <= window {
				chained++
				break
			}
		}
	}
	out.Effect.HotReadChainPct = PctOf(chained, hotRows)
}

// ChainPct — the usage chain rate: among reads bearing returned ids, the share followed
// within 24h by (a) a re-return of the same id in a later read, or (b) a same-topic
// write. evs and writes are assumed ascending by ts.
func ChainPct(evs []ReadEventRow, writes []WriteRow, topics map[int]string, window time.Duration) int {
	withIDs, used := 0, 0
	for i, e := range evs {
		if len(e.Ids) == 0 {
			continue
		}
		withIDs++
		idset := make(map[int]bool, len(e.Ids))
		for _, id := range e.Ids {
			idset[id] = true
		}
		hit := false
		// (a) a re-return within the following 24h — Δ>0 required (consistent with the pairing's
		// 0 < Δ rule; a simultaneous record (Δ=0) is not a chain).
		for j := i + 1; j < len(evs) && !hit; j++ {
			if evs[j].Ts.After(e.Ts.Add(window)) {
				break
			}
			if !evs[j].Ts.After(e.Ts) {
				continue
			}
			for _, id := range evs[j].Ids {
				if idset[id] {
					hit = true
					break
				}
			}
		}
		// (b) a same-topic write within the following 24h.
		for _, w := range writes {
			if hit {
				break
			}
			if !w.Ts.After(e.Ts) || w.Ts.After(e.Ts.Add(window)) {
				continue
			}
			for _, id := range e.Ids {
				if t := topics[id]; t != "" && t == w.Topic {
					hit = true
					break
				}
			}
		}
		if hit {
			used++
		}
	}
	return PctOf(used, withIDs)
}
