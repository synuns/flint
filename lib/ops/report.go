package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

const reportDaysDefault = 7

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

	ReadZero int `json:"read_zero"`
	ReadNear int `json:"read_zero_near"`

	Brief      int `json:"brief"`
	ReadBytes  int `json:"read_bytes"`
	HotBytes   int `json:"hot_bytes"`
	BriefBytes int `json:"brief_bytes"`
}

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

	DupPct      int `json:"dup_pct"`
	FallbackPct int `json:"fallback_pct"`

	DupHook   int `json:"dup_hook"`
	DupManual int `json:"dup_manual"`

	ZeroNearPct int `json:"zero_near_pct"`

	Effect EffectOutput `json:"effect"`

	Curation CurationCounters `json:"curation"`

	Diagnosis []DiagnosisItem `json:"diagnosis"`
}

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

type EffectOutput struct {
	HitPct         int `json:"hit_pct"`
	NearPct        int `json:"near_pct"`
	FallbackPct    int `json:"fallback_pct"`
	DupPrevented   int `json:"dup_prevented"`
	ReusePct       int `json:"reuse_pct"`
	DeadPct        int `json:"dead_pct"`
	ReadEventsN    int `json:"read_events_n"`
	ReadWritePairs int `json:"read_write_pairs"`
	PairPct        int `json:"pair_pct"`

	RecallLedPct int `json:"recall_led_pct"`
	ChainPct     int `json:"chain_pct"`

	ProfileHint string `json:"profile_hint"`

	ExactPct int     `json:"exact_pct"`
	Top1Pct  int     `json:"top1_pct"`
	MRR      float64 `json:"mrr"`

	Trend *TrendOutput `json:"trend,omitempty"`

	Bytes EffectBytes `json:"bytes"`

	ZeroUnownedPct    int        `json:"zero_unowned_pct"`
	ZeroOwnedMissPct  int        `json:"zero_owned_miss_pct"`
	ZeroHitUnownedPct float64    `json:"zero_hit_unowned_pct"`
	SurvivedPct       int        `json:"survived_pct"`
	BadRecallPct      int        `json:"bad_recall_pct"`
	HotReadChainPct   int        `json:"hot_read_chain_pct"`
	Cost              EffectCost `json:"cost"`
}

const (
	CostSavingsLabel = "savings lower-bound estimate"
	CostHitLabel     = "injection cost"

	costModeAlwaysOn = "always-on"
	costModeOnInvoke = "on-invoke"
)

type EffectCost struct {
	Read                   CostSurface `json:"read"`
	Hot                    CostSurface `json:"hot"`
	Brief                  CostSurface `json:"brief"`
	SavingsLowerBoundBytes int         `json:"savings_lower_bound_bytes"`
	SavingsLabel           string      `json:"savings_label"`
	HitInjectionCostBytes  int         `json:"hit_injection_cost_bytes"`
	HitCostLabel           string      `json:"hit_cost_label"`

	summaryAvgBytes int `json:"-"`
}

type CostSurface struct {
	Mode     string `json:"mode"`
	Bytes14d int    `json:"bytes_14d"`
	AvgBytes int    `json:"avg_bytes"`
	P95Bytes int    `json:"p95_bytes"`
}

type EffectBytes struct {
	Read  SurfaceBytes `json:"read"`
	Hot   SurfaceBytes `json:"hot"`
	Brief SurfaceBytes `json:"brief"`
}

type SurfaceBytes struct {
	Bytes14d int `json:"bytes_14d"`
	AvgBytes int `json:"avg_bytes"`
}

func avgBytes(bytes, calls int) int {
	if calls <= 0 {
		return 0
	}
	return bytes / calls
}

type TrendOutput struct {
	HitPct      TrendPoint `json:"hit_pct"`
	FallbackPct TrendPoint `json:"fallback_pct"`
	Top1Pct     TrendPoint `json:"top1_pct"`
}

