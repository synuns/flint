package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/query"
)

func recallWiringQueryFn(fallback [][]any, recall [][]any) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, " FROM recall_events GROUP BY"):
			return recall, nil
		case strings.Contains(sql, " OR "):
			return fallback, nil
		case strings.Contains(sql, "count(*) FROM knowledge"):
			return [][]any{{"0"}}, nil
		}
		return nil, nil
	}
}

func TestRunReadRecallSignalWiringOrdersRows(t *testing.T) {
	defer query.SetRecallCounts(nil)
	fallbackRows := func() [][]any {
		return [][]any{
			{"1", "backup policy demo", "synthetic note", "", "", "", "decision", "0.9", "", "active", int32(2)},
			{"2", "backup policy demo", "synthetic note", "", "", "", "decision", "0.9", "", "active", int32(2)},
		}
	}

	st := &fakeStore{queryFn: recallWiringQueryFn(fallbackRows(), [][]any{{int64(2), int64(3), "2026-11-27 00:00:00"}})}
	var out strings.Builder
	if code := RunRead(context.Background(), st,
		[]string{"backup", "policy"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d: %s", code, out.String())
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("출력 라인 수 = %d, want 2: %q", len(lines), out.String())
	}
	if !strings.HasPrefix(lines[0], "2|") {
		t.Fatalf("recall 3건 행(id=2)이 상위여야 함: 상위=%q", lines[0])
	}

	stNo := &fakeStore{queryFn: recallWiringQueryFn(fallbackRows(), nil)}
	var outNo strings.Builder
	if code := RunRead(context.Background(), stNo,
		[]string{"backup", "policy"}, strings.NewReader(""), &outNo); code != 0 {
		t.Fatalf("exit = %d: %s", code, outNo.String())
	}
	linesNo := strings.Split(strings.TrimRight(outNo.String(), "\n"), "\n")
	if !strings.HasPrefix(linesNo[0], "1|") {
		t.Fatalf("recall 무신호 시 기존 순위 유지: 상위=%q", linesNo[0])
	}
}
