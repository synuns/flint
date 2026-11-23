package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/synuns/flint/internal/testengine"
)

func TestSeamRealDSNGate(t *testing.T) {
	t.Setenv("SUPERLITE_REAL", "1")
	t.Setenv("FLINT_TEST_DSN", "")
	t.Setenv("FLINT_DSN", "")
	dsn, real := seamRealDSN()
	if !real || dsn != testengine.TestDSNDefault() {
		t.Fatalf("seamRealDSN = (%q, %v), want (%q, true) — 전용 엔진 폴백", dsn, real, testengine.TestDSNDefault())
	}

	const want = "postgresql://flint:t@h:5434/flint_ci"
	t.Setenv("FLINT_TEST_DSN", want)
	if dsn, real := seamRealDSN(); !real || dsn != want {
		t.Fatalf("seamRealDSN = (%q, %v), want (%q, true)", dsn, real, want)
	}
}

func TestSeamFakePathRunsWithoutRealGate(t *testing.T) {
	os.Unsetenv("SUPERLITE_REAL")
	f := &fakeStore{}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"n1","summary":"seam gate","confidence":0.9}`)
	if code != 0 || !strings.Contains(out, `"status":"committed"`) {
		t.Fatalf("exit=%d out=%s — fake 경로가 깨짐", code, out)
	}
	if len(f.inserted) != 1 {
		t.Fatalf("fake 삽입 기록=%d건, want 1", len(f.inserted))
	}
}
