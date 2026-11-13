// report_html.go — v0.36.0(E): report --html — 단일 파일 정적 대시보드.
// v0.40.0 T3: lib/cli에서 이동(식별자 대문자화 — 동작·출력 불변).
// 원칙: 임베드 템플릿·프레임워크 무의존·외부 요청 없음(로컬 렌더만 —
// METRICS §4 프라이버시)·수치만(쿼리 본문 없음). Go 템플릿 렌더라
// JS 의존도 0 — 파일 하나를 브라우저로 열면 끝.
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
	"github.com/synuns/flint/lib/wire"
)

// dashboardTmpl — 임베드 템플릿(단일 파일 산출물 — 외부 자산 참조 없음).
//
//go:embed dashboard.tmpl.html
var dashboardTmpl string

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

// ChipFor — Trend 지점 → 칩. nil(빈 창)·0은 미표시.
func ChipFor(label string, p TrendPoint, invert bool) deltaChip {
	if p == (TrendPoint{}) {
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
	// v5 규율(Stripe): 녹=개선만. 악화는 빨강이 아니라 회색 ▼ — 칩 Good는
	// '녹색 표기' 의미로만(회색은 .delta 기본색).
	// v5.1 사람말 사본: "10%포인트 좋아짐" — pp·기호 조합 폐기(컨텍스트
	// 없는 독자 기준 — 방향 화살표는 템플릿 CSS/색이 담당).
	word := "좋아짐"
	if !good {
		word = "나빠짐"
	}
	return deltaChip{Show: true, Text: sign + fmt.Sprint(d) + "%포인트 " + word,
		Good: good, Label: label + " · 지난주 대비"}
}

// TrendRow — 추세 표 1행(Go 사전계산 — 템플릿 산술 금지).
type TrendRow struct {
	Name, DeltaText string
	Cur             int
	Up              bool // 개선 방향(녹) — 회색 ▼와 구분(빨강 금지 규율)
}

// EstTok — rune 수 → 토큰 추정(표시 전용·±30% 명시 — A3 스펙):
// 한글 룬 ≈ 1.1토큰, 기타 ≈ ÷4(cl100k급 근사).
func EstTok(runes int) int {
	return int(float64(runes)*0.55 + float64(runes)*0.45/4)
}

// RenderDashboard — 템플릿 렌더(html/template — Go 측 렌더라 JS 불필요).
func RenderDashboard(w io.Writer, d HtmlDashboard) error {
	t, err := template.New("dashboard").Funcs(template.FuncMap{"EstTok": EstTok}).Parse(dashboardTmpl)
	if err != nil {
		return fmt.Errorf("report html: 템플릿: %w", err)
	}
	return t.Execute(w, d)
}

// HtmlDashboard — 템플릿 데이터(report 원천을 그대로 소비 — 이중 산출 금지).
// v0.36.0 재설계: 전체 report를 담는다 — 카드·스파크라인·소스 분해·
// 위생(관측 갭 메타까지)을 원천 그대로 렌더.
type HtmlDashboard struct {
	GeneratedAt string
	Days        int
	Effect      EffectOutput
	Rep         ReportOutput // days·hot_p95·counts·stale·dup_pct·fallback_pct·소스별
	// v0.36.0 재설계 — Go 계산 산물(템플릿 산술 금지).
	ChipHit, ChipFallback, ChipTop1 deltaChip
	SparkRead, SparkWrite           struct{ Line, Area string }
	BarsRead, BarsWrite, BarsDup    []BarSeg   // 일별 바(스몰 멀티플 — 계열별 스케일·Go 사전계산)
	TrendRows                       []TrendRow // 추세 표 행(서사 재구성 — v5)
}

// BarSeg — 바 1개(높이 % 문자열 + 날짜·값 툴팁 — no-JS 네이티브).
type BarSeg struct {
	H     string // "62%" — max 대비 비율(최소 1px는 템플릿 baseline 폴백)
	Title string // "2026-11-05 · 12"
}

// runReportHTML — `flint report --html [--days N] [--out file]`.
// 미지정 --out은 report.html(FLINT_HOME 아님 — 사용자가 지정한 곳).
func runReportHTML(ctx context.Context, st wire.Store, days int, out string, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	// 보고 원천 재사용 — HTML이 별도 지표를 만들지 않는다(단일 소스:
	// runReport와 동일 수집 체인). v0.36.0 재설계: DupPct(G2 병기 —
	// METRICS §2)·스파크라인 패스(Go 계산 — 템플릿 산술 금지)까지
	// 원천 층에서 마친다.
	rep := ReportOutput{Days: make([]ReportDay, 0, days)}
	for _, step := range []func(context.Context, wire.Store, int, *ReportOutput) error{
		collectDaily, CollectCounts, CollectEffect,
		func(c context.Context, s wire.Store, _ int, o *ReportOutput) error { // hot_p95(관측 메타)
			return collectStats(c, s, o)
		},
	} {
		if err := step(ctx, st, days, &rep); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 1
		}
	}
	rep.DupPct = DupPct(rep.Days)
	// v1.2.0 T5 — 규칙 진단 카드 원천: JSON 경로(RunReport)와 같은
	// Diagnose·같은 ReportOutput.Diagnosis에서 렌더한다(세 뷰 동일 원천 —
	// HTML이 별도 지표를 만들지 않는다, 단일 소스 원칙 승계).
	rep.Diagnosis = Diagnose(rep, knowledge.WriteThresholds(ctx, st),
		knowledge.HotBudget(knowledge.MetaGetKV(ctx, st)))
	d := HtmlDashboard{
		GeneratedAt: timeNowUTC(),
		Days:        days,
		Effect:      rep.Effect,
		Rep:         rep,
	}
	if t := rep.Effect.Trend; t != nil {
		d.ChipHit = ChipFor("회상 명중", t.HitPct, false)
		d.ChipFallback = ChipFor("폴백", t.FallbackPct, true)
		d.ChipTop1 = ChipFor("정합 1위", t.Top1Pct, false)
		mk := func(name string, p TrendPoint, invert bool) TrendRow {
			up := p.Delta >= 0
			if invert {
				up = !up
			}
			sign := "+"
			if p.Delta < 0 {
				sign = ""
			}
			word := "좋아짐"
			if !up {
				word = "나빠짐"
			}
			return TrendRow{Name: name, Cur: p.Cur, Up: up,
				DeltaText: fmt.Sprintf("지난주보다 %s%d%%포인트 %s", sign, p.Delta, word)}
		}
		d.TrendRows = []TrendRow{
			mk("회상 명중", t.HitPct, false),
			mk("폴백 경유", t.FallbackPct, true),
			mk("첫 결과 정답", t.Top1Pct, false),
		}
	}
	// v8.1(사용자 지시 — 동적 스케일): 이상치 하루가 절대 최댓값 기준이
	// 되면 나머지가 전부 납작해진다. p90 기준 스케일 + 초과분 클리핑:
	// 초과 막대는 100%에서 자르고 툴팁에 '기준 초과' 표시(Grafana
	// percentile 스케일 관례).
	mkBars := func(vals []int, dates []string) []BarSeg {
		sorted := append([]int(nil), vals...)
		sort.Ints(sorted)
		// p90(빈/1건 가드: 기본 1)
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
				title += " · 기준(p90) 초과"
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

// timeNowUTC — 테스트 주입 접합면(고정 시각 주입).
var timeNowUTC = func() string { return time.Now().Format("1월 2일") }
