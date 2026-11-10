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
	"time"
)

// dashboardTmpl — 임베드 템플릿(단일 파일 산출물 — 외부 자산 참조 없음).
//
//go:embed dashboard.tmpl.html
var dashboardTmpl string

// renderDashboard — 템플릿 렌더(html/template — Go 측 렌더라 JS 불필요).
func renderDashboard(w io.Writer, d htmlDashboard) error {
	t, err := template.New("dashboard").Parse(dashboardTmpl)
	if err != nil {
		return fmt.Errorf("report html: 템플릿: %w", err)
	}
	return t.Execute(w, d)
}

// htmlDashboard — 템플릿 데이터(report 원천을 그대로 소비 — 이중 산출 금지).
type htmlDashboard struct {
	GeneratedAt string
	Days        int
	Effect      effectOutput
	EffectDays  []reportDay
}

// runReportHTML — `flint report --html [--days N] [--out file]`.
// 미지정 --out은 report.html(FLINT_HOME 아님 — 사용자가 지정한 곳).
func runReportHTML(ctx context.Context, st Store, days int, out string, stdout io.Writer) int {
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	// 보고 원천 재사용 — HTML이 별도 지표를 만들지 않는다(단일 소스:
	// runReport와 동일 수집 체인).
	rep := reportOutput{Days: make([]reportDay, 0, days)}
	for _, step := range []func(context.Context, Store, int, *reportOutput) error{
		collectDaily, collectCounts, collectEffect,
	} {
		if err := step(ctx, st, days, &rep); err != nil {
			fmt.Fprintln(stdout, errLine(err))
			return 1
		}
	}
	d := htmlDashboard{
		GeneratedAt: timeNowUTC(),
		Days:        days,
		Effect:      rep.Effect,
		EffectDays:  rep.Days,
	}
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
