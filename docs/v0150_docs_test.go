package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0150(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlanRecordsV0150(t *testing.T) {
	if !strings.Contains(docStrV0150(t, "PLAN.md"), "v0.15.0") {
		t.Error("PLAN.md v0.15.0 마일스톤 누락")
	}
}

func TestTuningRecordsWindowed(t *testing.T) {
	s := docStrV0150(t, "TUNING.md")
	if !strings.Contains(s, "read_zero_near") || !strings.Contains(s, "30") {
		t.Error("TUNING.md 기간화 지표·임계(30) 미기록")
	}
}

func TestBadgeIsV0150(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStrV0150(t, p), "release-v0.15.0") {
			t.Errorf("%s 배지 v0.15.0 미갱신", p)
		}
	}
}

func TestReadmeExitTable(t *testing.T) {
	cases := []struct{ path, expr string }{
		{"../README.md", "invalid `read` arguments"},
		{"../README.ko.md", "read 인자 규칙 위반"},
	}
	for _, c := range cases {
		s := docStrV0150(t, c.path)
		if !strings.Contains(s, "exit 3") || !strings.Contains(s, c.expr) {
			t.Errorf("%s exit 표(exit 3·%s) 누락", c.path, c.expr)
		}
	}
}
