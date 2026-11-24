package doclint

import (
	"regexp"
	"strings"
	"testing"
)

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

	if !strings.Contains(doc, "G6 recall fate") {
		t.Error("METRICS.md에 G6 recall fate 절 없음")
	}
	if !strings.Contains(doc, "Tier 2 correlation") {
		t.Error("METRICS.md G6/Tier 등급 표기(Tier 2 correlation) 없음")
	}
	if !strings.Contains(doc, "converted into tokens") {
		t.Error("METRICS.md §5 토큰 환산 금지 조항 유실")
	}

	rep := read(t, "../lib/ops/report.go")
	for _, name := range []string{"CostSavingsLabel", "CostHitLabel"} {
		m := regexp.MustCompile(name + `\s*=\s*"([^"]+)"`).FindStringSubmatch(rep)
		if m == nil {
			t.Fatalf("lib/ops/report.go에 %s 상수 없음", name)
		}
		if !strings.Contains(doc, m[1]) {
			t.Errorf("METRICS.md에 cost 라벨 %q 없음 — report.go와 불일치", m[1])
		}
	}
	for _, kw := range []string{"always-on", "on-invoke"} {
		if !strings.Contains(doc, kw) {
			t.Errorf("METRICS.md에 cost 문구 %s 없음", kw)
		}
		if !strings.Contains(rep, `"`+kw+`"`) {
			t.Errorf("lib/ops/report.go에 문구 %s 없음 — METRICS와 불일치", kw)
		}
	}
}

func TestMetricsDefinitionGate(t *testing.T) {
	doc := read(t, "METRICS.md")

	if !strings.Contains(doc, "**zero_hit_unowned_pct**") {
		t.Error("METRICS.md에 zero_hit_unowned_pct 정의 행 없음 — abstention 지표")
	}
	if !strings.Contains(doc, "zero-hit") && !strings.Contains(doc, "zero_hit") {
		t.Error("METRICS.md zero_hit_unowned_pct 정의에 분모(zero-hit rows) 서술 없음")
	}
	if !strings.Contains(doc, "**hot_bytes_success_corr**") {
		t.Error("METRICS.md에 hot_bytes_success_corr 상관 계측 정의 행 없음 — 주간 rollup 소비")
	}
	corrSection := doc[strings.Index(doc, "**hot_bytes_success_corr**"):]
	if len(corrSection) == 0 || !strings.Contains(corrSection[:600], "weekly") {
		t.Error("hot_bytes_success_corr 정의에 주간 rollup 소비 서술 없음")
	}
	if !strings.Contains(corrSection[:600], "Tier 2") {
		t.Error("hot_bytes_success_corr 정의에 Tier 2 correlation 등급 표기 없음")
	}

	rep := read(t, "../lib/ops/report.go")
	if !strings.Contains(rep, `zero_hit_unowned_pct`) {
		t.Error("lib/ops/report.go에 zero_hit_unowned_pct JSON 태그 없음 — METRICS와 불일치")
	}
}

func TestRunbookV1200Migration(t *testing.T) {
	doc := read(t, "RUNBOOK.md")

	if !strings.Contains(doc, "플랫 키") {
		t.Error("RUNBOOK.md에 daily 플랫 키 전환 언급 없음")
	}
	if !strings.Contains(doc, "폴백") || !strings.Contains(doc, "daily:<kind>:<date>") {
		t.Error("RUNBOOK.md에 구 JSON 키 폴백 승계(daily:<kind>:<date>) 언급 없음")
	}
}

func TestPlanV1200Checked(t *testing.T) {
	doc := read(t, "PLAN.md")

	if !strings.Contains(doc, "[x] v1.2.0") {
		t.Error("PLAN.md §6에 [x] v1.2.0 엔트리 없음")
	}
}

func TestTuningV1200ZeroOwnedFloor(t *testing.T) {
	doc := read(t, "TUNING.md")

	if !strings.Contains(doc, "zero_owned_floor") {
		t.Error("TUNING.md §2에 zero_owned_floor 행 없음")
	}
	if !strings.Contains(read(t, "../lib/knowledge/read.go"), `"zero_owned_floor"`) {
		t.Error("lib/knowledge/read.go에 zero_owned_floor 키 없음 — TUNING과 불일치")
	}
}
