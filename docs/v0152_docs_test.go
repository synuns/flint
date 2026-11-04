package docs

import (
	"os"
	"strings"
	"testing"
)

func docStrV0152(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlanRecordsV0152(t *testing.T) {
	if !strings.Contains(docStrV0152(t, "PLAN.md"), "v0.15.2") {
		t.Error("PLAN.md v0.15.2 마일스톤 누락")
	}
}