type TrendPoint struct {
	Prev  int `json:"prev"`
	Cur   int `json:"cur"`
	Delta int `json:"delta"`
}

const reportSessionLookback = 24 * time.Hour

var serveNewStoreFn func(context.Context) (wire.Store, error)

func SetServeNewStoreFn(fn func(context.Context) (wire.Store, error)) { serveNewStoreFn = fn }

func serveStoreFactory() func(context.Context) (wire.Store, error) {
	return serveNewStoreFn
}

func runReportServe(ctx context.Context, st wire.Store, days, port int, stdout io.Writer) int {
	factory := serveStoreFactory()
	if factory == nil {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("report: --serve store factory not injected")))
		return 3
	}
	s := NewServeServer(factory, days, port)
	if s.err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(s.err))
		return 1
	}
	defer s.Close()
	fmt.Fprintf(stdout, "flint report --serve: http://127.0.0.1:%d (Ctrl-C to stop)\n", s.Port)
	fmt.Fprintln(stdout, "  GET / · GET /api/diagnose.json · GET /api/sections/<area>.json")
	serveCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- s.ListenAndServe() }()
	select {
	case <-serveCtx.Done():
		s.Close()
		return 0
	case err := <-done:
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 1
		}
		return 0
	}
}

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

func FallbackPct(fallbackCalls int, readCalls int) int {
	if readCalls == 0 {
		return 0
	}
	return int(100.0 * float64(fallbackCalls) / float64(readCalls))
}

func RunReport(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--html"},
			{Name: "--text"},
			{Name: "--out", Value: true},
			{Name: "--days", Value: true},
			{Name: "--serve"},
			{Name: "--port", Value: true},
		},
		Usage: i18n.T("usage.report.args") + " [--serve] [--port N]",
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	days := reportDaysDefault
	htmlOut := ""
	textOut := res.Given("--text")
	serveOn := false
	servePort := 0
	portGiven := false
	for _, o := range res.AllOccs() {
		switch o.Name {
		case "--serve":
			serveOn = true
		case "--port":
			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("report: --port requires a number — %s", i18n.T("usage.report.args")+" [--serve] [--port N]")))
				return 3
			}
			n, err := strconv.Atoi(o.Value)
			if err != nil || n < 1 || n > 65535 {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("report: invalid port %q — "+i18n.T("usage.report.args")+" [--serve] [--port N]", o.Value)))
				return 3
			}
			servePort, portGiven = n, true
		case "--html":
			htmlOut = "report.html"
		case "--out":
			if !o.HasValue {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", "report: "+i18n.T("err.out_requires_path")+" — "+i18n.T("usage.report.args"))))
				return 3
			}

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
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", i18n.T("err.days_requires_number")+" — "+i18n.T("usage.report.args"))))
				return 3
			}
			n, err := strconv.Atoi(o.Value)
			if err != nil || n < 1 {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("report: invalid days %q — "+i18n.T("usage.report.args"), o.Value)))
				return 3
			}
			days = n
		}
	}

	th := knowledge.WriteThresholds(ctx, st)

	if max := th.DailyRetentionD; max > 0 && days > max {
		days = max
	}

	if serveOn && htmlOut != "" {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", "report: --serve and --html/--out are exclusive — "+i18n.T("usage.report.args")+" [--serve] [--port N]")))
		return 3
	}
	if portGiven && !serveOn {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", "report: --port requires --serve — "+i18n.T("usage.report.args")+" [--serve] [--port N]")))
		return 3
	}
	if serveOn {
		return runReportServe(ctx, st, days, servePort, stdout)
	}

	if htmlOut != "" {
		if res.Given("--out") {
			fmt.Fprintln(stdout, DeprecationNotice("--out"))
		}
		return runReportHTML(ctx, st, days, htmlOut, stdout)
	}
	out := ReportOutput{Days: make([]ReportDay, 0, days)}

	var g errgroup.Group
	var dailyErr, statErr, countErr, effectErr error
	var daily dailyAgg
	g.Go(func() error { daily, dailyErr = collectDailyAgg(ctx, st, days); return nil })
	g.Go(func() error { collectCuration(ctx, st, days, &out); return nil })
	g.Go(func() error { statErr = collectStats(ctx, st, &out); return nil })
	g.Go(func() error { countErr = collectCounts(ctx, st, days, th, &out); return nil })
	g.Go(func() error { effectErr = collectEffect(ctx, st, days, th, &out); return nil })
	g.Wait()
	for _, err := range []error{dailyErr, statErr, countErr, effectErr} {
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
	}
	daily.applyTo(&out)

	out.DupPct = DupPct(out.Days)

	for _, d := range out.Days {
		out.DupHook += d.DupHook
		out.DupManual += d.DupManual
	}

	out.Diagnosis = Diagnose(out, th, knowledge.HotBudget(knowledge.MetaGetKV(ctx, st)))
	if textOut {
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
	fmt.Fprintf(w, "abstention: zero_hit_unowned_pct %.1f%% (share of zero-hit reads)\n",
		out.Effect.ZeroHitUnownedPct)

	if UpdateCacheInfoFn != nil {
		if _, checkedAt := UpdateCacheInfoFn(); checkedAt != "" {
			fmt.Fprintf(w, "update check: %s\n", checkedAt)
		}
	}
	if len(out.Diagnosis) == 0 {
		return
	}
	fmt.Fprintln(w, "diagnosis")
	for _, d := range out.Diagnosis {
		fmt.Fprintf(w, "- %s\n", d.Sentence())
	}
}

