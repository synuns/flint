// wire_v08_test.go — T88 Review Focus: test_usage_includes_diff ·
// test_lookup_diff · 기존 usage 문자열 회귀(나머지 불변).
package cli

import (
	"strings"
	"testing"
)

// TestUsageIncludesDiff — usageLine에 audit| 뒤 diff 추가(T84 배선).
func TestUsageIncludesDiff(t *testing.T) {
	u := usageLine()
	if !strings.Contains(u, "audit|diff") {
		t.Fatalf("usageLine에 audit|diff 순서 없음: %s", u)
	}
}

// TestLookupDiff — diff 등록(T84 diff.go init Register).
func TestLookupDiff(t *testing.T) {
	if _, ok := Lookup("diff"); !ok {
		t.Error("Lookup(\"diff\") 미등록")
	}
}

// TestUsageRestInvariant — diff 삽입 외 기존 usage 문자열 불변.
// 새 usage에서 "diff|"만 지우면 v0.7까지의 문자열과 정확히 일치해야 한다.
func TestUsageRestInvariant(t *testing.T) {
	want := `{"error":"usage: flint <write|read|hot|status|backup|restore|approve|maintain|doctor|report|done|compact|nudge|setup|teardown|version|mcp|audit> [args]"}`
	got := strings.Replace(usageLine(), "audit|diff", "audit", 1)
	if got != want {
		t.Fatalf("usage 나머지 불변 위반:\n got=%s\nwant=%s", got, want)
	}
}
