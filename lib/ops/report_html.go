// report_html.go — report --html — a single-file static dashboard.
// Principles: embedded template, framework-free, no external requests (local rendering only —
// METRICS §4 privacy), numbers only (no query bodies). Rendered by Go templates, so
// 0 JS dependencies — open the one file in a browser and it just works.
package ops

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// dashboardTmpl — the embedded template (a single-file artifact — no external asset references).
//
//go:embed dashboard.tmpl.html
var dashboardTmpl string

// sparkPaths — the (line, area) SVG path d strings for a value slice (Go-computed —
// the no-template-arithmetic principle). Flat guard (min==max -> max+1); 1 point yields empty values.
func sparkPaths(vals []int) (line, area string) {
	if len(vals) < 2 {
		return "", ""
	}
	const w, h = 20.0, 36.0
	min, max := vals[0], vals[0]
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	if max == min {
		max++ // flat guard
	}
	var b strings.Builder
	for i, v := range vals {
		x := w * float64(i) / float64(len(vals)-1)
		y := h - 2 - float64(v-min)/float64(max-min)*(h-4)
		if i == 0 {
			fmt.Fprintf(&b, "M%.1f %.1f", x, y)
		} else {
			fmt.Fprintf(&b, " L%.1f %.1f", x, y)
		}
	}
	line = b.String()
	area = line + fmt.Sprintf(" L%.1f %.1f L0 %.1f Z", w, h, h)
	return line, area
}

// deltaChip — the delta chip data (direction x positive — color alone is forbidden: sign + arrow).
type deltaChip struct {
	Show  bool
	Text  string // "▲ +7pp"
	Good  bool
	Label string // metric name (for the title)
}

// ChipFor — a Trend point -> a chip. nil (empty window) and 0 are not shown.
func ChipFor(label string, p TrendPoint, invert bool) deltaChip {
	if p == (TrendPoint{}) {
		return deltaChip{}
	}
	d := p.Delta
	good := d >= 0
	if invert { // fallback: rising = bad
		good = !good
	}
	sign := "+"
	if d < 0 {
		sign = ""
	}
	// discipline (Stripe): green = improvement only. Worsening is not red but a gray ▼ — the chip's
	// Good means only 'green marking' (gray is the .delta default color).
	// Human wording: "improved by 10% points" — dropping the pp + symbol combination
	// (for readers without context — direction arrows are the template CSS/color's job).
	word := "improving"
	if !good {
		word = "worsening"
	}
	return deltaChip{Show: true, Text: sign + fmt.Sprint(d) + " points " + word,
		Good: good, Label: label + " · vs last week"}
}

// TrendRow — one row of the trend table (Go precomputation — no template arithmetic).
type TrendRow struct {
	Name, DeltaText string
	Cur             int
	Up              bool // improvement direction (green) — distinguished from the gray ▼ (the no-red discipline)
}

// EstTok — rune count -> token estimate (display-only, ±30% explicit — A3 spec):
// a Korean rune ≈ 1.1 tokens, others ≈ /4 (a cl100k-class approximation).
func EstTok(runes int) int {
	return int(float64(runes)*0.55 + float64(runes)*0.45/4)
}

// RenderDashboard — template rendering (html/template — rendered on the Go side, so no JS needed).
func RenderDashboard(w io.Writer, d HtmlDashboard) error {
	t, err := template.New("dashboard").Funcs(template.FuncMap{"EstTok": EstTok}).Parse(dashboardTmpl)
	if err != nil {
		return fmt.Errorf("report html: template: %w", err)
	}
	return t.Execute(w, d)
}

// HtmlDashboard — the template data (consuming the report source as-is — no double computation).
// It holds the whole report — cards, sparklines, source decomposition,
// and hygiene (down to the observation-gap meta) rendered straight from the source.
type HtmlDashboard struct {
	GeneratedAt string
	Days        int
	Effect      EffectOutput
	Rep         ReportOutput // days, hot_p95, counts, stale, dup_pct, fallback_pct, per-source
	// Go-computed products (no template arithmetic).
	ChipHit, ChipFallback, ChipTop1 deltaChip
	SparkRead, SparkWrite           struct{ Line, Area string }
	BarsRead, BarsWrite, BarsDup    []BarSeg   // daily bars (small multiples — per-series scale, Go-precomputed)
	TrendRows                       []TrendRow // trend table rows (narrative recomposition — v5)
}

// BarSeg — one bar (height % string + date/value tooltip — no-JS native).
type BarSeg struct {
	H     string // "62%" — the ratio vs max (the 1px minimum is the template baseline fallback)
	Title string // "2026-11-05 · 12"
}

