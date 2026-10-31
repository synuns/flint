package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type dupCountStore struct {
	fakeStore
	calls []string
}

func (d *dupCountStore) RecordUsageCount(ctx context.Context, kind, entity string, n int) error {
	d.calls = append(d.calls, kind)
	return nil
}

func TestRecordDupsSplitsBySource(t *testing.T) {
	d := &dupCountStore{}
	recordDups(context.Background(), d, "hook", 3)
	if len(d.calls) != 2 || d.calls[0] != "dup" || d.calls[1] != "dup_hook" {
		t.Fatalf("카운터 호출 = %v, want [dup dup_hook]", d.calls)
	}
	recordDups(context.Background(), d, "manual", 2)
	if d.calls[len(d.calls)-1] != "dup_manual" {
		t.Fatalf("manual 소스 키 누락: %v", d.calls)
	}
}

func TestReportDailyDupHookFields(t *testing.T) {
	row := DailyRow{Date: "d", Write: 1, Dup: 4, DupHook: 3, DupManual: 1}
	b, _ := json.Marshal(row)
	s := string(b)
	for _, want := range []string{`"dup_hook":3`, `"dup_manual":1`, `"dup_mcp":0`} {
		if !strings.Contains(s, want) {
			t.Errorf("필드 누락 %q in %s", want, s)
		}
	}
	if p := dupPct([]DailyRow{row}); p != 80 {
		t.Errorf("총계 dup_pct 변형 = %d, want 80", p)
	}
}
