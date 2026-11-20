// report_dup_gap_v1120_test.go — v1.12.0 T1 regression: the dup_gap
// per-source counter recorded as the daily flat key (daily:dup_gap:<date>)
// is exposed on the report day row (the dailyFlatFields/ReportDay mapping
// inherited from dup_correction and siblings — the dup-family field
// position preserved).
package ops

import (
	"testing"
)

// TestReportDayDupGap — seeding the flat key through the fixture store,
// the report days[0] row carries dup_gap (and the sibling per-source dup
// fields stay intact — additive mapping).
func TestReportDayDupGap(t *testing.T) {
	_, today := v1200Today()
	s := &v1200Store{daily: map[string]string{
		"daily:dup_gap:" + today:        "3",
		"daily:dup_correction:" + today: "5",
		"daily:dup_hook:" + today:       "2",
	}}
	m := runReportV1200(t, s, "--days", "1")
	days, ok := m["days"].([]any)
	if !ok || len(days) != 1 {
		t.Fatalf("days=%v, want 1 row", m["days"])
	}
	d := days[0].(map[string]any)
	numEq(t, "days[0].dup_gap", d["dup_gap"], 3)
	numEq(t, "days[0].dup_correction", d["dup_correction"], 5)
	numEq(t, "days[0].dup_hook", d["dup_hook"], 2)
	// An unrecorded per-source field stays 0 (the missing-flat-key convention).
	numEq(t, "days[0].dup_mcp", d["dup_mcp"], 0)
}