type dailyAgg struct {
	days         []ReportDay
	zeroPct      int
	zeroNearPct  int
	hitPct       int
	nearPct      int
	dupPrevented int
	bytes        EffectBytes
}

func collectDailyAgg(ctx context.Context, st wire.Store, days int) (dailyAgg, error) {
	dayRows, err := WindowedDaily(ctx, st, days)
	if err != nil {
		return dailyAgg{}, err
	}
	a := dailyAgg{days: dayRows}

	var sumRead, sumZero, sumNear, sumDup int

	var sumHot, sumBrief, sumReadB, sumHotB, sumBriefB int
	for _, d := range a.days {
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
	a.zeroPct = ReadZeroPct(sumRead, sumZero)
	a.zeroNearPct = ReadZeroPct(sumRead, sumNear)

	a.hitPct = PctOf(sumRead-sumZero, sumRead)
	a.nearPct = PctOf(sumNear, sumRead)
	a.dupPrevented = sumDup

	a.bytes = EffectBytes{
		Read:  SurfaceBytes{Bytes14d: sumReadB, AvgBytes: avgBytes(sumReadB, sumRead)},
		Hot:   SurfaceBytes{Bytes14d: sumHotB, AvgBytes: avgBytes(sumHotB, sumHot)},
		Brief: SurfaceBytes{Bytes14d: sumBriefB, AvgBytes: avgBytes(sumBriefB, sumBrief)},
	}
	return a, nil
}

func (a dailyAgg) applyTo(out *ReportOutput) {
	out.Days = a.days
	out.ZeroPct = a.zeroPct
	out.ZeroNearPct = a.zeroNearPct
	out.Effect.HitPct = a.hitPct
	out.Effect.NearPct = a.nearPct
	out.Effect.DupPrevented = a.dupPrevented
	out.Effect.Bytes = a.bytes
	out.Effect.Cost.Read.Bytes14d = a.bytes.Read.Bytes14d
	out.Effect.Cost.Read.AvgBytes = a.bytes.Read.AvgBytes
	out.Effect.Cost.Hot.Bytes14d = a.bytes.Hot.Bytes14d
	out.Effect.Cost.Hot.AvgBytes = a.bytes.Hot.AvgBytes
	out.Effect.Cost.Brief.Bytes14d = a.bytes.Brief.Bytes14d
	out.Effect.Cost.Brief.AvgBytes = a.bytes.Brief.AvgBytes
	out.Effect.Cost.SavingsLowerBoundBytes = a.dupPrevented * out.Effect.Cost.summaryAvgBytes
}

func collectDaily(ctx context.Context, st wire.Store, days int, out *ReportOutput) error {
	a, err := collectDailyAgg(ctx, st, days)
	if err != nil {
		return err
	}
	a.applyTo(out)
	return nil
}

func WindowedDaily(ctx context.Context, st wire.Store, days int) ([]ReportDay, error) {
	rows, err := wire.QueryOnce(ctx, st, `SELECT key, value FROM meta WHERE key LIKE 'daily:%'`, nil)
	if err != nil {
		return nil, err
	}
	daysOut := make([]ReportDay, 0, days)
	window := make(map[string]int, days)
	now := time.Now().UTC()
	for i := days - 1; i >= 0; i-- {
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
			continue
		}
		var d ReportDay
		if json.Unmarshal([]byte(fmt.Sprint(r[1])), &d) != nil {
			continue
		}
		d.Date = daysOut[idx].Date
		daysOut[idx] = d
	}

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
		idx, ok := window[date]
		if !ok {
			continue
		}
		setter, ok := dailyFlatFields[kind]
		if !ok {
			continue
		}
		n, perr := strconv.Atoi(strings.TrimSpace(fmt.Sprint(r[1])))
		if perr != nil {
			continue
		}
		setter(&daysOut[idx], n)
	}
	mergeDerivedReadDaily(ctx, st, daysOut)
	return daysOut, nil
}

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

