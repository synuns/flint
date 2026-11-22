// usage_registry_test.go — 3 usage-drift guards.
// When the wire package runs standalone, the registry is empty, so seedCommands
// registers a mirror of the real command surface (27 human-exposed + 2
// hook-only) — adding a new command requires updating this list together with
// usageOrder (the same maintenance contract as the existing cli tests).
package wire

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
)

// seedCommands — a mirror of the real command surface (the human-exposed set must
// equal usageOrder + the hook-only session-start/end). The handlers are not under test.
var seedCommands = []string{
	"write", "read", "hot", "brief", "status", "profile", "backup", "restore",
	"sync",
	"approve", "pending", "maintain", "doctor", "report", "export",
	"distill",
	"done", "compact", "nudge", "setup", "teardown", "version", "mcp",
	"index",
	"audit", "diff", "update", "tuning", "import",
	"session-start", "session-end",
}

// seedRegistry — registers seedCommands with stub handlers (restored after the test).
func seedRegistry(t *testing.T) {
	t.Helper()
	stub := func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int { return 0 }
	for _, name := range seedCommands {
		n := name
		Register(n, stub)
		t.Cleanup(func() { Unregister(n) })
	}
}

// TestUsageLineListsAllRegistered — usageLine must list every user-exposed
// command registered in the registry. session-start/end are hook-only entry
// points, so excluded (incompatible with the wire_v08 TestUsageRestInvariant contract).
func TestUsageLineListsAllRegistered(t *testing.T) {
	seedRegistry(t)
	line := UsageLine()
	for _, name := range seedCommands {
		if hiddenFromUsage(name) {
			if strings.Contains(line, name) {
				t.Fatalf("훅 전용 %q이 usage에 노출됨", name)
			}
			continue
		}
		if !strings.Contains(line, name) {
			t.Fatalf("usage missing registered command %q", name)
		}
	}
	if !strings.Contains(line, "pending") {
		t.Fatal("pending missing — R2")
	}
}

// TestUsageOrderMatchesRegistry — the usageOrder set == the registry key set
// (hook-only excluded). Registering a new command without adding it to
// usageOrder FAILs this test — a permanent drift guard.
func TestUsageOrderMatchesRegistry(t *testing.T) {
	seedRegistry(t)
	want := map[string]bool{}
	for _, name := range seedCommands {
		if !hiddenFromUsage(name) {
			want[name] = true
		}
	}
	got := map[string]bool{}
	for _, name := range usageOrder {
		if got[name] {
			t.Fatalf("usageOrder 중복: %q", name)
		}
		got[name] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("usageOrder/registry 불일치\n usageOrder=%v\nregistry=%v", got, want)
	}
}

// TestHelpSummaryDocumentsPAMExport — the HelpSummary export entry must
// mention the PAM standard export (--format pam) and the --out sidecar
// checksum (an export-row extension — not a new command, so the usageOrder
// and seedCommands sets are unchanged).
func TestHelpSummaryDocumentsPAMExport(t *testing.T) {
	s := HelpSummary()
	for _, want := range []string{"export", "pam", "sha256"} {
		if !strings.Contains(s, want) {
			t.Errorf("helpSummary에 %q 누락: %s", want, s)
		}
	}
}

// TestHelpSummaryCoversUsageOrder — a completeness guard:
// HelpSummary must include the keywords of every usageOrder item. Adding a new
// command to usageOrder without updating HelpSummary FAILs this test (a
// HelpSummary drift guard).
func TestHelpSummaryCoversUsageOrder(t *testing.T) {
	s := HelpSummary()
	for _, name := range usageOrder {
		if !strings.Contains(s, name) {
			t.Errorf("HelpSummary에 usageOrder 항목 %q 누락: %s", name, s)
		}
	}
}

// TestHelpSummaryDocumentsReaffirm — the maintain summary entry must
// mention the v1.22.0 reaffirm/recheck upkeep flags (subflag rows — the
// usageOrder and seedCommands sets are unchanged).
func TestHelpSummaryDocumentsReaffirm(t *testing.T) {
	s := HelpSummary()
	for _, want := range []string{"reaffirm", "recheck"} {
		if !strings.Contains(s, want) {
			t.Errorf("helpSummary에 %q 누락: %s", want, s)
		}
	}
}

// TestHelpSummaryEnglish — HelpSummary is a single English output
// (no Korean residue — the pipe/grep convention). Completeness is covered by
// TestHelpSummaryCoversUsageOrder.
func TestHelpSummaryEnglish(t *testing.T) {
	for _, r := range HelpSummary() {
		if r >= '가' && r <= '힣' {
			t.Fatalf("HelpSummary에 한국어 잔존: %s", HelpSummary())
		}
	}
}

// TestHelpSummaryDocumentsAutoMode — the tuning summary entry must mention
// the v1.23.0 auto mode subcommand (`tuning auto off|shadow` — a subcommand
// row extension, the usageOrder and seedCommands sets unchanged).
func TestHelpSummaryDocumentsAutoMode(t *testing.T) {
	s := HelpSummary()
	for _, want := range []string{"auto", "off|shadow"} {
		if !strings.Contains(s, want) {
			t.Errorf("helpSummary에 %q 누락: %s", want, s)
		}
	}
}

// TestUsageLineAssembledFromOrder — usageLine is assembled exactly in
// usageOrder order (no literals, a Produces contract). The error key is
// abolished — the `{"usage":...}` guidance form (informational content unchanged).
func TestUsageLineAssembledFromOrder(t *testing.T) {
	want := `{"usage":"flint <` + strings.Join(usageOrder, "|") + `> [args]"}`
	if UsageLine() != want {
		t.Fatalf("usageLine이 usageOrder 조립과 불일치:\n got=%s\nwant=%s", UsageLine(), want)
	}
}

// TestHelpSummaryDocumentsMergeAudit — the maintain summary entry must
// mention the v1.24.0 merge-audit report (a subflag row extension — the
// usageOrder and seedCommands sets are unchanged).
func TestHelpSummaryDocumentsMergeAudit(t *testing.T) {
	s := HelpSummary()
	if !strings.Contains(s, "merge-audit") {
		t.Errorf("helpSummary에 merge-audit 누락: %s", s)
	}
}

// TestHelpSummaryDocumentsRecallAudit — the maintain summary entry must
// mention the v1.24.0 recall-audit report (a subflag row extension — the
// usageOrder and seedCommands sets are unchanged).
func TestHelpSummaryDocumentsRecallAudit(t *testing.T) {
	s := HelpSummary()
	if !strings.Contains(s, "recall-audit") {
		t.Errorf("helpSummary에 recall-audit 누락: %s", s)
	}
}
