package ops

import (
	"context"
	"strings"
	"testing"
	"time"
)

func thresholdsLoads(f *fakeStore) int {
	n := 0
	for _, q := range f.queries {
		if strings.Contains(q, "FROM meta WHERE key IN") && strings.Contains(q, "'stale_days'") {
			n++
		}
	}
	return n
}

func TestReportThresholdsLoadedOnce(t *testing.T) {
	f := parityFake(10, 3, 1, 2)
	var out strings.Builder
	if code := RunReport(context.Background(), f, []string{"--days", "7"}, nil, &out); code != 0 {
		t.Fatalf("report exit=%d: %s", code, out.String())
	}
	if n := thresholdsLoads(f); n != 1 {
		t.Fatalf("thresholds IN 쿼리 %d회, want 1회(커맨드 스코프 공유)", n)
	}
}

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
			return [][]any{{int64(0)}}, nil
		case strings.Contains(sql, "valid_at"):
			return [][]any{{now.Add(-24 * time.Hour)}}, nil
		}
		return nil, nil
	}
}

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
	if n != 11 {
		t.Fatalf("ThFn 검사 %d종, want 11종(last-seen·backup-age·zero-near·pending-age·event-volume·meta-hygiene·secrets·metering-freshness·depth·size·versions)", n)
	}
	if got := thresholdsLoads(f); got != 1 {
		t.Fatalf("thresholds IN 쿼리 %d회, want 1회(실행 스코프 공유)", got)
	}
}

func TestDoctorFullRunThresholdsQueries(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	t.Setenv("FLINT_CLAUDE_HOME", t.TempDir())
	f := &fakeStore{queryFn: f6DoctorFn()}
	out, code := runDoctorArgs(t, f)
	if code != 0 && !strings.Contains(out, `"status":"ok"`) {
		t.Fatalf("doctor exit=%d: %s", code, out)
	}
	if got := thresholdsLoads(f); got != 1 {
		t.Fatalf("thresholds IN 쿼리 %d회, want 1회(secrets 포함 실행 스코프 공유)", got)
	}
}