// runReportHTML — `flint report --html [--days N] [--out file]`.
// An unspecified --out is report.html (not FLINT_HOME — wherever the user chooses).
func runReportHTML(ctx context.Context, st wire.Store, days int, out string, stdout io.Writer) int {
	// The collection-error exit code follows the same store.MapExit as the JSON path
	// (RunReport) — server_down -> 2, SchemaError -> 3. Before the fix, every step's errors
	// were hardcoded to 1, so the same command's two views diverged in code.
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	// reusing the reporting source — HTML creates no metrics of its own (single source:
	// the same collection chain as runReport). DupPct (G2 reported alongside —
	// METRICS §2) and the sparkline paths (Go-computed — no template arithmetic) are all
	// finished at the source layer.
	rep := ReportOutput{Days: make([]ReportDay, 0, days)}
	for _, step := range []func(context.Context, wire.Store, int, *ReportOutput) error{
		collectDaily, CollectCounts, CollectEffect,
		func(c context.Context, s wire.Store, _ int, o *ReportOutput) error { // hot_p95 (observation meta)
			return collectStats(c, s, o)
		},
	} {
		if err := step(ctx, st, days, &rep); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
	}
	rep.DupPct = DupPct(rep.Days)
	// the rule-diagnostics card source: the same Diagnose as the JSON path
	// (RunReport), rendered from the same ReportOutput.Diagnosis (all three views share one
	// source — HTML creates no metrics of its own, inheriting the single-source principle).
	rep.Diagnosis = Diagnose(rep, knowledge.WriteThresholds(ctx, st),
		knowledge.HotBudget(knowledge.MetaGetKV(ctx, st)))
	d := HtmlDashboard{
		GeneratedAt: timeNowUTC(),
		Days:        days,
		Effect:      rep.Effect,
		Rep:         rep,
	}
	if t := rep.Effect.Trend; t != nil {
		d.ChipHit = ChipFor("recall hit", t.HitPct, false)
		d.ChipFallback = ChipFor("fallback", t.FallbackPct, true)
		d.ChipTop1 = ChipFor("exact top-1", t.Top1Pct, false)
		mk := func(name string, p TrendPoint, invert bool) TrendRow {
			up := p.Delta >= 0
			if invert {
				up = !up
			}
			sign := "+"
			if p.Delta < 0 {
				sign = ""
			}
			word := "improving"
			if !up {
				word = "worsening"
			}
			return TrendRow{Name: name, Cur: p.Cur, Up: up,
				DeltaText: fmt.Sprintf("vs last week %s%d points %s", sign, p.Delta, word)}
		}
		d.TrendRows = []TrendRow{
			mk("recall hit", t.HitPct, false),
			mk("fallback used", t.FallbackPct, true),
			mk("first result exact", t.Top1Pct, false),
		}
	}
	// dynamic scaling: if an outlier day becomes the absolute max,
	// all the rest flatten out. p90-based scaling + excess clipping: bars
	// over the reference are cut at 100% and the tooltip marks 'above reference'
	// (the Grafana percentile-scaling convention).
	mkBars := func(vals []int, dates []string) []BarSeg {
		sorted := append([]int(nil), vals...)
		sort.Ints(sorted)
		// p90 (empty/1-item guard: default 1)
		ref := 1
		if n := len(sorted); n > 0 {
			idx := n * 9 / 10
			if idx >= n {
				idx = n - 1
			}
			ref = sorted[idx]
			if ref < 1 {
				ref = 1
			}
		}
		out := make([]BarSeg, len(vals))
		for i, v := range vals {
			date := ""
			if i < len(dates) {
				date = dates[i]
			}
			title := date + " · " + fmt.Sprint(v)
			h := v * 100 / ref
			if h > 100 {
				h = 100
				title += " · above p90 reference"
			}
			out[i] = BarSeg{H: fmt.Sprintf("%d%%", h), Title: title}
		}
		return out
	}
	var dates []string
	var rd, wr, dp []int
	for _, day := range rep.Days {
		dates = append(dates, day.Date)
		rd, wr, dp = append(rd, day.Read), append(wr, day.Write), append(dp, day.Dup)
	}
	d.BarsRead, d.BarsWrite, d.BarsDup = mkBars(rd, dates), mkBars(wr, dates), mkBars(dp, dates)
	d.SparkRead.Line, d.SparkRead.Area = sparkPaths(rd)
	d.SparkWrite.Line, d.SparkWrite.Area = sparkPaths(wr)
	f, err := os.Create(out)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	defer f.Close()
	if err := RenderDashboard(f, d); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	fmt.Fprintf(stdout, `{"status":"ok","path":%s,"days":%d}`+"\n", QuoteJSON(out), days)
	return 0
}

// timeNowUTC — the test-injection seam (injecting a fixed time).
var timeNowUTC = func() string { return time.Now().Format("Jan 2") }
