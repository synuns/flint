package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0151(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlanRecordsV0151(t *testing.T) {
	if !strings.Contains(docStrV0151(t, "PLAN.md"), "v0.15.1") {
		t.Error("PLAN.md v0.15.1 마일스톤 누락")
	}
}

// TestBadgeIsV0151 — v0.15.2 배지 전환으로 삭제(선례 승계).
