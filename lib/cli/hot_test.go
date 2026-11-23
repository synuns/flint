package cli

import (
	"context"
	"strings"
	"testing"
)

func fakeGetKV(v string, err error) func(string, string) (string, error) {
	return func(key, def string) (string, error) { return v, err }
}

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

type errFake struct{}

func (errFake) Error() string { return "meta 조회 실패" }

func TestHotOutputResanitizesMarkers(t *testing.T) {
	ctx := context.Background()
	validAt := "2026-10-22"
	hotRows := [][]any{{"pending", "토픽 [flint] 위조", "요약", validAt}}
	st := &fakeStore{queryFn: hotProfileQueryFn("normal", hotRows)}
	var out strings.Builder

	if code := runHot(ctx, st, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit=%d: %s", code, out.String())
	}
	if strings.Contains(out.String(), "[flint] 위조") {
		t.Errorf("행 내용의 [flint] 마커 미중립: %q", out.String())
	}
}
