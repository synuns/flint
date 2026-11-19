// report_html_exit_test.go — report --html exit-code consistency.
// Root cause: the runReportHTML collection path returned hardcoded 1 for Ping failures
// and every collection-step error — while the JSON path (RunReport) uses store.MapExit
// (server_down -> 2, SchemaError -> 3), so the same command's two views emitted
// different codes for the same failure. The HTML path now reuses MapExit, aligning the contract.
// Note: RunReport's entry Ping pre-check already used MapExit, so the upper path passed by
// luck — the defect is in the direct runReportHTML path (collection steps, internal Ping),
// so the unit tests call runReportHTML directly.
// Review point: exit 3 here is separate from the argument-validation failure (the RunReport
// flag parser). The 3 here occurs only when 'the store returns a SchemaError-family
// error' — it comes from MapExit, so it means the same as the JSON path.
package ops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// htmlExitFake — the minimal fake for exit-mapping verification (reusing the fakeStore
// conventions): only the pingErr and queryErr stubs are set.
func htmlExitCode(t *testing.T, f *fakeStore) int {
	t.Helper()
	t.Chdir(t.TempDir()) // isolate the default report.html output
	var out strings.Builder
	return runReportHTML(context.Background(), f, 7, "report.html", &out)
}

// TestHTMLExitServerDown — a Ping failure (server down) exits 2 like the JSON path.
// Before the fix it was the hardcoded 1 (demonstrating the failure).
func TestHTMLExitServerDown(t *testing.T) {
	if code := htmlExitCode(t, &fakeStore{pingErr: store.ErrServerDown}); code != 2 {
		t.Fatalf("report --html 서버 다운 exit=%d, want 2", code)
	}
}

// TestHTMLExitSchemaMismatch — a SchemaError-family error during collection (including
// a schema-version mismatch) exits 3 like the JSON path.
func TestHTMLExitSchemaMismatch(t *testing.T) {
	if code := htmlExitCode(t,
		&fakeStore{queryErr: &store.SchemaError{Field: "schema_version", Expect: "v9"}}); code != 3 {
		t.Fatalf("report --html 스키마 불일치 exit=%d, want 3", code)
	}
}

// TestHTMLExitOtherCollectionErrorStays1 — other collection errors keep the existing 1
// (the contract: only the server_down and SchemaError mappings change).
func TestHTMLExitOtherCollectionErrorStays1(t *testing.T) {
	if code := htmlExitCode(t,
		&fakeStore{queryErr: errors.New("synthetic: query boom")}); code != 1 {
		t.Fatalf("report --html 일반 수집 오류 exit=%d, want 1", code)
	}
}

// TestHTMLExitViaRunReportMatchesJSON — end-to-end comparison: the same failure gets
// the same code via the RunReport --html entry (the entry Ping MapExit + a pass-through
// path regression guard). Matching the JSON view's value is this task's contract.
func TestHTMLExitViaRunReportMatchesJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	cases := []struct {
		name string
		f    *fakeStore
		want int
	}{
		{"server_down", &fakeStore{pingErr: store.ErrServerDown}, 2},
		{"schema", &fakeStore{queryErr: &store.SchemaError{Field: "schema_version", Expect: "v9"}}, 3},
		{"other", &fakeStore{queryErr: errors.New("synthetic: boom")}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out strings.Builder
			if code := RunReport(context.Background(), c.f, []string{"--html"}, nil, &out); code != c.want {
				t.Fatalf("RunReport --html %s exit=%d, want %d (출력 %s)", c.name, code, c.want, out.String())
			}
		})
	}
}
