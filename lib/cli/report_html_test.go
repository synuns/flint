package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

func synthDash5() htmlDashboard {
	d := htmlDashboard{
		GeneratedAt: "2026-01-01T00:00:00Z",
		Days:        14,
		Effect: effectOutput{
			HitPct: 90, NearPct: 5, FallbackPct: 10, DupPrevented: 42,
			ReusePct: 70, DeadPct: 2, ReadEventsN: 30,
			ReadWritePairs: 12, PairPct: 40, RecallLedPct: 60, ChainPct: 55,
			ExactPct: 50, Top1Pct: 80, MRR: 0.9,
			ProfileHint: "normal 유지 근거 충분(회상 선도 다수)",
			Trend: &trendOutput{
				HitPct:      trendPoint{Prev: 80, Cur: 90, Delta: 10},
				FallbackPct: trendPoint{Prev: 20, Cur: 10, Delta: -10},
				Top1Pct:     trendPoint{Prev: 70, Cur: 80, Delta: 10},
			},
		},
	}
	d.Rep.Days = []reportDay{
		{Date: "2026-01-01", Read: 5, Write: 2, Dup: 3, Hook: 1, Manual: 1},
		{Date: "2026-01-02", Read: 3, Write: 1, Dup: 1, ReadZero: 1},
	}
	d.Rep.Counts.Active, d.Rep.Counts.Pending, d.Rep.Counts.Invalidated = 55, 2, 11
	d.Rep.Stale.Days90, d.Rep.Stale.NeverRead = 3, 1
	d.Rep.DupPct = 84
	d.Rep.HotP95 = 1163
	d.ChipHit = chipFor("recall hit", d.Effect.Trend.HitPct, false)
	d.ChipTop1 = chipFor("exact top-1", d.Effect.Trend.Top1Pct, false)
	mk := func(name string, p trendPoint, invert bool) trendRow {
		up := p.Delta >= 0
		if invert {
			up = !up
		}
		sign := "+"
		if p.Delta < 0 {
			sign = ""
		}
		return trendRow{Name: name, Cur: p.Cur, Up: up, DeltaText: "▲ " + sign + fmt.Sprint(p.Delta) + "pp"}
	}
	d.TrendRows = []trendRow{mk("recall hit", d.Effect.Trend.HitPct, false), mk("via fallback", d.Effect.Trend.FallbackPct, true)}
	max := 5
	seg2 := []barSeg{{H: "100%", Title: "2026-01-01 · 5"}, {H: "60%", Title: "2026-01-02 · 3"}}
	d.BarsRead, d.BarsWrite, d.BarsDup = seg2, seg2, seg2
	_ = max
	return d
}

func TestRenderDashboardV5(t *testing.T) {
	var b strings.Builder
	if err := renderDashboard(&b, synthDash5()); err != nil {
		t.Fatalf("렌더: %v", err)
	}
	s := b.String()
	for _, want := range []string{
		"recall hit", `class="panel"`, `class="kpis"`,
		"7-day trend", `class="trow"`,
		"memory inventory", `class="inv"`,
		"dup blocked", "first result exact", "7-day trend",
		"points", "tokens",
		"flint report",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("요소 %q 누락", want)
		}
	}

	for _, jargon := range []string{"G1 ", "G2 ", "G3 ", "G4 ", "n=", "METRICS §", "fallback ", "dup_pct", "recall_led"} {
		if strings.Contains(s, jargon) {
			t.Errorf("내부 jargon %q 잔존 — 컨텍스트 없는 독자 기준 위반", jargon)
		}
	}

	for _, banned := range []string{`class="card`, `class="grid"`, `class="inv .cell"`} {
		if strings.Contains(s, banned) {
			t.Errorf("slop 패턴 %q 존재", banned)
		}
	}
	if strings.Contains(s, "<script") {
		t.Error("script 존재")
	}
	if strings.Contains(s, `src="http`) || strings.Contains(s, `href="http`) {
		t.Error("외부 로딩 존재")
	}
	if n := len(s); n > 128*1024 {
		t.Errorf("렌더 %d bytes 초과", n)
	}
}

func TestEstTok(t *testing.T) {
	if got := EstTok(1000); got < 300 || got > 900 {
		t.Errorf("EstTok(1000)=%d — 합리 범위 이탈", got)
	}
}

func TestChipDiscipline(t *testing.T) {
	down := chipFor("hit", trendPoint{Prev: 90, Cur: 80, Delta: -10}, false)
	if down.Show && down.Good {
		t.Error("감소 칩이 Good — 녹=개선만 규율 위반")
	}
}

func TestReportHTMLFlagWiring(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	out := t.TempDir() + "/dash.html"
	var res strings.Builder
	if code := runReport(context.Background(), st, []string{"--html", "--out", out, "--days", "7"}, nil, &res); code != 0 {
		t.Fatalf("exit=%d: %s", code, res.String())
	}
	if b, err := os.ReadFile(out); err != nil || !strings.Contains(string(b), "flint usage report") {
		t.Errorf("파일: %v", err)
	}
	out2 := t.TempDir() + "/plain"
	res.Reset()
	if code := runReport(context.Background(), st, []string{"--html", "--out", out2}, nil, &res); code != 0 {
		t.Fatalf("보정 exit=%d", code)
	}
	if _, err := os.Stat(out2 + ".html"); err != nil {
		t.Errorf("확장자 보정: %v", err)
	}
}
