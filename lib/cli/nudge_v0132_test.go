// nudge_v0132_test.go — the nudge store-based transition.
// When the DB is unavailable, it ends with only a quiet MapExit (server_down=2), no error JSON.
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// TestNudgeStoreDownQuiet — on a store connection failure (DB down), Run("nudge")
// ends with exit 2 only, without leaking an error JSON to stdout (the hook quiet contract).
func TestNudgeStoreDownQuiet(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return nil, store.ErrServerDown
	}
	t.Cleanup(func() { newStore = prev })

	var out bytes.Buffer
	code := Run([]string{"nudge"}, strings.NewReader(`{"hook_event_name":"Stop"}`), &out)
	if code != 2 {
		t.Fatalf("exit=%d, want 2(server_down)", code)
	}
	if out.String() != "" {
		t.Fatalf("stdout=%q, want 빈 문자열(에러 JSON 누출)", out.String())
	}
}