func mergeDerivedReadDaily(ctx context.Context, st wire.Store, days []ReportDay) {
	if len(days) == 0 {
		return
	}

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
			continue
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

func collectStats(ctx context.Context, st wire.Store, out *ReportOutput) error {
	_, p95 := DerivedHotStats(ctx, st)
	out.HotP95 = p95
	return nil
}

var curationKinds = []string{
	"approve_done", "approve_not_found", "approve_supersede_err",
	"maintain_supersede", "maintain_expire", "maintain_prune_rows",
	"import_added", "import_skipped",
}

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

func PctOf(n, total int) int {
	if total <= 0 {
		return 0
	}
	return n * 100 / total
}

func CollectEffect(ctx context.Context, st wire.Store, days int, out *ReportOutput) error {
	return collectEffect(ctx, st, days, knowledge.WriteThresholds(ctx, st), out)
}

func collectEffect(ctx context.Context, st wire.Store, days int, th store.Thresholds, out *ReportOutput) error {
	now := time.Now()

	windowStart := db.DaysCutoff(days, now)

	pairWindow := db.MinutesSpan(th.PairWindowMin, store.DefaultThresholds().PairWindowMin)
	chainWindow := db.HoursSpan(th.ChainWindowH, store.DefaultThresholds().ChainWindowH)

	deadCutoff := db.DaysCutoff(th.DeadDays, now)

	fetchStart := windowStart
	if min14 := now.Add(-14 * 24 * time.Hour); min14.Before(fetchStart) {
		fetchStart = min14
	}

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

	g.Go(func() error {
		evs, evsErr = queryReadEvents(ctx, st, fetchStart)
		return nil
	})

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

	if evsErr != nil {
		return nil
	}

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
	out.Effect.FallbackPct = PctOf(fallbacks, len(inWin))

	out.FallbackPct = out.Effect.FallbackPct

	q := qualityOf(inWin)
	out.Effect.ExactPct = PctOf(q.exact, len(inWin))
	out.Effect.Top1Pct = PctOf(q.top1, q.exact)
	out.Effect.MRR = math.Round(q.rrSum/math.Max(float64(q.exact), 1)*1000) / 1000

	cur, prev := TrendWindow(now, evs)
	out.Effect.Trend = cur.Diff(prev)

	collectZeroDecompose(out, inWin)

	var (
		writes []WriteRow
		werr   error
		starts []time.Time
		serr   error
	)

	gated := len(inWin) > 0 || len(recalls) > 0
	g.Go(func() error { collectCost(ctx, st, out, inWin); return nil })
	g.Go(func() error { collectRecallFate(ctx, st, out, recalls, now); return nil })
	g.Go(func() error {
		if gated {
			writes, werr = queryWrites(ctx, st, windowStart)
		}
		return nil
	})

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

	out.Effect.ProfileHint = ProfileHint(th, out.Effect)
	if len(inWin) == 0 {
		return nil

	}
	if werr != nil {
		return nil
	}

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
	for _, e := range inWin {
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
	out.Effect.ChainPct = ChainPct(inWin, writes, topics, chainWindow)
	return nil
}

func ProfileHint(th store.Thresholds, e EffectOutput) string {

	if e.ReadEventsN < th.ZeroNearMinRead {
		return "insufficient sample — verdict deferred"
	}
	if e.RecallLedPct >= 50 {
		return "keep normal — recall-led majority"
	}
	return "low recall lead — consider the minimal profile (advisory only, no automatic switch)"
}

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

func cellToBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return fmt.Sprint(v) == "true"
}

type TrendStat struct {
	HitPct, FallbackPct, Top1Pct int
	N                            int
}

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

type WriteRow struct {
	Ts    time.Time
	Topic string
}

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

var CellToTime = wire.CellToTime

func sessionIndexOf(starts []time.Time, ts time.Time) int {
	return sort.Search(len(starts), func(i int) bool { return starts[i].After(ts) }) - 1
}

func TimeWindowHit(times []time.Time, base time.Time, window time.Duration) bool {
	i := sort.Search(len(times), func(i int) bool { return times[i].After(base) })
	return i < len(times) && !times[i].After(base.Add(window))
}

func recallLedPct(starts []time.Time, evs []ReadEventRow, writes []WriteRow, windowStart time.Time) int {
	times := make([]time.Time, 0, len(evs))
	for _, e := range evs {
		times = append(times, e.Ts)
	}
	return recallLedPctTimes(starts, times, writes, windowStart)
}

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

const p95MinSamples = 5

func p95NearestRank(samples []int) int {
	if len(samples) < p95MinSamples {
		return 0
	}
	s := append([]int(nil), samples...)
	sort.Ints(s)
	return HotP95(s)
}

type RecallRow struct {
	KnowledgeID int
	Ts          time.Time
	Kind        string
}

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
	out.Effect.ZeroHitUnownedPct = zeroHitUnownedPctOf(inWin)
}

func collectCost(ctx context.Context, st wire.Store, out *ReportOutput, inWin []ReadEventRow) {
	c := EffectCost{
		Read:         CostSurface{Mode: costModeOnInvoke},
		Hot:          CostSurface{Mode: costModeAlwaysOn},
		Brief:        CostSurface{Mode: costModeOnInvoke},
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
		c.summaryAvgBytes = avg
		c.SavingsLowerBoundBytes = out.Effect.DupPrevented * avg
		c.HitInjectionCostBytes = hitSum * avg
	}
	prev := out.Effect.Cost
	c.Read.Bytes14d = prev.Read.Bytes14d
	c.Read.AvgBytes = prev.Read.AvgBytes
	c.Hot.Bytes14d = prev.Hot.Bytes14d
	c.Hot.AvgBytes = prev.Hot.AvgBytes
	c.Brief.Bytes14d = prev.Brief.Bytes14d
	c.Brief.AvgBytes = prev.Brief.AvgBytes
	out.Effect.Cost = c
}

const recallFateDays = 7

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
		return
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT id, invalid_at, superseded_by FROM knowledge`, nil)
	if err != nil {
		return
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
