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

// TestNudgeStoreFree — Run("nudge")은 store 경유(v0.13.2 N5 갱신):
// newStore 시임을 호출하고 핸들러에 store가 주입된다(nudgeProfile가
// exposure_profile을 조회해 minimal 계약 유지). nudge 핸들러는 가짜로
// 등록(배선 자체를 검증 — 원래 구조 승계).
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
	// v0.34.1 T1c(race 게이트 선결함): 클린업에서 무조건 unregister하면
	// init이 등록한 원 nudge 핸들러까지 사라진다 — init()은 -count≥2에서
	// 재실행되지 않으므로 2회차부터 Lookup("nudge") 미등록·nudge계 테스트
	// 전체 오염. 원 핸들러를 캡처해 복원한다(부재 시에만 unregister).
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
