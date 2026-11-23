package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/store"
)

func runEntryArgs(t *testing.T, f Store, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := runReportEntry(context.Background(), f, args, strings.NewReader(""), &out)
	return out.String(), code
}

func TestReportEntryFlagPathUnchanged(t *testing.T) {
	out, code := runEntryArgs(t, newReportFake(nil))
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(out, `"days"`) {
		t.Errorf("default JSON path output changed: %q", out)
	}
}

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

func TestReportEntryTUINonTerminalFallback(t *testing.T) {
	orig := reportTUIFn
	reportTUIFn = nil
	defer func() { reportTUIFn = orig }()

	out, code := runEntryArgs(t, newReportFake(nil), "--tui")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	if !strings.Contains(out, "TUI requires a terminal") {
		t.Errorf("fallback should mention terminal requirement: %q", out)
	}
}

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

func TestReportEntryTUIBadFlagStillValidated(t *testing.T) {
	out, code := runEntryArgs(t, newReportFake(nil), "--tui", "--days", "abc")
	if code != 3 {
		t.Fatalf("exit=%d, want 3; out=%s", code, out)
	}
	if !strings.Contains(out, `"error"`) {
		t.Errorf("invalid flag should surface error JSON: %q", out)
	}
}
