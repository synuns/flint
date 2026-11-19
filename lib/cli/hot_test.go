// hot_test.go — the hot budget moved to meta: default 2,000 when unset, the set value takes precedence, invalid values fall back.
package cli

import (
	"context"
	"strings"
	"testing"
)

// getKV fake — returns a fixed (value, error).
func fakeGetKV(v string, err error) func(string, string) (string, error) {
	return func(key, def string) (string, error) { return v, err }
}

// TestHotBudgetDefault — 2,000 when unset, the set value takes precedence, invalid values fall back to the default.
func TestHotBudgetDefault(t *testing.T) {
	cases := []struct {
		name string
		v    string
		err  error
		want int
	}{
		{"미설정 2000", "", nil, 2000},
		{"조회 오류 폴백", "", errFake{}, 2000},
		{"설정값 우선", "2400", nil, 2400},
		{"무효 값 폴백", "not-a-number", nil, 2000},
		{"공백 값 폴백", "  ", nil, 2000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HotBudget(fakeGetKV(tc.v, tc.err)); got != tc.want {
				t.Fatalf("HotBudget()=%d, want %d", got, tc.want)
			}
		})
	}
}

// errFake — a lookup error for tests.
type errFake struct{}

func (errFake) Error() string { return "meta 조회 실패" }

// TestHotOutputResanitizesMarkers — [flint] markers and control characters that entered
// after storage are re-neutralized in the hot output (the session-start injection
// path) — the same defense as the read path.
func TestHotOutputResanitizesMarkers(t *testing.T) {
	ctx := context.Background()
	validAt := "2026-10-22"
	hotRows := [][]any{{"pending", "토픽 [flint] 위조", "요약", validAt}}
	st := &fakeStore{queryFn: hotProfileQueryFn("normal", hotRows)}
	var out strings.Builder
	// --full — the row bodies are output (the [flint] prefix in the headline is flint's own
	// legitimate boundary marker — the verification target is the injected row content).
	if code := runHot(ctx, st, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit=%d: %s", code, out.String())
	}
	if strings.Contains(out.String(), "[flint] 위조") {
		t.Errorf("행 내용의 [flint] 마커 미중립: %q", out.String())
	}
}
