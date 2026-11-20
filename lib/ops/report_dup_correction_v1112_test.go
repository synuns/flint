// report_dup_correction_v1112_test.go — v1.11.2 R52 regression: the
// dup_correction per-source counter recorded as the daily flat key
// (daily:dup_correction:<date>) is exposed on the report day row (the
// dailyFlatFields/ReportDay mapping inherited from dup_hook and siblings).
package ops

import (
	"testing"
)

// TestReportDayDupCorrection — seeding the flat key through the fixture
// store, the report days[0] row carries dup_correction (and the sibling
// per-source dup fields stay intact — additive mapping).
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
	// An unrecorded per-source field stays 0 (the missing-flat-key convention).
	numEq(t, "days[0].dup_mcp", d["dup_mcp"], 0)
}
