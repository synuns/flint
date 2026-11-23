package ops

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecayAuditShortRowGuard(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return [][]any{
				{int32(9), int32(5), now.Add(-25 * time.Hour)},
				{int32(3), int32(5), now.Add(-25 * time.Hour), ":adr:"},
				{int32(4), int32(9), now.Add(-1 * time.Hour), ":adr:"},
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

func TestDecayAuditSurvivedRelief(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	saved := decayNow
	decayNow = func() time.Time { return now }
	t.Cleanup(func() { decayNow = saved })

	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {

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
