// report_html.go — v0.36.0(E): report --html — 단일 파일 정적 대시보드.
// 원칙: 임베드 템플릿·프레임워크 무의존·외부 요청 없음(로컬 렌더만 —
// METRICS §4 프라이버시)·수치만(쿼리 본문 없음). Go 템플릿 렌더라
// JS 의존도 0 — 파일 하나를 브라우저로 열면 끝.
package cli

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"os"
	"strings"
	"time"
)

// dashboardTmpl — 임베드 템플릿(단일 파일 산출물 — 외부 자산 참조 없음).
//
//go:embed dashboard.tmpl.html
var dashboardTmpl string

// baseCSS — 클래스리스 베이스(Water.css v2, MIT — vendor/water.min.css).
// 컴포넌트 라이브러리 기반 설계: 상류 유지보수 토큰·다크모드를 임베드로
// 계승하고 외부 요청은 0을 유지한다(단일 파일·로컬 렌더 원칙).
//
//go:embed vendor/water.min.css
var baseCSS string

// sparkPaths — 값 슬라이스로 (line, area) SVG path d 문자열(Go 계산 —
// 템플릿 산술 금지 원칙). 평평 가드(min==max→max+1), 점 1개는 빈 값.
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
		max++ // 평평 가드
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

// deltaChip — 델타 칩 데이터(방향×긍정 — 색 단독 금지: 부호+화살표).
type deltaChip struct {
	Show  bool
	Text  string // "▲ +7pp"
	Good  bool
	Label string // 지표명(title용)
}

// chipFor — Trend 지점 → 칩. nil(빈 창)·0은 미표시.
func chipFor(label string, p trendPoint, invert bool) deltaChip {
	if p == (trendPoint{}) {
		return deltaChip{}
	}
	d := p.Delta
	good := d >= 0
	if invert { // fallback: 상승=bad
		good = !good
	}
	sign := "+"
	if d < 0 {
		sign = ""
	}
	arrow := "▲"
	if d < 0 {
		arrow = "▼"
	}
	return deltaChip{Show: true, Text: arrow + " " + sign + fmt.Sprint(d) + "pp",
		Good: good, Label: label + " · 전 7일 대비"}
}

// renderDashboard — 템플릿 렌더(html/template — Go 측 렌더라 JS 불필요).
func renderDashboard(w io.Writer, d htmlDashboard) error {
	t, err := template.New("dashboard").Parse(strings.Replace(dashboardTmpl, "/*__BASE_CSS__*/", baseCSS, 1))
	if err != nil {
		return fmt.Errorf("report html: 템플릿: %w", err)
	}
	return t.Execute(w, d)
}

// htmlDashboard — 템플릿 데이터(report 원천을 그대로 소비 — 이중 산출 금지).
// v0.36.0 재설계: 전체 report를 담는다 — 카드·스파크라인·소스 분해·
// 위생(관측 갭 메타까지)을 원천 그대로 렌더.
type htmlDashboard struct {
	GeneratedAt string
	Days        int
	Effect      effectOutput
	Rep         reportOutput // days·hot_p95·counts·stale·dup_pct·fallback_pct·소스별
	// v0.36.0 재설계 — Go 계산 산물(템플릿 산술 금지).
	ChipHit, ChipFallback, ChipTop1 deltaChip
	SparkRead, SparkWrite           struct{ Line, Area string }
	BarsRead, BarsWrite, BarsDup    []barSeg // 일별 바(스몰 멀티플 — 계열별 스케일·Go 사전계산)
}

// barSeg — 바 1개(높이 % 문자열 + 날짜·값 툴팁 — no-JS 네이티브).
type barSeg struct {
	H     string // "62%" — max 대비 비율(최소 1px는 템플릿 baseline 폴백)
	Title string // "2026-11-05 · 12"
}

// runReportHTML — `flint report --html [--days N] [--out file]`.
// 미지정 --out은 report.html(FLINT_HOME 아님 — 사용자가 지정한 곳).
func runReportHTML(ctx context.Context, st Store, days int, out string, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	// 보고 원천 재사용 — HTML이 별도 지표를 만들지 않는다(단일 소스:
	// runReport와 동일 수집 체인). v0.36.0 재설계: DupPct(G2 병기 —
	// METRICS §2)·스파크라인 패스(Go 계산 — 템플릿 산술 금지)까지
	// 원천 층에서 마친다.
	rep := reportOutput{Days: make([]reportDay, 0, days)}
	for _, step := range []func(context.Context, Store, int, *reportOutput) error{
		collectDaily, collectCounts, collectEffect,
	} {
		if err := step(ctx, st, days, &rep); err != nil {
			fmt.Fprintln(stdout, errLine(err))
			return 1
		}
	}
	rep.DupPct = dupPct(rep.Days)
	d := htmlDashboard{
		GeneratedAt: timeNowUTC(),
		Days:        days,
		Effect:      rep.Effect,
		Rep:         rep,
	}
	if rep.Effect.Trend != nil {
		d.ChipHit = chipFor("회상 명중", rep.Effect.Trend.HitPct, false)
		d.ChipFallback = chipFor("폴백", rep.Effect.Trend.FallbackPct, true)
		d.ChipTop1 = chipFor("정합 1위", rep.Effect.Trend.Top1Pct, false)
	}
	mkBars := func(vals []int, dates []string) []barSeg {
		max := 1
		for _, v := range vals {
			if v > max {
				max = v
			}
		}
		out := make([]barSeg, len(vals))
		for i, v := range vals {
			date := ""
			if i < len(dates) {
				date = dates[i]
			}
			out[i] = barSeg{H: fmt.Sprintf("%d%%", v*100/max), Title: date + " · " + fmt.Sprint(v)}
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
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	defer f.Close()
	if err := renderDashboard(f, d); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	fmt.Fprintf(stdout, `{"status":"ok","path":%s,"days":%d}`+"\n", quoteJSON(out), days)
	return 0
}

// timeNowUTC — 테스트 주입 접합면(고정 시각 주입).
var timeNowUTC = func() string { return time.Now().UTC().Format(time.RFC3339) }
