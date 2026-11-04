package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0140(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlanRecordsV0140(t *testing.T) {
	if !strings.Contains(docStrV0140(t, "PLAN.md"), "v0.14.0") {
		t.Error("PLAN.md v0.14.0 마일스톤 누락")
	}
}

func TestTuningRecordsZeroNear(t *testing.T) {
	s := docStrV0140(t, "TUNING.md")
	if !strings.Contains(s, "zero_near") {
		t.Error("TUNING.md zero_near 미기록")
	}
}

func TestBadgeIsV0140(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if !strings.Contains(docStrV0140(t, p), "release-v0.14.0") {
			t.Errorf("%s 배지 v0.14.0 미갱신", p)
		}
	}
}

func TestReadmeSilenceContract(t *testing.T) {
	for _, p := range []string{"../README.md", "../README.ko.md"} {
		if strings.Contains(docStrV0140(t, p), "still hits nothing") ||
			strings.Contains(docStrV0140(t, p), "근접 후보 제안") {
			t.Errorf("%s 제안 문단 잔존", p)
		}
	}
}
