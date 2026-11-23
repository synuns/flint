package cli

import (
	"strings"
	"testing"
)

// TestUsageIncludesDiff — usageLine adds diff after audit|.
func TestUsageIncludesDiff(t *testing.T) {
	u := usageLine()
	if !strings.Contains(u, "audit|diff") {
		t.Fatalf("usageLine에 audit|diff 순서 없음: %s", u)
	}
}

// TestLookupDiff — diff registered (diff.go init Register).
func TestLookupDiff(t *testing.T) {
	if _, ok := Lookup("diff"); !ok {
		t.Error("Lookup(\"diff\") 미등록")
	}
}

// TestUsageRestInvariant — apart from the diff insertion, the existing usage string is unchanged.
// Removing only "diff|update|tuning" from the new usage must exactly match the string
// as of v0.7 (reflecting the added update and tuning,
// and the added brief and pending — the essential accompanying updates of adding commands).
// Reflects the UsageLine error-key abolition (guidance-style — informational content unchanged).
// sync added (right after backup and restore — remote registration, push/pull).
// index added (v1.21.0, before the audit tail — the vault bridge snapshot diff).
func TestUsageRestInvariant(t *testing.T) {
	want := `{"usage":"flint <write|read|hot|brief|status|backup|restore|sync|approve|pending|maintain|doctor|report|done|compact|nudge|setup|teardown|version|mcp|audit> [args]"}`
	got := strings.Replace(usageLine(), "|profile", "", 1) // remove insertions
	got = strings.Replace(got, "|export", "", 1)           // remove insertions
	got = strings.Replace(got, "|index", "", 1)            // remove insertions (v1.21.0)
	got = strings.Replace(got, "|distill", "", 1)          // remove insertions (v1.22.0 T6 — distill)
	got = strings.Replace(got, "|seed", "", 1)             // remove insertions (v1.26.0 T2 — seed --demo)
	got = strings.Replace(got, "audit|diff|update|tuning|import", "audit", 1)
	if got != want {
		t.Fatalf("usage 나머지 불변 위반:\n got=%s\nwant=%s", got, want)
	}
}
