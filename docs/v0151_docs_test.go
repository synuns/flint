package docs

import (
	"strings"
	"testing"
)

func TestPlanRecordsV0151(t *testing.T) {
	if !strings.Contains(docStr(t, "PLAN.md"), "v0.15.1") {
		t.Error("PLAN.md v0.15.1 마일스톤 누락")
	}
}

// TestBadgeIsV0151 — v0.15.2 배지 전환으로 삭제(선례 승계).
