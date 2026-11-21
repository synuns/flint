package db

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
)

// scratchDropFailures — scratch DB names whose cleanup DROP DATABASE
// failed, collected across the whole package run (the recording point
// is the newScratchTestRepo cleanup). TestMain prints them as one
// stderr warning after m.Run() — a residue alert, never a failure, so
// a server-side leftover cannot mask the tests' own verdict.
var scratchDropFailures []string

// scratchDropFailuresMu guards scratchDropFailures — parallel tests'
// cleanups can run concurrently.
var scratchDropFailuresMu sync.Mutex

// recordScratchDropFailure — called from the newScratchTestRepo
// cleanup when DROP DATABASE fails, so the leftover scratch name is
// reported once at the end of the run.
func recordScratchDropFailure(name string) {
	scratchDropFailuresMu.Lock()
	defer scratchDropFailuresMu.Unlock()
	scratchDropFailures = append(scratchDropFailures, name)
}

// scratchDropWarning — the single end-of-run stderr message for
// DROP-failed scratch DBs; empty string (no output) when every drop
// succeeded.
func scratchDropWarning(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("WARNING: lib/db tests: %d scratch DB(s) left on the server (DROP failed): %s\n",
		len(names), strings.Join(names, ", "))
}

// TestMain — runs the package unchanged (the no-DSN per-test skip in
// testDSN stays in effect), then emits one direct os.Stderr warning for
// any scratch DB whose DROP failed. It never changes the exit code.
func TestMain(m *testing.M) {
	code := m.Run()
	scratchDropFailuresMu.Lock()
	names := scratchDropFailures
	scratchDropFailuresMu.Unlock()
	if msg := scratchDropWarning(names); msg != "" {
		fmt.Fprint(os.Stderr, msg)
	}
	os.Exit(code)
}

// testDSN returns the DSN for slow tests. When the testDSNFromEnv guard
// is false, it skips (blocking the isolation blind spot of slow tests
// running against the operational DB under an operational FLINT_DSN
// environment).
// (Plan spelling TestDSN — kept unexported because go test forces
// func(t *testing.T) on Test* names.)
func testDSN(tb testing.TB) string {
	dsn, ok := testDSNFromEnv(os.Getenv("FLINT_TEST_DSN"), os.Getenv("FLINT_DSN"))
	if !ok {
		tb.Skip("no test DSN: set FLINT_TEST_DSN, or FLINT_DSN to a *test* database DSN")
	}
	return dsn
}

// testDSNFromEnv — resolves the slow-test DSN (a guard pure function —
// unit-test target). A non-empty FLINT_TEST_DSN is used as-is. The
// fallback FLINT_DSN can be an operational DSN, so unless the DB name
// (url.Path) contains the substring "test" it returns false and skips —
// a quiet skip in a wrong environment is safer than operational-DB
// pollution. An unparsable DSN is also treated as false (skip).
func testDSNFromEnv(testDsn, flintDsn string) (dsn string, ok bool) {
	if testDsn != "" {
		return testDsn, true
	}
	if flintDsn == "" {
		return "", false
	}
	u, err := url.Parse(flintDsn)
	if err != nil {
		return "", false
	}
	if !strings.Contains(strings.TrimPrefix(u.Path, "/"), "test") {
		return "", false
	}
	return flintDsn, true
}

// TestScratchDropWarningNone — the normal path (no DROP failure)
// produces no output: the warning formatter returns an empty string
// for an empty collection.
func TestScratchDropWarningNone(t *testing.T) {
	if got := scratchDropWarning(nil); got != "" {
		t.Fatalf("scratchDropWarning(nil) = %q, want empty", got)
	}
	if got := scratchDropWarning([]string{}); got != "" {
		t.Fatalf("scratchDropWarning(empty) = %q, want empty", got)
	}
}

// TestScratchDropWarningListsNames — the intentional no-DROP scenario:
// recorded scratch names surface in one stderr warning line listing
// every name.
func TestScratchDropWarningListsNames(t *testing.T) {
	got := scratchDropWarning([]string{"flint_scratch_deadbeef_1", "flint_scratch_cafef00d_2"})
	for _, want := range []string{"WARNING", "flint_scratch_deadbeef_1", "flint_scratch_cafef00d_2"} {
		if !strings.Contains(got, want) {
			t.Errorf("scratchDropWarning output %q missing %q", got, want)
		}
	}
}

// TestRecordScratchDropFailureCollects — the collection path used by
// the newScratchTestRepo cleanup: a recorded name is held for the
// single post-m.Run warning, without the recording test failing.
func TestRecordScratchDropFailureCollects(t *testing.T) {
	scratchDropFailuresMu.Lock()
	saved := scratchDropFailures
	scratchDropFailures = nil
	scratchDropFailuresMu.Unlock()
	t.Cleanup(func() {
		scratchDropFailuresMu.Lock()
		scratchDropFailures = saved
		scratchDropFailuresMu.Unlock()
	})

	recordScratchDropFailure("flint_scratch_abcd1234_9")

	scratchDropFailuresMu.Lock()
	names := append([]string(nil), scratchDropFailures...)
	scratchDropFailuresMu.Unlock()
	if len(names) != 1 || names[0] != "flint_scratch_abcd1234_9" {
		t.Fatalf("recorded scratch drop failures = %v, want [flint_scratch_abcd1234_9]", names)
	}
	if got := scratchDropWarning(names); !strings.Contains(got, "flint_scratch_abcd1234_9") {
		t.Errorf("warning %q missing the recorded name", got)
	}
}

// TestDSNReturnsEnv — with FLINT_TEST_DSN set, returns that value
// as-is.
func TestDSNReturnsEnv(t *testing.T) {
	want := "postgresql://flint:flint-dev@localhost:5435/flint-test"
	t.Setenv("FLINT_TEST_DSN", want)
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5433/flint")
	if got := testDSN(t); got != want {
		t.Fatalf("testDSN = %q, want %q", got, want)
	}
}

// TestDSNFromEnvGuard — the fallback FLINT_DSN guard unit tests (a
// defect regression): an operational DB name (flint, no "test"
// substring) skips; a test DB name passes.
func TestDSNFromEnvGuard(t *testing.T) {
	cases := []struct {
		name              string
		testDsn, flintDsn string
		wantDsn           string
		wantOK            bool
	}{
		{"test 우선 — 운영 FLINT_DSN 무시", "postgresql://f:f@h:5434/flint_test", "postgresql://flint:flint-dev@localhost:5433/flint", "postgresql://f:f@h:5434/flint_test", true},
		{"폴백 — DB명에 test 포함", "", "postgresql://flint:flint-dev@localhost:5434/flint_test", "postgresql://flint:flint-dev@localhost:5434/flint_test", true},
		{"폴백 — 운영 DB명은 스킵", "", "postgresql://flint:flint-dev@localhost:5433/flint", "", false},
		{"폴백 — 파싱 불가 DSN은 스킵", "", "://bad dsn", "", false},
		{"둘 다 비면 스킵", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := testDSNFromEnv(tc.testDsn, tc.flintDsn)
			if ok != tc.wantOK || got != tc.wantDsn {
				t.Errorf("testDSNFromEnv(%q, %q) = (%q, %v), want (%q, %v)",
					tc.testDsn, tc.flintDsn, got, ok, tc.wantDsn, tc.wantOK)
			}
		})
	}
}
