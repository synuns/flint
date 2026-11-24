package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func abstentionStore(rows [][]any) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM read_events") {
			return rows, nil
		}
		return nil, nil
	}}
}

func TestZeroHitUnownedPctMath(t *testing.T) {
	t.Run("pure-math-boundaries", func(t *testing.T) {
		if got := zeroHitUnownedPctOf(nil); got != 0 {
			t.Fatalf("no events: got %v, want 0", got)
		}
		allHit := []ReadEventRow{{Zero: false}, {Zero: false}}
		if got := zeroHitUnownedPctOf(allHit); got != 0 {
			t.Fatalf("zero-hit 0: got %v, want 0.0", got)
		}
		allUnowned := []ReadEventRow{
			{Zero: true, ZeroReason: "unowned"},
			{Zero: true, ZeroReason: "unowned"},
		}
		if got := zeroHitUnownedPctOf(allUnowned); got != 100 {
			t.Fatalf("all unowned: got %v, want 100.0", got)
		}
		ownedMissOnly := []ReadEventRow{
			{Zero: true, ZeroReason: "owned_miss"},
			{Zero: false},
		}
		if got := zeroHitUnownedPctOf(ownedMissOnly); got != 0 {
			t.Fatalf("zero-hit but owned_miss only: got %v, want 0.0", got)
		}
	})

	t.Run("mixed-ratio", func(t *testing.T) {
		evs := []ReadEventRow{{Zero: true, ZeroReason: "unowned"}}
		for i := 0; i < 9; i++ {
			evs = append(evs, ReadEventRow{Zero: true, ZeroReason: "owned_miss"})
		}
		for i := 0; i < 8; i++ {
			evs = append(evs, ReadEventRow{Zero: false})
		}
		if got := zeroHitUnownedPctOf(evs); got != 10 {
			t.Fatalf("1 of 10 zero-hit unowned: got %v, want 10.0", got)
		}
	})

	t.Run("store-query", func(t *testing.T) {
		rows := [][]any{
			{true, "unowned"},
			{true, "owned_miss"},
			{false, ""},
		}
		got, err := ZeroHitUnownedPct(context.Background(), abstentionStore(rows), 14)
		if err != nil {
			t.Fatalf("ZeroHitUnownedPct: %v", err)
		}
		if got != 50 {
			t.Fatalf("got %v, want 50.0 (1 of 2 zero-hit unowned)", got)
		}
	})

	t.Run("query-error-propagates", func(t *testing.T) {
		s := &fakeStore{queryErr: errors.New("boom")}
		if _, err := ZeroHitUnownedPct(context.Background(), s, 14); err == nil {
			t.Fatal("want error from store query")
		}
	})

	t.Run("null-reason-tolerated", func(t *testing.T) {
		rows := [][]any{{true, nil}, {false, nil}}
		got, err := ZeroHitUnownedPct(context.Background(), abstentionStore(rows), 7)
		if err != nil || got != 0 {
			t.Fatalf("got (%v, %v), want (0.0, nil)", got, err)
		}
	})
}

func TestReportRowExposure(t *testing.T) {
	t.Run("collect-zero-decompose-sets-field", func(t *testing.T) {
		out := &ReportOutput{}
		inWin := []ReadEventRow{
			{Zero: true, ZeroReason: "unowned"},
			{Zero: true, ZeroReason: "owned_miss"},
			{Zero: false},
		}
		collectZeroDecompose(out, inWin)
		if out.Effect.ZeroHitUnownedPct != 50 {
			t.Fatalf("Effect.ZeroHitUnownedPct = %v, want 50.0", out.Effect.ZeroHitUnownedPct)
		}
	})

	t.Run("report-text-row", func(t *testing.T) {
		out := ReportOutput{}
		out.Effect.ZeroHitUnownedPct = 62.5
		var buf bytes.Buffer
		printReportText(&buf, out, 7)
		if !strings.Contains(buf.String(), "zero_hit_unowned_pct") {
			t.Fatalf("report text missing zero_hit_unowned_pct row:\n%s", buf.String())
		}
	})

	t.Run("serve-metric-row", func(t *testing.T) {
		rep := ReportOutput{}
		rep.Effect.ZeroHitUnownedPct = 62.5
		areas := serveAreas(rep)
		for _, a := range areas {
			for _, r := range a.Rows {
				if strings.Contains(r.Label, "zero-hit unowned") && r.Value == "62.5%" {
					return
				}
			}
		}
		t.Fatal("serve areas missing zero-hit unowned pct row with 62.5% value")
	})

	t.Run("json-tag-present", func(t *testing.T) {
		out := ReportOutput{}
		out.Effect.ZeroHitUnownedPct = 12.5
		b, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"zero_hit_unowned_pct":12.5`) {
			t.Fatalf("json missing zero_hit_unowned_pct: %s", b)
		}
	})
}
