package cli

import (
	"context"
	"strings"
	"testing"
)

func TestSeamRealDSNGate(t *testing.T) {
	t.Setenv("SUPERLITE_REAL", "1")
	t.Setenv("FLINT_TEST_DSN", "")
	t.Setenv("FLINT_DSN", "")
	dsn, real := seamRealDSN()
	if real || dsn != "" {
		t.Fatalf("seamRealDSN = (%q, %v), want (\"\", false) — 미설정 시 fake 폴백", dsn, real)
	}

	f := &fakeStore{}
	withFake(t, f)
	st, err := newStore(context.Background(), "unused")
	if err != nil {
		t.Fatalf("newStore(fake): %v", err)
	}
	if got, ok := st.(*fakeStore); !ok || got != f {
		t.Fatalf("설치된 store = %T, want *fakeStore — 실구현 경로로 동작함", st)
	}

	const want = "postgresql://flint:t@h:5434/flint_ci"
	t.Setenv("FLINT_TEST_DSN", want)
	if dsn, real := seamRealDSN(); !real || dsn != want {
		t.Fatalf("seamRealDSN = (%q, %v), want (%q, true)", dsn, real, want)
	}
}

func TestSeamFakePathRunsWithoutRealDSN(t *testing.T) {
	t.Setenv("SUPERLITE_REAL", "1")
	t.Setenv("FLINT_TEST_DSN", "")
	t.Setenv("FLINT_DSN", "")
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
