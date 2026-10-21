package cli

import (
	"context"
	"io"
	"testing"
)

// TestAllCommandsRegistered — v0.1 전 커맨드가 registry에 등록되어 있어야 한다
// (switch 제거 회귀 방지, T23 부채 회수).
func TestAllCommandsRegistered(t *testing.T) {
	for _, name := range []string{"write", "read", "hot", "status", "backup", "restore"} {
		if _, ok := Lookup(name); !ok {
			t.Errorf("Lookup(%q) 실패 — 미등록 커맨드", name)
		}
	}
}

// TestLookupUnknown — 미등록 이름은 false.
func TestLookupUnknown(t *testing.T) {
	if _, ok := Lookup("no-such-command"); ok {
		t.Fatal("미등록 커맨드가 Lookup 성공")
	}
}

// TestRegisterAddsCommand — Register 후 Lookup 가능(신규 커맨드 등록 경로).
func TestRegisterAddsCommand(t *testing.T) {
	Register("probe-cmd", func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int { return 0 })
	h, ok := Lookup("probe-cmd")
	if !ok || h == nil {
		t.Fatal("Register 후 Lookup 실패")
	}
	unregister("probe-cmd")
	if _, ok := Lookup("probe-cmd"); ok {
		t.Fatal("unregister 후에도 Lookup 성공")
	}
}
