// v06_docs_test.go — T69 문서 계약 검증. v0.6 신규 커맨드(doctor·report)가
// usageLine·4개 문서(PLAN·TUNING·RUNBOOK·QUICKSTART)에 누락 없이 반영됐는지
// grep 정합 검증한다. 체크리스트 [x] 표기는 마일스톤 종료 시점이므로 여기서는
// 섹션 존재만 확인한다.
package docs

import (
	"strings"
	"testing"
)

// TestDocsV06 — ①usageLine에 doctor·report ②PLAN §14 v0.6 섹션
// ③TUNING §2 임계값 3키 ④RUNBOOK doctor·stale 절 ⑤QUICKSTART 예시.
func TestDocsV06(t *testing.T) {
	// ① usageLine(lib/cli/registry.go의 "usage: flint" 한 줄)에 신규 2커맨드.
	reg := read(t, "../lib/cli/registry.go")
	usage := ""
	for _, line := range strings.Split(reg, "\n") {
		if strings.Contains(line, "usage: flint") {
			usage = line
		}
	}
	if usage == "" {
		t.Fatal("registry.go에 usage: flint 라인 없음")
	}
	for _, cmd := range []string{"doctor", "report"} {
		if !strings.Contains(usage, cmd) {
			t.Errorf("usageLine에 %s 없음", cmd)
		}
	}

	// ② PLAN.md §14에 v0.6 섹션(스펙 §1·§2 체크리스트).
	plan := read(t, "PLAN.md")
	if !strings.Contains(plan, "v0.6") {
		t.Error("PLAN.md에 v0.6 섹션 없음")
	}
	for _, item := range []string{"doctor", "report", "--stale", "stale_days", "read_count", "--source"} {
		if !strings.Contains(plan, item) {
			t.Errorf("PLAN.md v0.6에 %s 누락", item)
		}
	}

	// ③ TUNING.md §2 임계값 표에 3키(기본값 포함).
	tuning := read(t, "TUNING.md")
	for _, kv := range []string{"stale_days", "stall_hot_h", "stall_write_h"} {
		if !strings.Contains(tuning, kv) {
			t.Errorf("TUNING.md에 %s 없음", kv)
		}
	}
	for _, def := range []string{"90", "48", "72"} {
		if !strings.Contains(tuning, def) {
			t.Errorf("TUNING.md에 기본값 %s 없음", def)
		}
	}

	// ④ RUNBOOK.md에 doctor 5검사·maintain --stale 절.
	runbook := read(t, "RUNBOOK.md")
	if !strings.Contains(runbook, "doctor") {
		t.Error("RUNBOOK.md에 doctor 절 없음")
	}
	if !strings.Contains(runbook, "maintain --stale") {
		t.Error("RUNBOOK.md에 maintain --stale 절차 없음")
	}

	// ⑤ QUICKSTART.md 확인 커맨드에 doctor·report 예시.
	quick := read(t, "QUICKSTART.md")
	for _, cmd := range []string{"doctor", "report"} {
		if !strings.Contains(quick, cmd) {
			t.Errorf("QUICKSTART.md에 %s 예시 없음", cmd)
		}
	}
}
