// text_test.go — TruncateRunes·CellToInt 테스트. v0.34.3 단일 구현
// 수렴 관례를 lib/render 이동 후에도 승계한다(스펙 §2).
// v0.39.0 T4: CountPending 테스트는 정의의 knowledge 이관에 동반해
// lib/knowledge/pending_count_test.go로 이동했다(룰링).
package render

import (
	"strings"
	"testing"
)

// TestTruncateRunesSingleSource — rune 절단(멀티바이트 안전) 단일 구현.
// 상한 이하·빈 문자열 불변, 혼합 문자열 룬 단위 절단.
func TestTruncateRunesSingleSource(t *testing.T) {
	long := strings.Repeat("한", 60) // 60룬(180바이트) — 바이트 절단이면 파손
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

// TestCellToIntSingleSource — v0.39.0 T3 부채 수렴: 셀(any)→int 단일
// 강제 변환 구현이 render에만 존재한다. pgx 숫자형·float64·10진 문자열
// 수용, nil·비수는 (0, false) — read rowIDs·maintain stale·report 집계가
// 공유하는 계약(구 cli/read.go 복제본은 T3 이동으로 삭제).
func TestCellToIntSingleSource(t *testing.T) {
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
		{7.9, 7, true}, // float64는 버림 강제 — 기존 계약 불변
	}
	for _, tc := range cases {
		got, ok := CellToInt(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("CellToInt(%#v) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
