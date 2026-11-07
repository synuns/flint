// v0200_docs_test.go — v0.20.0 효과 계측 정밀화 문서 회귀.
package docs

import (
	"strings"
	"testing"
)

// TestVersion0200Milestone — PLAN.md에 v0.20.0 엔트리·테마·룰링.
func TestVersion0200Milestone(t *testing.T) {
	doc := docStr(t, "PLAN.md")
	for _, want := range []string{"v0.20.0", "효과 계측 정밀화", "기저율 셔플(C) 이연"} {
		if !strings.Contains(doc, want) {
			t.Errorf("PLAN.md에 %q 누락", want)
		}
	}
}

// TestMetricsDocV0200 — METRICS.md v0.20.0 계약: 신지표·계측 원천
// 3테이블·폴백 규칙이 문서화돼야 한다(문서-코드 정합 — 산출 테스트는
// report_v0190_test가 담당).
func TestMetricsDocV0200(t *testing.T) {
	doc := docStr(t, "METRICS.md")
	for _, want := range []string{
		"recall_led_pct", "chain_pct",
		"session_starts", "hot_events",
		"구배포·계측 첫일", // 시간창 폴백 조건 문서화
		"최근 30일 hot 노출 제외",
		"기저율 셔플 이연",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("METRICS.md에 %q 누락", want)
		}
	}
}
