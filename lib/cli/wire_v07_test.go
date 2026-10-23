// wire_v07_test.go — T74 Review Focus: test_usage_includes_compact_nudge ·
// test_lookup_compact_nudge · test_nudge_store_free · dsnFromEnv→ResolveDSN 배선.
package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// TestUsageIncludesCompactNudge — usageLine에 done| 뒤 compact|nudge.
func TestUsageIncludesCompactNudge(t *testing.T) {
	u := usageLine()
	if !strings.Contains(u, "done|compact|nudge") {
		t.Fatalf("usageLine에 done|compact|nudge 순서 없음: %s", u)
	}
}

// TestLookupCompactNudge — compact·nudge 등록(T72·T73 init Register).
func TestLookupCompactNudge(t *testing.T) {
	for _, name := range []string{"compact", "nudge"} {
		if _, ok := Lookup(name); !ok {
			t.Errorf("Lookup(%q) 미등록", name)
		}
	}
}

// TestNudgeStoreFree — Run("nudge")는 newStore 시임을 호출하지 않는다.
// nudge 핸들러는 가짜로 등록(T73 커밋 전에도 배선 자체를 검증).
func TestNudgeStoreFree(t *testing.T) {
	called := false
	orig := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		called = true
		return nil, nil
	}
	t.Cleanup(func() { newStore = orig })
	Register("nudge", func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
		if st != nil {
			t.Error("nudge에 store가 주입됨 — store-free여야 함")
		}
		return 0
	})
	t.Cleanup(func() { unregister("nudge") })
	var out bytes.Buffer
	if code := Run([]string{"nudge"}, strings.NewReader("{}"), &out); code != 0 {
		t.Fatalf("Run(nudge) exit=%d out=%s", code, out.String())
	}
	if called {
		t.Error("newStore 호출됨 — nudge는 store-free여야 함")
	}
}

// TestDsnFromEnvWiredToResolve — dsnFromEnv 본체가 ResolveDSN 위임.
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
