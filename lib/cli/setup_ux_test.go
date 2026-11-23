package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestSetupUXRenders(t *testing.T) {
	var b strings.Builder
	ux := newSetupUX(&b, true)
	ux.Step("Doltgres 연결")
	ux.Success(":5434 (12ms)")
	ux.Step("마이그레이션")
	ux.Success("v5")
	s := b.String()
	for _, want := range []string{"Doltgres", "5434", "v5"} {
		if !strings.Contains(s, want) {
			t.Errorf("출력에 %q 부재:\n%s", want, s)
		}
	}
}

func TestSetupUXNonTTY(t *testing.T) {
	var b strings.Builder
	ux := newSetupUX(&b, false)
	ux.Step("테스트")
	ux.Success("완료")
	if strings.Contains(b.String(), "\033[") {
		t.Error("non-TTY에 ANSI 코드 존재")
	}
}

func TestSetupJSONMode(t *testing.T) {
	withFake(t, &fakeStore{})
	rec := withFakeCompose(t)
	out, code := mustRun(t, []string{"setup", "--json"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(건전 DB ok); out=%s", code, out)
	}
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Fatalf("--json 출력은 한 줄이어야 한다: %q", out)
	}
	m := decode(t, out)
	if m["status"] != "ok" {
		t.Fatalf("status=%v, want ok; out=%s", m["status"], out)
	}
	if rec.upCalls != 1 {
		t.Fatalf("compose=%d회, want 1(동작 불변)", rec.upCalls)
	}

	plain, code2 := mustRun(t, []string{"setup"}, "")
	if code2 != code || strings.TrimSpace(plain) != strings.TrimSpace(out) {
		t.Fatalf("plain(--json 미지정, non-TTY) 불일치:\nplain=%q\njson =%q", plain, out)
	}
}

func TestSetupDegradedWhenStoreDown(t *testing.T) {
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return nil, fmt.Errorf("db down")
	}
	t.Cleanup(func() { newStore = prev })
	withFakeCompose(t)
	out, code := mustRun(t, []string{"setup", "--json"}, "")
	if code != 1 {
		t.Fatalf("exit=%d, want 1(degraded); out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "degraded" {
		t.Fatalf("status=%v, want degraded; out=%s", m["status"], out)
	}
	if nexts, _ := m["next"].([]any); len(nexts) == 0 {
		t.Fatal("next 안내 유실")
	}
}
