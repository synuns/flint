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
// 새 usage에서 "diff|update|tuning"만 지우면 v0.7까지의 문자열과
// 정확히 일치해야 한다(v0.22.0 update·v0.23.0 tuning 추가 반영,
// v0.34.0 brief·v0.34.2 pending 추가 — 커맨드 추가의 본질적 동반 갱신).
func TestUsageRestInvariant(t *testing.T) {
<<<<<<< HEAD
	want := `{"error":"usage: flint <write|read|hot|brief|status|backup|restore|approve|pending|maintain|doctor|report|done|compact|nudge|setup|teardown|version|mcp|audit> [args]"}`
	got := strings.Replace(usageLine(), "audit|diff|update|tuning|import", "audit", 1)
=======
	want := `{"error":"usage: flint <write|read|hot|status|backup|restore|approve|maintain|doctor|report|done|compact|nudge|setup|teardown|version|mcp|audit> [args]"}`
	got := strings.Replace(usageLine(), "|profile", "", 1) // v0.35.0 삽입 제거
	got = strings.Replace(got, "audit|diff|update|tuning|import", "audit", 1)
>>>>>>> 5f82563 (feat: v0.35.0 — 프로파일 판정 완결: 추천에서 전환까지 (D①))
	if got != want {
		t.Fatalf("usage 나머지 불변 위반:\n got=%s\nwant=%s", got, want)
	}
}
