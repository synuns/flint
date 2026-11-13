// metrics_v1200_test.go — v1.2.0 T6: METRICS.md의 관측 확장 정의가
// 구현(effect 객체·보고 라벨)과 정합하는지, RUNBOOK·PLAN·TUNING의
// v1.2.0 문서 갱신이 이루어졌는지 검증한다.
package docs

import (
	"strings"
	"testing"
)

// TestMetricsV1200Definitions — METRICS.md가 v1.2.0 지표 6종(제로히트
// 분해 2·hot→read 연쇄 1·회상 운명 2·cost 객체 1)을 정의하고, cost
// 라벨·주입 모드 문구가 lib/ops/report.go 상수와 일치하며, G6 절과
// §5 토큰 환산 금지 조항이 유지되는지 검증.
func TestMetricsV1200Definitions(t *testing.T) {
	doc := read(t, "METRICS.md")

	for _, kw := range []string{
		"zero_unowned_pct", "zero_owned_miss_pct",
		"hot_read_chain_pct", "survived_pct", "bad_recall_pct",
		"savings_lower_bound_bytes", "hit_injection_cost_bytes",
	} {
		if !strings.Contains(doc, kw) {
			t.Errorf("METRICS.md에 %s 정의 없음", kw)
		}
	}

	if !strings.Contains(doc, "G6 회상 운명") {
		t.Error("METRICS.md에 G6 회상 운명 절 없음")
	}
	if !strings.Contains(doc, "Tier 2 상관") {
		t.Error("METRICS.md G6/Tier 등급 표기(Tier 2 상관) 없음")
	}
	if !strings.Contains(doc, "토큰으로 환산") {
		t.Error("METRICS.md §5 토큰 환산 금지 조항 유실")
	}

	// cost 라벨·주입 모드 문구는 보고 구현 상수와 정합(단일 소스).
	rep := read(t, "../lib/ops/report.go")
	for _, kw := range []string{"절약 하한 추정", "주입 원가", "always-on", "on-invoke"} {
		if !strings.Contains(doc, kw) {
			t.Errorf("METRICS.md에 cost 문구 %s 없음", kw)
		}
		if !strings.Contains(rep, `"`+kw+`"`) {
			t.Errorf("lib/ops/report.go에 문구 %s 없음 — METRICS와 불일치", kw)
		}
	}
}

// TestRunbookV1200Migration — RUNBOOK.md가 스키마 v10 게이트와 daily
// 플랫 키 전환(구 JSON 폴백 승계)을 각 1줄 이상 기술하는지 검증.
func TestRunbookV1200Migration(t *testing.T) {
	doc := read(t, "RUNBOOK.md")

	if !strings.Contains(doc, "플랫 키") {
		t.Error("RUNBOOK.md에 daily 플랫 키 전환 언급 없음")
	}
	if !strings.Contains(doc, "폴백") || !strings.Contains(doc, "daily:<kind>:<date>") {
		t.Error("RUNBOOK.md에 구 JSON 키 폴백 승계(daily:<kind>:<date>) 언급 없음")
	}
}

// TestPlanV1200Checked — PLAN.md §6에 v1.2.0 완료 체크박스가 있는지 검증.
func TestPlanV1200Checked(t *testing.T) {
	doc := read(t, "PLAN.md")

	if !strings.Contains(doc, "[x] v1.2.0") {
		t.Error("PLAN.md §6에 [x] v1.2.0 엔트리 없음")
	}
}

// TestTuningV1200ZeroOwnedFloor — TUNING.md §2에 zero_owned_floor 행이
// 등재되어 있는지 검증(제로히트 분해 임계 — meta 직접 오버라이드 키).
func TestTuningV1200ZeroOwnedFloor(t *testing.T) {
	doc := read(t, "TUNING.md")

	if !strings.Contains(doc, "zero_owned_floor") {
		t.Error("TUNING.md §2에 zero_owned_floor 행 없음")
	}
	if !strings.Contains(read(t, "../lib/knowledge/read.go"), `"zero_owned_floor"`) {
		t.Error("lib/knowledge/read.go에 zero_owned_floor 키 없음 — TUNING과 불일치")
	}
}
