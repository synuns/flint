package docs

import (
	"strings"
	"testing"
)

func TestPlanRecordsV0152(t *testing.T) {
	if !strings.Contains(docStr(t, "PLAN.md"), "v0.15.2") {
		t.Error("PLAN.md v0.15.2 마일스톤 누락")
	}
}
