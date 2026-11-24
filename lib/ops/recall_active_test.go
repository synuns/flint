package ops

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/synuns/flint/lib/wire"
)

const injectRealMode = "SUPERLITE_REAL"

func fakeBurstItems() []BurstItem {
	return []BurstItem{
		{ID: 7, Topic: "demo topic", Reason: "stale-condition"},
		{ID: 8, Topic: "second topic", Reason: "vault-changed"},
	}
}

func activeRecallQueryFn(t *testing.T, used int) func(string, []any) ([][]any, error) {
	t.Helper()
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM meta"):
			if len(args) > 0 && fmt.Sprint(args[0]) == "hot_budget" {
				return [][]any{{"500"}}, nil
			}
			return nil, nil
		case strings.Contains(sql, "FROM hot_events"):
			return [][]any{{used}}, nil
		case strings.Contains(sql, "FROM session_starts"):
			return [][]any{{time.Now().UTC()}}, nil
		case strings.Contains(sql, "FROM knowledge k WHERE k.id"):
			id := 0
			if len(args) > 0 {
				if n, ok := injectCellID(args[0]); ok {
					id = n
				}
			}
			switch id {
			case 7:
				return [][]any{{"demo topic", "demo summary", "demo-source", 0.75}}, nil
			case 8:
				return [][]any{{strings.Repeat("x", 400), "second summary", "", 0}}, nil
			}
			return nil, nil
		}
		return nil, nil
	}
}

func injectCellID(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case string:
		parsed, perr := strconv.Atoi(n)
		return parsed, perr == nil
	}
	return 0, false
}

func injectFixture(t *testing.T, used int) *bytes.Buffer {
	t.Helper()
	real := os.Getenv(injectRealMode) == "1"
	saved := burstItemsFn
	t.Cleanup(func() { burstItemsFn = saved })
	if !real {
		burstItemsFn = func(ctx context.Context, st wire.Store, limit int) ([]BurstItem, error) {
			return fakeBurstItems(), nil
		}
	}
	st := &fakeStore{}
	st.queryFn = activeRecallQueryFn(t, used)
	var out bytes.Buffer
	if code := InjectActiveRecall(context.Background(), st, "sess-demo", &out, used); code != 0 {
		t.Fatalf("InjectActiveRecall exit = %d, want 0 (quiet contract)", code)
	}
	return &out
}

func TestInjectSubtractsSessionHotBytes(t *testing.T) {
	out := injectFixture(t, 400)
	if n := strings.Count(out.String(), "recall-active|id="); n != 0 {
		t.Fatalf("hot bytes 400 of budget 500 must leave no room, got %d rows", n)
	}
	out = injectFixture(t, 350)
	if n := strings.Count(out.String(), "recall-active|id="); n != 1 {
		t.Fatalf("hot bytes 350 of budget 500 must leave exactly 1 row, got %d", n)
	}
}

func TestInjectRespectsRemainingBudget(t *testing.T) {
	out := injectFixture(t, 142)
	if got := out.String(); got == "" {
		t.Fatal("remaining budget > 0 but no injection rows")
	}
	zero := injectFixture(t, 500)
	if zero.Len() != 0 {
		t.Fatalf("remaining 0 must print nothing, got %q", zero.String())
	}
}

func koreanBudgetQueryFn(t *testing.T) func(string, []any) ([][]any, error) {
	t.Helper()
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM meta"):
			return [][]any{{"500"}}, nil
		case strings.Contains(sql, "FROM knowledge k WHERE k.id"):
			id := 0
			if len(args) > 0 {
				if n, ok := injectCellID(args[0]); ok {
					id = n
				}
			}
			switch id {
			case 7:
				return [][]any{{strings.Repeat("가", 10), strings.Repeat("나", 180), "", 0}}, nil
			case 8:
				return [][]any{{"보조 항목", "보조 요약", "", 0}}, nil
			}
			return nil, nil
		}
		return nil, nil
	}
}

func TestInjectBudgetUnitConsistency(t *testing.T) {
	if os.Getenv(injectRealMode) == "1" {
		t.Skip("synthetic Korean fixture only")
	}
	saved := burstItemsFn
	t.Cleanup(func() { burstItemsFn = saved })
	burstItemsFn = func(ctx context.Context, st wire.Store, limit int) ([]BurstItem, error) {
		return []BurstItem{{ID: 7, Topic: "예산 단위", Reason: "stale-condition"}}, nil
	}
	st := &fakeStore{}
	st.queryFn = koreanBudgetQueryFn(t)
	var out bytes.Buffer
	if code := InjectActiveRecall(context.Background(), st, "sess-unit", &out, 0); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if n := strings.Count(out.String(), "recall-active|id="); n != 1 {
		t.Fatalf("rows = %d, want 1 (rune cost 234 ≤ remaining 500 — byte cost 614이 한글 hot 뒤 능동 회상을 굶기면 안 됨)", n)
	}
}

