// text_test.go — tests for TruncateRunes and CellToInt. Single-implementation
// convention (spec §2).
package render

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// TestTruncateRunesSingleSource — single implementation of rune-bound
// truncation (multibyte-safe). Under-bound and empty strings unchanged;
// mixed strings truncated by rune count.
func TestTruncateRunesSingleSource(t *testing.T) {
	long := strings.Repeat("한", 60) // 60 runes (180 bytes) — byte truncation would corrupt
	if got := TruncateRunes(long, 40); len([]rune(got)) != 40 || got != strings.Repeat("한", 40) {
		t.Errorf("룬 절단 위반: %d룬", len([]rune(got)))
	}
	if got := TruncateRunes("abc한", 3); got != "abc" {
		t.Errorf("혼합 문자열 절단=%q, want %q", got, "abc")
	}
	if got := TruncateRunes("한글", 2); got != "한글" {
		t.Errorf("경계(상한==길이) 불변 위반: %q", got)
	}
	if got := TruncateRunes("한글", 40); got != "한글" {
		t.Errorf("상한 이하 불변 위반: %q", got)
	}
	if got := TruncateRunes("", 40); got != "" {
		t.Errorf("빈 문자열: %q", got)
	}
}

// TestCellToIntSingleSource — the single cell(any)->int coercion lives only
// in render. Accepts pgx numeric types, float64, decimal strings, and
// pgtype.Numeric; nil and non-numerics yield (0, false) — the contract
// shared by read rowIDs, maintain stale, and report aggregation.
func TestCellToIntSingleSource(t *testing.T) {
	// the r109-1 numeric case — a Doltgres numeric column cell (pgtype.Numeric)
	// must coerce, not silently read 0. Built via ScanInt64 (int-valued) and
	// the zero value (NULL/invalid) for the failure side.
	validNum := pgtype.Numeric{}
	if err := validNum.ScanInt64(pgtype.Int8{Int64: 4242, Valid: true}); err != nil {
		t.Fatalf("Numeric 구성 실패: %v", err)
	}
	cases := []struct {
		in   any
		want int
		ok   bool
	}{
		{7, 7, true},
		{int32(7), 7, true},
		{int64(7), 7, true},
		{7.0, 7, true},
		{"7", 7, true},
		{nil, 0, false},
		{"x", 0, false},
		{7.9, 7, true},               // float64 truncates toward zero — existing contract unchanged
		{validNum, 4242, true},       // r109-1: Doltgres numeric cells coerce
		{pgtype.Numeric{}, 0, false}, // invalid (NULL) numeric stays (0, false)
	}
	for _, tc := range cases {
		got, ok := CellToInt(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("CellToInt(%#v) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
