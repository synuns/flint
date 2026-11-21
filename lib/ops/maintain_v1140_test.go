// maintain_v1140_test.go — the v1.14.0 T4 decay-audit regressions:
// (1) the R56 row-length guard (a 3-cell row — an old-shape/fake row
// without the tags tail — must be skipped, not panic on the r[3] index);
// (2) the survived decay-relief connection (one batched SurvivedCounts
// lookup, effective = count − max(0, rate×days − survived×relief)).
package ops

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestDecayAuditShortRowGuard — R56: the decay-audit loop guard must be
// len(r) < 4 (the v1.13.0 T2 tags tail made column 3 load-bearing — a
// 3-cell row panics on r[3] indexing with the old len(r) < 3 guard).
func TestDecayAuditShortRowGuard(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return [][]any{
				{int32(9), int32(5), now.Add(-25 * time.Hour)},          // 3 cells — skipped, no panic
				{int32(3), int32(5), now.Add(-25 * time.Hour), ":adr:"}, // 1-day decay — a candidate
				{int32(4), int32(9), now.Add(-1 * time.Hour), ":adr:"},  // 0 days — excluded
			}, nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--decay-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	var parsed decayAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &parsed); err != nil {
		t.Fatalf("JSON 파싱: %v (%s)", err, out)
	}
	if parsed.Count != 1 {
		t.Errorf("후보 수 = %d, want 1(3셀 행 skip·0일 제외 후)", parsed.Count)
	}
}

// TestDecayAuditSurvivedRelief — with survived_decay_relief overridden
// (meta) the effective score takes the relief: 12 − max(0, 1×10 − 4×1) = 6.
// The survived lookup answers id 3 → 4 (one batched query).
func TestDecayAuditSurvivedRelief(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		// The meta thresholds lookup must precede the knowledge_reads case —
		// its key list contains knowledge_reads_retention_days (substring
		// collision); it is matched on its FROM meta shape instead.
		case strings.Contains(sql, "FROM meta") && strings.Contains(sql, "survived_decay_relief"):
			return [][]any{{"survived_decay_relief", "1.0"}}, nil
		case strings.Contains(sql, "FROM knowledge_reads"):
			return [][]any{{int32(3), int64(4)}}, nil
		case strings.Contains(sql, "FROM knowledge"):
			return [][]any{{int32(3), int32(12), now.Add(-10 * 24 * time.Hour), ":adr:"}}, nil
		}
		return nil, nil
	}}}
	out, code := runMaintainArgs(t, f, "--decay-audit")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	var parsed decayAuditJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &parsed); err != nil {
		t.Fatalf("JSON 파싱: %v (%s)", err, out)
	}
	if parsed.Count != 1 || len(parsed.Items) != 1 {
		t.Fatalf("후보 수 = %d, want 1 (%s)", parsed.Count, out)
	}
	it := parsed.Items[0]
	if it.Effective != 6 {
		t.Errorf("완화 effective = %d, want 6(12 − max(0, 10 − 4×1))", it.Effective)
	}
	if it.Score != 12 {
		t.Errorf("score = %d, want 12", it.Score)
	}
}
