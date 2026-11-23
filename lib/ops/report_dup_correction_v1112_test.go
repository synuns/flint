package ops

import (
	"testing"
)

func TestReportDayDupCorrection(t *testing.T) {
	_, today := v1200Today()
	s := &v1200Store{daily: map[string]string{
		"daily:dup_correction:" + today: "5",
		"daily:dup_hook:" + today:       "2",
	}}
	m := runReportV1200(t, s, "--days", "1")
	days, ok := m["days"].([]any)
	if !ok || len(days) != 1 {
		t.Fatalf("days=%v, want 1 row", m["days"])
	}
	d := days[0].(map[string]any)
	numEq(t, "days[0].dup_correction", d["dup_correction"], 5)
	numEq(t, "days[0].dup_hook", d["dup_hook"], 2)

	numEq(t, "days[0].dup_mcp", d["dup_mcp"], 0)
}
