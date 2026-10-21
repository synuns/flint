// hot_test.go — hot 예산 meta화: 미설정 기본 2,000·설정값 우선·무효값 폴백.
package cli

import "testing"

// getKV fake — 고정 (값, 오류) 반환.
func fakeGetKV(v string, err error) func(string, string) (string, error) {
	return func(key, def string) (string, error) { return v, err }
}

// TestHotBudgetDefault — 미설정 2,000·설정값 우선·무효 값 기본 폴백.
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

// errFake — 테스트용 조회 오류.
type errFake struct{}

func (errFake) Error() string { return "meta 조회 실패" }
