// v0190_docs_test.go — v0.19.0 효과 계측 문서 회귀. 버전 동기는
// TestVersionArtifactsSync(v0171 범용화)가 담당 — 여기는 마일스톤 기재만.
package docs

import (
	"strings"
	"testing"
)

// TestVersion0190Milestone — PLAN.md에 v0.19.0 엔트리·테마.
func TestVersion0190Milestone(t *testing.T) {
	doc := docStr(t, "PLAN.md")
	for _, want := range []string{"v0.19.0", "효과 계측"} {
		if !strings.Contains(doc, want) {
			t.Errorf("PLAN.md에 %q 누락", want)
		}
	}
}

// TestMetricsDocContract — docs/METRICS.md 기준 문서 계약: GSM 목표 4종·
// effect 지표명·증거 등급·금지조항이 문서화돼야 한다(지표 정의의 단일
// 소스 — 코드 산출과의 동기는 report 테스트가 담당).
func TestMetricsDocContract(t *testing.T) {
	doc := docStr(t, "METRICS.md")
	for _, want := range []string{
		"목표→신호→지표",      // GSM 원칙
		"G1 회상 명중",         // 목표 4종
		"G2 재발견 저지",
		"G3 자산 건전성",
		"G4 회상 우선 정착",
		"hit_pct", "dup_prevented", "reuse_pct", "dead_pct",
		"read_write_pairs", "pair_pct",
		"Tier 1", "Tier 2", // 증거 등급
		"금지조항",            // 반론 방어
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("METRICS.md에 %q 누락", want)
		}
	}
}
