package db

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
)

var scratchDropFailures []string

var scratchDropFailuresMu sync.Mutex

func recordScratchDropFailure(name string) {
	scratchDropFailuresMu.Lock()
	defer scratchDropFailuresMu.Unlock()
	scratchDropFailures = append(scratchDropFailures, name)
}

func scratchDropWarning(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("WARNING: lib/db tests: %d scratch DB(s) left on the server (DROP failed): %s\n",
		len(names), strings.Join(names, ", "))
}

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

func testDSN(tb testing.TB) string {
	dsn, ok := testDSNFromEnv(os.Getenv("FLINT_TEST_DSN"), os.Getenv("FLINT_DSN"))
	if !ok {
		tb.Skip("no test DSN: set FLINT_TEST_DSN, or FLINT_DSN to a *test* database DSN")
	}
	return dsn
}

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

func TestScratchDropWarningNone(t *testing.T) {
	if got := scratchDropWarning(nil); got != "" {
		t.Fatalf("scratchDropWarning(nil) = %q, want empty", got)
	}
	if got := scratchDropWarning([]string{}); got != "" {
		t.Fatalf("scratchDropWarning(empty) = %q, want empty", got)
	}
}

func TestScratchDropWarningListsNames(t *testing.T) {
	got := scratchDropWarning([]string{"flint_scratch_deadbeef_1", "flint_scratch_cafef00d_2"})
	for _, want := range []string{"WARNING", "flint_scratch_deadbeef_1", "flint_scratch_cafef00d_2"} {
		if !strings.Contains(got, want) {
			t.Errorf("scratchDropWarning output %q missing %q", got, want)
		}
	}
}

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

func TestDSNReturnsEnv(t *testing.T) {
	want := "postgresql://flint:flint-dev@localhost:5435/flint-test"
	t.Setenv("FLINT_TEST_DSN", want)
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5433/flint")
	if got := testDSN(t); got != want {
		t.Fatalf("testDSN = %q, want %q", got, want)
	}
}

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
