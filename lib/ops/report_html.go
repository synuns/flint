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

//go:embed dashboard.tmpl.html
var dashboardTmpl string

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
		max++
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

type deltaChip struct {
	Show  bool
	Text  string
	Good  bool
	Label string
}

func ChipFor(label string, p TrendPoint, invert bool) deltaChip {
	if p == (TrendPoint{}) {
		return deltaChip{}
	}
	d := p.Delta
	good := d >= 0
	if invert {
		good = !good
	}
	sign := "+"
	if d < 0 {
		sign = ""
	}

	word := "improving"
	if !good {
		word = "worsening"
	}
	return deltaChip{Show: true, Text: sign + fmt.Sprint(d) + " points " + word,
		Good: good, Label: label + " · vs last week"}
}

type TrendRow struct {
	Name, DeltaText string
	Cur             int
	Up              bool
}

func EstTok(runes int) int {
	return int(float64(runes)*0.55 + float64(runes)*0.45/4)
}

func RenderDashboard(w io.Writer, d HtmlDashboard) error {
	t, err := template.New("dashboard").Funcs(template.FuncMap{"EstTok": EstTok}).Parse(dashboardTmpl)
	if err != nil {
		return fmt.Errorf("report html: template: %w", err)
	}
	return t.Execute(w, d)
}

type HtmlDashboard struct {
	GeneratedAt string
	Days        int
	Effect      EffectOutput
	Rep         ReportOutput

	ChipHit, ChipFallback, ChipTop1 deltaChip
	SparkRead, SparkWrite           struct{ Line, Area string }
	BarsRead, BarsWrite, BarsDup    []BarSeg
	TrendRows                       []TrendRow
}

type BarSeg struct {
	H     string
	Title string
}

func runReportHTML(ctx context.Context, st wire.Store, days int, out string, stdout io.Writer) int {

	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}

	rep := ReportOutput{Days: make([]ReportDay, 0, days)}
	for _, step := range []func(context.Context, wire.Store, int, *ReportOutput) error{
		collectDaily, CollectCounts, CollectEffect,
		func(c context.Context, s wire.Store, _ int, o *ReportOutput) error {
			return collectStats(c, s, o)
		},
	} {
		if err := step(ctx, st, days, &rep); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
	}
	rep.DupPct = DupPct(rep.Days)

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

	mkBars := func(vals []int, dates []string) []BarSeg {
		sorted := append([]int(nil), vals...)
		sort.Ints(sorted)

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

var timeNowUTC = func() string { return time.Now().Format("Jan 2") }
