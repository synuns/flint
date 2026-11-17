// test_lookup_diff · 기존 usage 문자열 회귀(나머지 불변).
package cli

import (
	"strings"
	"testing"
)

// TestUsageIncludesDiff — usageLine에 audit| 뒤 diff 추가.
func TestUsageIncludesDiff(t *testing.T) {
	u := usageLine()
	if !strings.Contains(u, "audit|diff") {
		t.Fatalf("usageLine에 audit|diff 순서 없음: %s", u)
	}
}

// TestLookupDiff — diff 등록(diff.go init Register).
func TestLookupDiff(t *testing.T) {
	if _, ok := Lookup("diff"); !ok {
		t.Error("Lookup(\"diff\") 미등록")
	}
}

// TestUsageRestInvariant — diff 삽입 외 기존 usage 문자열 불변.
// 새 usage에서 "diff|update|tuning"만 지우면 v0.7까지의 문자열과
// 정확히 일치해야 한다(update·tuning 추가 반영,
// brief·pending 추가 — 커맨드 추가의 본질적 동반 갱신).
// UsageLine error 키 폐지 반영(안내형 — 정보 내용 불변).
// sync 추가(backup·restore 직후 — 원격 등록·push/pull).
func TestUsageRestInvariant(t *testing.T) {
	want := `{"usage":"flint <write|read|hot|brief|status|backup|restore|sync|approve|pending|maintain|doctor|report|done|compact|nudge|setup|teardown|version|mcp|audit> [args]"}`
	got := strings.Replace(usageLine(), "|profile", "", 1) // 삽입 제거
	got = strings.Replace(got, "|export", "", 1)           // 삽입 제거
	got = strings.Replace(got, "audit|diff|update|tuning|import", "audit", 1)
	if got != want {
		t.Fatalf("usage 나머지 불변 위반:\n got=%s\nwant=%s", got, want)
	}
}
