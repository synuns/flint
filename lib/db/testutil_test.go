package db

import (
	"net/url"
	"os"
	"strings"
	"testing"
)

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
