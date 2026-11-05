package docs

import (
	"strings"
	"testing"
)

func TestPlanRecordsV0161(t *testing.T) {
	if !strings.Contains(docStr(t, "PLAN.md"), "v0.16.1") {
		t.Error("PLAN.md v0.16.1 마일스톤 누락")
	}
}

// TestBadgeIsV0161 — v0.16.2 배지 전환으로 삭제(선례 승계).
// 배지는 TestBadgeIsV0162(v0162_docs_test.go)가 대체.

func TestReadmePathRequirement(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		s := docStr(t, p)
		if !strings.Contains(s, "PATH") {
			t.Errorf("%s 바이너리 PATH 요구 미기록", p)
		}
	}
}
