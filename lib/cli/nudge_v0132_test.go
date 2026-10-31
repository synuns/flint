// nudge_v0132_test.go — v0.13.2 심층 리뷰 회귀(N5): nudge store 경유 전환.
// DB 미가용 시 에러 JSON 없이 조용히 MapExit(server_down=2) 종료만.
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"flint/lib/store"
)

// TestNudgeStoreDownQuiet — store 접속 실패(DB 다운) 시 Run("nudge")은
// stdout에 에러 JSON을 흘리지 않고(훅 quiet 계약) exit 2로만 종료한다.
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
