package ops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

type countsFake struct {
	*fakeMaintStore
	counts db.Counts
}

func (f *countsFake) Counts(ctx context.Context) (db.Counts, error) {
	return f.counts, nil
}

func TestMaintainCheckNextKey(t *testing.T) {
	cases := []struct {
		name       string
		counts     db.Counts
		wantStatus string
		wantNext   []string
	}{
		{"ok — 임계 이내", db.Counts{Pending: 10}, "ok", []string{"none"}},
		{"pending 초과", db.Counts{Pending: 51}, "review-needed",
			[]string{"flint pending", "maintain --expire-pending"}},
		{"dup 초과", db.Counts{DupSuspect: 21}, "review-needed",
			[]string{"maintain --dup-suspects"}},
		{"양쪽 초과", db.Counts{Pending: 60, DupSuspect: 25}, "review-needed",
			[]string{"flint pending", "maintain --expire-pending", "maintain --dup-suspects"}},
	}
	for _, c := range cases {
		f := &countsFake{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{}}, counts: c.counts}
		var buf strings.Builder
		code := RunMaintain(context.Background(), f, []string{"--check"}, nil, &buf)
		out := buf.String()
		if code != 0 {
			t.Errorf("%s: exit=%d, want 0", c.name, code)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
			t.Fatalf("%s: JSON 아님: %q", c.name, out)
		}
		if m["status"] != c.wantStatus {
			t.Errorf("%s: status=%v, want %q", c.name, m["status"], c.wantStatus)
		}
		next, ok := m["next"].(string)
		if !ok || next == "" {
			t.Errorf("%s: next 키 부재/빈 값: %q", c.name, out)
			continue
		}
		for _, want := range c.wantNext {
			if !strings.Contains(next, want) {
				t.Errorf("%s: next=%q, want %q 포함", c.name, next, want)
			}
		}
	}
}
