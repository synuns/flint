// report_tui_test.go — tests for the report --tui entry branching.
// protocol: without the --tui opt-in, the existing 3 views (JSON, text, HTML) are unchanged;
// --tui combination errors exit 3; non-terminal fallback exits 3; collection delegation and error propagation.
// The collection producer (ops.RunReport) is the real implementation via a fake Store (same path with
// SUPERLITE_REAL=1); the screen launch (reportTUIFn) is a stub seam.
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/store"
)

// runEntryArgs — a helper running the entry wrapper (runReportEntry) directly.
func runEntryArgs(t *testing.T, f Store, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := runReportEntry(context.Background(), f, args, strings.NewReader(""), &out)
	return out.String(), code
}

// TestReportEntryFlagPathUnchanged — without --tui, byte-identical delegation to the existing JSON path
// (default path unchanged).
func TestReportEntryFlagPathUnchanged(t *testing.T) {
	out, code := runEntryArgs(t, newReportFake(nil))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"days"`) {
		t.Errorf("default JSON path output changed: %q", out)
	}
}

// TestReportEntryTUIExclusive — --tui cannot combine with other view flags
// (preventing screen/file output duality) — exit 3 error JSON.
func TestReportEntryTUIExclusive(t *testing.T) {
	for _, args := range [][]string{
		{"--tui", "--html"},
		{"--tui", "--text"},
		{"--tui", "--out", "x.html"},
	} {
		out, code := runEntryArgs(t, newReportFake(nil), args...)
		if code != 3 {
			t.Errorf("%v: exit=%d, want 3 (out %q)", args, code, out)
		}
		if !strings.Contains(out, `"error"`) || !strings.Contains(out, "--tui") {
			t.Errorf("%v: error JSON should name --tui conflict: %q", args, out)
		}
	}
}

// TestReportEntryTUIRunsScreen — --tui collects (real implementation — fake Store) and then
// passes the ReportOutput and window days to the screen model. It leaves no JSON on
// stdout (view-only).
func TestReportEntryTUIRunsScreen(t *testing.T) {
	daily := map[string]string{}
	for _, d := range reportDates(3) {
		daily["daily:"+d] = `{"write":1,"read":2,"dup":0,"hot":1}`
	}
	f := newReportFake(daily)

	orig := reportTUIFn
	var gotRep ops.ReportOutput
	var gotDays int
	reportTUIFn = func(rep ops.ReportOutput, days int) error {
		gotRep, gotDays = rep, days
		return nil
	}
	defer func() { reportTUIFn = orig }()

	out, code := runEntryArgs(t, f, "--tui", "--days", "3")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(gotRep.Days) != 3 {
		t.Errorf("screen got %d day rows, want 3 (collection reuse)", len(gotRep.Days))
	}
	if gotDays != 3 {
		t.Errorf("screen got days=%d, want 3 (window from collected rows)", gotDays)
	}
	if strings.Contains(out, `"days"`) {
		t.Errorf("TUI path must not print JSON to stdout: %q", out)
	}
}

// TestReportEntryTUINonTerminalFallback — on non-terminal (pipe, CI), a screen-launch
// error is the exit 3 guidance fallback (the contract wording).
func TestReportEntryTUINonTerminalFallback(t *testing.T) {
	orig := reportTUIFn
	reportTUIFn = nil // keep-the-real-implementation convention: nil means use the default
	defer func() { reportTUIFn = orig }()

	out, code := runEntryArgs(t, newReportFake(nil), "--tui")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	if !strings.Contains(out, "TUI requires a terminal") {
		t.Errorf("fallback should mention terminal requirement: %q", out)
	}
}

// TestReportEntryTUICollectErrorPropagates — a collection-stage error (server down) propagates
// with the same error and exit as the existing JSON path (2).
func TestReportEntryTUICollectErrorPropagates(t *testing.T) {
	f := &fakeStore{pingErr: store.ErrServerDown}
	orig := reportTUIFn
	called := false
	reportTUIFn = func(ops.ReportOutput, int) error { called = true; return nil }
	defer func() { reportTUIFn = orig }()

	out, code := runEntryArgs(t, f, "--tui")
	if code != 2 {
		t.Fatalf("exit=%d, want 2; out=%s", code, out)
	}
	if !strings.Contains(out, "server_down") {
		t.Errorf("collect error not propagated: %q", out)
	}
	if called {
		t.Error("screen must not launch when collection fails")
	}
}

// TestReportEntryTUIBadFlagStillValidated — an invalid flag used together with --tui is still
// handled by the existing argument-validation path (exit 3).
func TestReportEntryTUIBadFlagStillValidated(t *testing.T) {
	out, code := runEntryArgs(t, newReportFake(nil), "--tui", "--days", "abc")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	if !strings.Contains(out, `"error"`) {
		t.Errorf("invalid flag should surface error JSON: %q", out)
	}
}
