package db

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/synuns/flint/internal/testengine"
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
	testengine.RunCleanup(context.Background())
	scratchDropFailuresMu.Lock()
	names := scratchDropFailures
	scratchDropFailuresMu.Unlock()
	if msg := scratchDropWarning(names); msg != "" {
		fmt.Fprint(os.Stderr, msg)
	}
	os.Exit(code)
}

func testDSN(tb testing.TB) string {
	return testengine.ResolveDSN()
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

func TestDSNIgnoresOpsFLINTDSN(t *testing.T) {
	t.Setenv("FLINT_TEST_DSN", "")
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5434/flint_test")
	if got := testDSN(t); got != testengine.TestDSNDefault() {
		t.Errorf("testDSN = %q, want 전용 엔진 기본값 %q(운영 FLINT_DSN 폴백 없음)", got, testengine.TestDSNDefault())
	}
}
