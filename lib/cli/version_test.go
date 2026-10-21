package cli

import (
	"context"
	"strings"
	"testing"

	"flint/lib/store"
)

// TestVersionJSON — 기본 dev 폴백 한 줄 JSON(ldflags 미주입 시).
func TestVersionJSON(t *testing.T) {
	f := &fakeStore{}
	withFake(t, f)
	out, code := mustRun(t, []string{"version"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["version"] != "dev" || m["commit"] != "dev" {
		t.Fatalf("version/commit=%v/%v, want dev/dev(기본 폴백)", m["version"], m["commit"])
	}
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Fatalf("한 줄 JSON이어야 한다: %q", out)
	}
	if got := VersionString(); got != strings.TrimSpace(out) {
		t.Fatalf("VersionString()=%q, want %q", got, strings.TrimSpace(out))
	}
}

// TestVersionRegistered — init() Register 확인.
func TestVersionRegistered(t *testing.T) {
	if _, ok := Lookup("version"); !ok {
		t.Fatal("Lookup(\"version\") 실패 — 미등록 커맨드")
	}
}

// TestVersionStoreFree — DB 다운(접속 실패)에도 version은 exit 0(§13 정합).
func TestVersionStoreFree(t *testing.T) {
	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return nil, store.ErrServerDown
	}
	t.Cleanup(func() { newStore = prev })
	out, code := mustRun(t, []string{"version"}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(DB 다운에도 version은 즉시 응답); out=%s", code, out)
	}
	if m := decode(t, out); m["version"] != "dev" {
		t.Fatalf("version=%v, want dev; out=%s", m["version"], out)
	}
}
