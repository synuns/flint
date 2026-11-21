// thresholds_once_f6_test.go — performance regression: the thresholds IN query
// (SELECT key, value FROM meta WHERE key IN ...) runs once per command execution.
// report's --days cap, stale cutoff, and effect decision window, and doctor's 7 checks,
// which each re-queried, now share 1 command-scoped load (secrets folded into
// the ThFn — a full doctor run is also 1).
package ops

import (
	"context"
	"strings"
	"testing"
	"time"
)

// thresholdsLoads — the count of thresholds IN queries the fake saw. The hot_stats lookup
// (IN ('hot_stats')) uses the same syntax, so it is distinguished by whether a thresholds-registry
// key ('stale_days') is included.
func thresholdsLoads(f *fakeStore) int {
	n := 0
	for _, q := range f.queries {
		if strings.Contains(q, "FROM meta WHERE key IN") && strings.Contains(q, "'stale_days'") {
			n++
		}
	}
	return n
}

// TestReportThresholdsLoadedOnce — a report --days run issues the thresholds IN
// query once.
func TestReportThresholdsLoadedOnce(t *testing.T) {
	f := parityFake(10, 3, 1, 2) // the read 10, zero 3, near 1, fallback 2 fixture
	var out strings.Builder
	if code := RunReport(context.Background(), f, []string{"--days", "7"}, nil, &out); code != 0 {
		t.Fatalf("report exit=%d: %s", code, out.String())
	}
	if n := thresholdsLoads(f); n != 1 {
		t.Fatalf("thresholds IN 쿼리 %d회, want 1회(커맨드 스코프 공유)", n)
	}
}

// f6DoctorFn — responses where all 7 checks (last-seen, backup-age, zero-near,
// pending-age, event-volume, meta-hygiene, secrets) consume thresholds:
// last_backup, the most recent pending row, and 0 metering-table rows.
func f6DoctorFn() func(string, []any) ([][]any, error) {
	now := time.Now().UTC()
	fresh := now.Add(-time.Hour).Format(time.RFC3339)
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "schema_version"):
			return [][]any{{"9"}}, nil
		case strings.Contains(sql, "pg_indexes"):
			return [][]any{{int64(3)}}, nil
		case strings.Contains(sql, "last_write_at"):
			return [][]any{
				{"last_write_at", fresh},
				{"last_hot_at", fresh},
				{"last_read_at", fresh},
			}, nil
		case strings.Contains(sql, "last_backup"):
			return [][]any{{"last_backup", fresh}}, nil
		case strings.Contains(sql, "count(*) FROM read_events"):
			return [][]any{{int64(0)}}, nil // the event-volume 3-table total
		case strings.Contains(sql, "valid_at"):
			return [][]any{{now.Add(-24 * time.Hour)}}, nil // a pending exists
		}
		return nil, nil // thresholds, daily, read_events, meta keys — unset
	}
}

// TestDoctorThresholdsSharedAcrossChecks — running the 7 thresholds-consuming
// checks through the run-scoped memo (doctorThresholds) means 1 IN query
// (secrets folded in).
func TestDoctorThresholdsSharedAcrossChecks(t *testing.T) {
	f := &fakeStore{queryFn: f6DoctorFn()}
	th := doctorThresholds(context.Background(), f)
	ctx := context.Background()
	n := 0
	for _, s := range doctorChecks() {
		if s.ThFn == nil {
			continue
		}
		runCheckSpec(ctx, f, th, s)
		n++
	}
	if n != 8 {
		t.Fatalf("ThFn 검사 %d종, want 8종(last-seen·backup-age·zero-near·pending-age·event-volume·meta-hygiene·secrets·metering-freshness)", n)
	}
	if got := thresholdsLoads(f); got != 1 {
		t.Fatalf("thresholds IN 쿼리 %d회, want 1회(실행 스코프 공유)", got)
	}
}

// TestDoctorFullRunThresholdsQueries — a full doctor run issues the thresholds IN
// query once (secrets folded into the ThFn).
func TestDoctorFullRunThresholdsQueries(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	t.Setenv("FLINT_CLAUDE_HOME", t.TempDir()) // skill undetected — the separate chain skipped
	f := &fakeStore{queryFn: f6DoctorFn()}
	out, code := runDoctorArgs(t, f)
	if code != 0 && !strings.Contains(out, `"status":"ok"`) {
		t.Fatalf("doctor exit=%d: %s", code, out)
	}
	if got := thresholdsLoads(f); got != 1 {
		t.Fatalf("thresholds IN 쿼리 %d회, want 1회(secrets 포함 실행 스코프 공유)", got)
	}
}
