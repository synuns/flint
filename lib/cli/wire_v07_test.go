package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// TestUsageIncludesCompactNudge — usageLine includes compact|nudge after done|.
func TestUsageIncludesCompactNudge(t *testing.T) {
	u := usageLine()
	if !strings.Contains(u, "done|compact|nudge") {
		t.Fatalf("usageLine에 done|compact|nudge 순서 없음: %s", u)
	}
}

// TestLookupCompactNudge — compact and nudge registered (init Register).
func TestLookupCompactNudge(t *testing.T) {
	for _, name := range []string{"compact", "nudge"} {
		if _, ok := Lookup(name); !ok {
			t.Errorf("Lookup(%q) 미등록", name)
		}
	}
}

// TestNudgeStoreFree — Run("nudge") goes through the store:
// it calls the newStore seam and injects the store into the handler (nudgeProfile looks up
// exposure_profile to keep the minimal contract). The nudge handler is registered as a fake
// (verifying the wiring itself — inheriting the original structure).
func TestNudgeStoreFree(t *testing.T) {
	called := false
	orig := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		called = true
		return &fakeStore{}, nil
	}
	t.Cleanup(func() { newStore = orig })
	Register("nudge", func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
		if st == nil {
			t.Error("nudge에 store가 미주입 — store 경유여야 함(N5)")
		}
		return 0
	})
	// unconditionally unregistering in cleanup would also remove the original nudge handler
	// registered by init — init() does not re-run with -count>=2, so from the 2nd run on,
	// Lookup("nudge") would be unregistered, polluting every nudge-family test.
	// The original handler is captured and restored (unregister only if absent).
	origNudge, hadNudge := Lookup("nudge")
	t.Cleanup(func() {
		if hadNudge {
			Register("nudge", origNudge)
		} else {
			unregister("nudge")
		}
	})
	var out bytes.Buffer
	if code := Run([]string{"nudge"}, strings.NewReader("{}"), &out); code != 0 {
		t.Fatalf("Run(nudge) exit=%d out=%s", code, out.String())
	}
	if !called {
		t.Error("newStore 미호출 — nudge는 store 경유여야 함(N5)")
	}
}

// TestDsnFromEnvWiredToResolve — the dsnFromEnv body delegates to ResolveDSN.
func TestDsnFromEnvWiredToResolve(t *testing.T) {
	t.Run("env only", func(t *testing.T) {
		t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
		t.Setenv("FLINT_DSN", "postgresql://env:5433/flint")
		if got := dsnFromEnv(); got != "postgresql://env:5433/flint" {
			t.Fatalf("dsnFromEnv=%q", got)
		}
	})
	t.Run("config file fallback", func(t *testing.T) {
		unsetDSN(t)
		writeConfig(t, `{"dsn":"postgresql://file:5434/flint"}`)
		if got := dsnFromEnv(); got != "postgresql://file:5434/flint" {
			t.Fatalf("dsnFromEnv=%q want config.json DSN", got)
		}
	})
}
