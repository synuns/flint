package testengine

import (
	"os"
	"testing"
)

func unset(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if had {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
}

func TestResolveDSNEnvOverride(t *testing.T) {
	t.Setenv("FLINT_TEST_DSN", "postgresql://u:secret@testhost:6000/db")
	if got := ResolveDSN(); got != "postgresql://u:secret@testhost:6000/db" {
		t.Fatalf("FLINT_TEST_DSN verbatim 실험: %q", got)
	}
}

func TestResolveDSNIgnoresOperationalDSN(t *testing.T) {
	unset(t, "FLINT_TEST_DSN")
	t.Setenv("FLINT_DSN", "postgresql://flint:flint-dev@localhost:5434/flint")
	if got := ResolveDSN(); got != TestDSNDefault() {
		t.Fatalf("FLINT_DSN 무시 실험: %q", got)
	}
}

func TestResolveDSNDefaultWhenUnset(t *testing.T) {
	unset(t, "FLINT_TEST_DSN")
	unset(t, "FLINT_DSN")
	if got := ResolveDSN(); got != TestDSNDefault() {
		t.Fatalf("기본값 실험: %q", got)
	}
}

func TestDSNDefaultIsDedicatedEngine(t *testing.T) {
	want := "postgresql://flint:pw@127.0.0.1:5499/flint_test"
	if got := TestDSNDefault(); got != want {
		t.Fatalf("전용 엔진 기본값 부정합: %q != %q", got, want)
	}
}
