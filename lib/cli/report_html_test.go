// report_html_test.go — v0.36.0(E) 재설계: 정적 대시보드 계약 — 합성
// 렌더 구조·자기완결·성능 상한(≤32KB)·델타칩 조건부·바 툴팁·반응형.
package cli

import (
	"context"
	"os"
	"strings"
	"testing"
)

// synthDash — 스펙 전 요소를 담은 합성 픽스처(공개 산출물 원칙).
func synthDash() htmlDashboard {
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
	d.ChipHit = chipFor("회상 명중", d.Effect.Trend.HitPct, false)
	d.ChipFallback = chipFor("폴백", d.Effect.Trend.FallbackPct, true)
	d.ChipTop1 = chipFor("정합 1위", d.Effect.Trend.Top1Pct, false)
	d.SparkRead.Line, d.SparkRead.Area = sparkPaths([]int{1, 5, 3})
	var rd, wr, dp []int
	for _, day := range d.Rep.Days {
		rd, wr, dp = append(rd, day.Read), append(wr, day.Write), append(dp, day.Dup)
	}
	max := func(v []int) int {
		m := 1
		for _, x := range v {
			if x > m {
				m = x
			}
		}
		return m
	}
	mk := func(v []int, dates []string) []barSeg {
		out := make([]barSeg, len(v))
		for i, x := range v {
			out[i] = barSeg{H: "50%", Title: dates[i] + " · " + string(rune('0'+x))}
		}
		return out
	}
	_ = max
	d.BarsRead, d.BarsWrite, d.BarsDup = mk(rd, []string{"2026-01-01", "2026-01-02"}), mk(wr, []string{"2026-01-01", "2026-01-02"}), mk(dp, []string{"2026-01-01", "2026-01-02"})
	return d
}

// TestRenderDashboardSpec — 스펙 전 요소 렌더 + 자기완결 + 성능 상한.
func TestRenderDashboardSpec(t *testing.T) {
	var b strings.Builder
	if err := renderDashboard(&b, synthDash()); err != nil {
		t.Fatalf("렌더: %v", err)
	}
	s := b.String()
	for _, want := range []string{
		"G1 회상 명중", "class=\"card hero\"", "G2 재발견 저지", "G3 자산 건전성", "G4 회상 우선",
		"hit 품질", "dup_pct 84%", // G2 병기(스펙 A-5)
		"n=30",           // 표본 배지(A-1)
		"▲", "▼", "10pp", // 델타칩 — 부호+화살표(A-4, html/template는 +를 &#43; 이스케이프)
		"7일 추세", "일별 흐름", "지식 재고", // A-2·A-3
		"프로파일 판정",                              // A-6
		"vector-effect",                        // 스파크라인(B)
		"prefers-color-scheme", "@media print", // 다크·인쇄
		"--background",      // Water.css 베이스 임베드(컴포넌트 라이브러리 기반)
		"<title>2026-01-01", // 바 네이티브 툴팁
		"docs/METRICS.md",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("요소 %q 누락", want)
		}
	}
	if strings.Contains(s, "<script") {
		t.Error("script 태그 존재 — JS 무의존 위반")
	}
	// 자기완결 — 실제 리소스 로딩(src=/href=의 http)만 금지. SVG xmlns
	// 네임스페이스 식별자(w3.org)는 요청이 아니라 문자열이다.
	if strings.Contains(s, "src=\"http") || strings.Contains(s, "href=\"http") || strings.Contains(s, "@import") {
		t.Error("외부 리소스 로딩 존재 — 자기완결 위반")
	}
	if !strings.HasPrefix(s[strings.Index(s, "<head>")+6:strings.Index(s, "<head>")+30], "\n<meta charset") {
		t.Error("charset가 head 첫 요소 아님")
	}
	// 예산 48KB(v0.36.0 재설계: Water.css 베이스 23KB + 컴포넌트층·데이터).
	if n := len(s); n > 48*1024 {
		t.Errorf("렌더 크기 %d bytes — 48KB 예산 초과", n)
	}
}

// TestChipInvertAndNil — 폴백 칩 방향 반전·빈 Trend 미표시.
func TestChipInvertAndNil(t *testing.T) {
	fb := chipFor("폴백", trendPoint{Prev: 20, Cur: 30, Delta: 10}, true) // 상승=bad
	if !fb.Show || fb.Good {
		t.Errorf("폴백 상승 = %+v, want bad", fb)
	}
	hit := chipFor("명중", trendPoint{Prev: 80, Cur: 90, Delta: 10}, false)
	if !hit.Good {
		t.Errorf("명중 상승 = %+v, want good", hit)
	}
	if none := chipFor("x", trendPoint{}, false); none.Show {
		t.Error("빈 Trend 칩 렌더")
	}
}

// TestSparkPathsFlatGuard — 평평(모든 값 동일)·점 1개 가드.
func TestSparkPathsFlatGuard(t *testing.T) {
	l, a := sparkPaths([]int{5, 5, 5})
	if l == "" || a == "" {
		t.Fatal("평평 가드 미작동 — 빈 패스")
	}
	if l0, _ := sparkPaths([]int{5}); l0 != "" {
		t.Error("점 1개에 패스 생성")
	}
}

// TestReportHTMLFlagWiring — 플래그 배선·파일 생성·확장자 보정.
func TestReportHTMLFlagWiring(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) { return nil, nil }}
	out := t.TempDir() + "/dash.html"
	var res strings.Builder
	if code := runReport(context.Background(), st, []string{"--html", "--out", out, "--days", "7"}, nil, &res); code != 0 {
		t.Fatalf("exit=%d: %s", code, res.String())
	}
	if !strings.Contains(res.String(), `"status":"ok"`) {
		t.Errorf("출력: %s", res.String())
	}
	if b, err := os.ReadFile(out); err != nil || !strings.Contains(string(b), "flint 효과 대시보드") {
		t.Errorf("파일 미생성: %v", err)
	}
	out2 := t.TempDir() + "/plain"
	res.Reset()
	if code := runReport(context.Background(), st, []string{"--html", "--out", out2}, nil, &res); code != 0 {
		t.Fatalf("보정 exit=%d", code)
	}
	if _, err := os.Stat(out2 + ".html"); err != nil {
		t.Errorf("확장자 보정 실패: %v", err)
	}
}