func TestRemainingNeverNegative(t *testing.T) {
	if os.Getenv(injectRealMode) == "1" {
		t.Skip("synthetic Korean fixture only")
	}
	saved := burstItemsFn
	t.Cleanup(func() { burstItemsFn = saved })
	burstItemsFn = func(ctx context.Context, st wire.Store, limit int) ([]BurstItem, error) {
		return []BurstItem{
			{ID: 7, Topic: "경계", Reason: "stale-condition"},
			{ID: 8, Topic: "다음", Reason: "vault-changed"},
		}, nil
	}
	line := "recall-active|id=7|reason=stale-condition|" + strings.Repeat("가", 10) + "|" + strings.Repeat("나", 180)
	hotUsed := 500 - (utf8.RuneCountInString(line) + 1)
	st := &fakeStore{}
	st.queryFn = koreanBudgetQueryFn(t)
	var out bytes.Buffer
	if code := InjectActiveRecall(context.Background(), st, "sess-boundary", &out, hotUsed); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if n := strings.Count(out.String(), "recall-active|id=7"); n != 1 {
		t.Fatalf("boundary rows = %d, want 1 (cost == remaining 경계에서 정확히 1행)", n)
	}
	if strings.Contains(out.String(), "recall-active|id=8") {
		t.Fatal("remaining 소진 후 두 번째 행 출력 금지 (remaining 음수 진입 없음)")
	}

	var starved bytes.Buffer
	st2 := &fakeStore{}
	st2.queryFn = koreanBudgetQueryFn(t)
	if code := InjectActiveRecall(context.Background(), st2, "sess-over", &starved, 600); code != 0 {
		t.Fatalf("exit = %d, want 0 (hotUsed 초과도 조용히 0)", code)
	}
	if starved.Len() != 0 {
		t.Fatalf("remaining ≤ 0 must print nothing, got %q", starved.String())
	}
}

func TestInjectQuietOnNoBurst(t *testing.T) {
	saved := burstItemsFn
	t.Cleanup(func() { burstItemsFn = saved })
	burstItemsFn = func(ctx context.Context, st wire.Store, limit int) ([]BurstItem, error) {
		return nil, nil
	}
	st := &fakeStore{}
	st.queryFn = activeRecallQueryFn(t, 0)
	var out bytes.Buffer
	if code := InjectActiveRecall(context.Background(), st, "sess-demo", &out, 0); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if out.Len() != 0 {
		t.Fatalf("no burst must stay quiet, got %q", out.String())
	}
}

func TestInjectProvenanceFormat(t *testing.T) {
	if os.Getenv(injectRealMode) == "1" {
		t.Skip("needs seeded burst items; real mode covered by budget/quiet tests")
	}
	out := injectFixture(t, 0)
	line := out.String()
	if !strings.Contains(line, "|source=demo-source") || !strings.Contains(line, "|confidence=0.75") {
		t.Fatalf("provenance suffix missing: %q", line)
	}
	if strings.Contains(line, "|source=|") || strings.Contains(line, "||") {
		t.Fatalf("empty provenance fields must be omitted: %q", line)
	}
}

func TestInjectRenderJokypoint(t *testing.T) {
	if os.Getenv(injectRealMode) == "1" {
		t.Skip("needs seeded burst items")
	}
	out := injectFixture(t, 0)
	line := out.String()
	if strings.ContainsRune(line, '\x1b') {
		t.Fatalf("raw control bytes leaked: %q", line)
	}
}

func TestInjectRowTruncation(t *testing.T) {
	if os.Getenv(injectRealMode) == "1" {
		t.Skip("needs seeded burst items")
	}
	out := injectFixture(t, 142)
	if n := strings.Count(out.String(), "recall-active|id="); n != 1 {
		t.Fatalf("rows printed = %d, want 1 (row-wise budget truncation)", n)
	}
}

func TestInjectItemQueryErrorStaysQuiet(t *testing.T) {
	if os.Getenv(injectRealMode) == "1" {
		t.Skip("needs seeded burst items")
	}
	saved := burstItemsFn
	t.Cleanup(func() { burstItemsFn = saved })
	burstItemsFn = func(ctx context.Context, st wire.Store, limit int) ([]BurstItem, error) {
		return fakeBurstItems(), nil
	}
	st := &fakeStore{queryErr: fmt.Errorf("engine down")}
	var out bytes.Buffer
	if code := InjectActiveRecall(context.Background(), st, "sess-demo", &out, 0); code != 0 {
		t.Fatalf("exit = %d, want 0 on store failure", code)
	}
	if out.Len() != 0 {
		t.Fatalf("failure must skip quietly, got %q", out.String())
	}
}
