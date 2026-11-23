package cli

import (
	"strings"
	"testing"
	"time"
)

func TestStaleCutoffTimestamptzParam(t *testing.T) {
	var staleSQL string
	var staleArgs []any
	f := &fakeMaint{fakeStore: &fakeStore{}}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return nil, nil
		}
		staleSQL, staleArgs = sql, args
		return nil, nil
	}
	if _, code := runMaintainArgs(t, f, "--stale", "--days", "30"); code != 0 {
		t.Fatalf("exit=%d, want 0; out 확인", code)
	}
	if staleSQL == "" {
		t.Fatal("stale 조회 미발생")
	}
	if strings.Contains(staleSQL, "interval") {
		t.Errorf("SQL에 interval 연산 잔존(Doltgres $1 interval 추론 결함): %q", staleSQL)
	}
	if len(staleArgs) != 2 {
		t.Fatalf("인자 수=%d, want 2(cutoff, limit): %v", len(staleArgs), staleArgs)
	}
	cut, ok := staleArgs[0].(time.Time)
	if !ok {
		t.Fatalf("첫 인자가 timestamptz 컷오프가 아님: %T(%v)", staleArgs[0], staleArgs[0])
	}
	if el := time.Since(cut); el < 29*24*time.Hour || el > 31*24*time.Hour {
		t.Errorf("컷오프 경과=%v, want 약 30일(--days 30)", el)
	}
	if lim, ok := staleArgs[1].(int); !ok || lim != 20 {
		t.Errorf("limit 인자=%v, want 20(기본)", staleArgs[1])
	}
}
