package cli

import (
	"strings"
	"testing"
)

func TestUsageIncludesDiff(t *testing.T) {
	u := usageLine()
	if !strings.Contains(u, "audit|diff") {
		t.Fatalf("usageLine에 audit|diff 순서 없음: %s", u)
	}
}

func TestLookupDiff(t *testing.T) {
	if _, ok := Lookup("diff"); !ok {
		t.Error("Lookup(\"diff\") 미등록")
	}
}

func TestUsageRestInvariant(t *testing.T) {
	want := `{"usage":"flint <write|read|hot|brief|status|backup|restore|sync|approve|pending|maintain|doctor|report|done|compact|nudge|setup|teardown|version|mcp|audit> [args]"}`
	got := strings.Replace(usageLine(), "|profile", "", 1)
	got = strings.Replace(got, "|export", "", 1)
	got = strings.Replace(got, "|index", "", 1)
	got = strings.Replace(got, "|distill", "", 1)
	got = strings.Replace(got, "|seed", "", 1)
	got = strings.Replace(got, "audit|diff|update|tuning|import", "audit", 1)
	if got != want {
		t.Fatalf("usage 나머지 불변 위반:\n got=%s\nwant=%s", got, want)
	}
}
