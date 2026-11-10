// report_html_test.go — v0.36.0(E): 정적 대시보드 — 합성 데이터 렌더·
// 자기완결성(외부 참조 없음)·플래그 배선.
package cli

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestRenderDashboardSynthetic — 합성 픽스처 렌더: GSM 4카드·품질·
// 추세·일별 표 전부 존재(공개 산출물 원칙 — 합성 데모로 실증).
func TestRenderDashboardSynthetic(t *testing.T) {
	d := htmlDashboard{
		GeneratedAt: "2026-01-01T00:00:00Z",
		Days:        14,
		Effect: effectOutput{
			HitPct: 90, NearPct: 5, FallbackPct: 10, DupPrevented: 42,
			ReusePct: 70, DeadPct: 2, ReadEventsN: 30,
			ReadWritePairs: 12, PairPct: 40, RecallLedPct: 60, ChainPct: 55,
			ExactPct: 50, Top1Pct: 80, MRR: 0.9,
			Trend: &trendOutput{
				HitPct:      trendPoint{Prev: 80, Cur: 90, Delta: 10},
				FallbackPct: trendPoint{Prev: 20, Cur: 10, Delta: -10},
				Top1Pct:     trendPoint{Prev: 70, Cur: 80, Delta: 10},
			},
		},
		EffectDays: []reportDay{
			{Date: "2026-01-01", Read: 5, Write: 2, Dup: 3},
			{Date: "2026-01-02", Read: 3, Write: 1, Dup: 1, ReadZero: 1},
		},
	}
	var b strings.Builder
	if err := renderDashboard(&b, d); err != nil {
		t.Fatalf("렌더: %v", err)
	}
	s := b.String()
	for _, want := range []string{
		"G1 회상 명중", "G2 재발견 저지", "G3 자산 건전성", "G4 회상 우선",
		"hit 품질", "7일 추세", "일별 활동", "2026-01-02",
		"+10", "-10", "docs/METRICS.md",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("요소 %q 누락", want)
		}
	}
	// 자기완결 — 외부 참조 금지(로컬 렌더 원칙).
	if strings.Contains(s, "http:") || strings.Contains(s, "https:") || strings.Contains(s, "src=") {
		t.Error("외부 참조 존재 — 정적 단일 파일 원칙 위반")
	}
}

// TestReportHTMLFlagWiring — --html --out 플래그 → 파일 생성·한 줄 JSON.
func TestReportHTMLFlagWiring(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil // 전 수집 폴백(0값 대시보드 — 렌더 경로 검증)
	}}
	out := t.TempDir() + "/dash.html"
	var res strings.Builder
	code := runReport(context.Background(), st, []string{"--html", "--out", out, "--days", "7"}, nil, &res)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, res.String())
	}
	if !strings.Contains(res.String(), `"status":"ok"`) {
		t.Errorf("출력: %s", res.String())
	}
	b, err := os.ReadFile(out)
	if err != nil || !strings.Contains(string(b), "flint 효과 대시보드") {
		t.Errorf("파일 미생성/빈 렌더: %v %d bytes", err, len(b))
	}
	// --out 확장자 자동 보정(.html 없으면).
	out2 := t.TempDir() + "/plain"
	res.Reset()
	if code := runReport(context.Background(), st, []string{"--html", "--out", out2}, nil, &res); code != 0 {
		t.Fatalf("보정 exit=%d", code)
	}
	if _, err := os.Stat(out2 + ".html"); err != nil {
		t.Errorf("확장자 보정 실패: %v", err)
	}
}
