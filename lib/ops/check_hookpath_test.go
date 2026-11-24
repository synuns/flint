package ops

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type fakeFileInfo struct{ mode os.FileMode }

func (f fakeFileInfo) Name() string       { return "flint" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

func fakeStatMode(mode os.FileMode, _ bool) os.FileInfo { return fakeFileInfo{mode: mode} }

func withHookPathSeams(t *testing.T, cmds []string, lookup func(string) (string, error), stat func(string) (os.FileInfo, error)) {
	t.Helper()
	prevCmds, prevLookup, prevStat := HookPathCommandsFn, HookPathLookupFn, HookPathStatFn
	HookPathCommandsFn = func() ([]string, error) { return cmds, nil }
	HookPathLookupFn = lookup
	HookPathStatFn = stat
	t.Cleanup(func() { HookPathCommandsFn, HookPathLookupFn, HookPathStatFn = prevCmds, prevLookup, prevStat })
}

func makeTempHookBin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "flint")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHookPathResolution(t *testing.T) {
	bin := makeTempHookBin(t)

	passCases := []struct {
		name string
		cmds []string
	}{
		{"absolute-path-exists", []string{fmt.Sprintf(`eval "%s session-start --session x" 2>/dev/null || true`, shQuote(bin))}},
		{"bare-flint-on-path", []string{`flint session-end 2>/dev/null`}},
	}
	for _, tc := range passCases {
		t.Run(tc.name+"/pass", func(t *testing.T) {
			withHookPathSeams(t, tc.cmds,
				func(name string) (string, error) { return "/usr/local/bin/" + name, nil },
				func(path string) (os.FileInfo, error) { return fakeStatMode(0o755, false), nil })
			c := CheckHookPath()
			if c.Name != "hook-path" || c.Status != "pass" {
				t.Fatalf("got %+v, want name=hook-path status=pass", c)
			}
		})
	}

	failCases := []struct {
		name    string
		cmds    []string
		lookup  func(string) (string, error)
		stat    func(string) (os.FileInfo, error)
		wantSub string
	}{
		{"absolute-path-missing", []string{`eval "'/nonexistent/flint' session-start" 2>/dev/null || true`},
			func(string) (string, error) { return "", errors.New("unexpected lookup") },
			func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
			"unresolved"},
		{"absolute-path-not-executable", []string{`eval "'/opt/flint' session-end" 2>/dev/null || true`},
			func(string) (string, error) { return "", errors.New("unexpected lookup") },
			func(string) (os.FileInfo, error) { return fakeStatMode(0o644, false), nil },
			"unresolved"},
		{"bare-flint-not-on-path", []string{`flint compact`},
			func(string) (string, error) { return "", exec.ErrNotFound },
			func(string) (os.FileInfo, error) { return nil, errors.New("unexpected stat") },
			"unresolved"},
	}
	for _, tc := range failCases {
		t.Run(tc.name+"/fail", func(t *testing.T) {
			withHookPathSeams(t, tc.cmds, tc.lookup, tc.stat)
			c := CheckHookPath()
			if c.Status != "fail" {
				t.Fatalf("status=%q, want fail", c.Status)
			}
			if !strings.Contains(c.Detail, tc.wantSub) {
				t.Fatalf("detail %q, want substring %q", c.Detail, tc.wantSub)
			}
			if !strings.HasPrefix(c.Fix, "fix:") {
				t.Fatalf("fix=%q, want guidance", c.Fix)
			}
		})
	}
}

func TestNoLeak(t *testing.T) {
	secret := "s3cret"
	fullDSN := "postgresql://flint:" + secret + "@no-such-host.invalid:5434/flint"

	prev := DSNClassDSNFn
	DSNClassDSNFn = func() string { return fullDSN }
	defer func() { DSNClassDSNFn = prev }()

	prevL := DSNClassLookupFn
	DSNClassLookupFn = func(context.Context, string) error { return errors.New("lookup no such host") }
	defer func() { DSNClassLookupFn = prevL }()

	c := CheckDSNClass()
	for _, leak := range []string{secret, fullDSN, "flint:" + secret} {
		if strings.Contains(c.Detail, leak) {
			t.Fatalf("detail %q leaks %q", c.Detail, leak)
		}
	}

	bin := makeTempHookBin(t)
	hookCmd := fmt.Sprintf(`eval "%s session-start" 2>/dev/null || true`, shQuote(bin))
	withHookPathSeams(t, []string{hookCmd},
		func(string) (string, error) { return "", exec.ErrNotFound },
		func(string) (os.FileInfo, error) { return nil, os.ErrNotExist })
	hc := CheckHookPath()
	if strings.Contains(hc.Detail, string(filepath.Separator)+string(filepath.Separator)) {
		t.Fatalf("hook-path detail suspicious: %q", hc.Detail)
	}
}

func TestHookPathDetailNoAbsolutePath(t *testing.T) {
	home := filepath.Join(string(filepath.Separator)+"Users", "demo-user", ".claude")
	prev := HookPathCommandsFn
	HookPathCommandsFn = func() ([]string, error) {
		_, ferr := os.ReadFile(filepath.Join(home, "settings.json"))
		return nil, ferr
	}
	t.Cleanup(func() { HookPathCommandsFn = prev })

	c := CheckHookPath()
	if c.Status != "fail" {
		t.Fatalf("status=%q, want fail", c.Status)
	}
	if strings.Contains(c.Detail, string(filepath.Separator)) {
		t.Fatalf("detail %q leaks an absolute path", c.Detail)
	}
	if !strings.Contains(c.Detail, "unreadable") {
		t.Fatalf("detail %q lost the failure reason", c.Detail)
	}
}

func TestDSNLookupDeadlineCtx(t *testing.T) {
	prevCtx := DSNClassContextFn
	DSNClassContextFn = func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx, cancel
	}
	t.Cleanup(func() { DSNClassContextFn = prevCtx })

	withDSNSeams(t, "postgresql://flint:pw@no-such-host.invalid:5434/flint", DSNClassLookupFn, refuseDial)

	start := time.Now()
	c := CheckDSNClass()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("canceled-ctx lookup took %s, want immediate", elapsed)
	}
	if c.Status != "fail" {
		t.Fatalf("status=%q, want fail (%s)", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "dsn defect") {
		t.Fatalf("detail %q, want dsn-defect classification", c.Detail)
	}
}

func withDSNSeams(t *testing.T, dsn string, lookup func(context.Context, string) error, dial func(context.Context, string, string) error) {
	t.Helper()
	prevDSN, prevL, prevD := DSNClassDSNFn, DSNClassLookupFn, DSNClassDialFn
	DSNClassDSNFn = func() string { return dsn }
	DSNClassLookupFn = lookup
	DSNClassDialFn = dial
	t.Cleanup(func() { DSNClassDSNFn, DSNClassLookupFn, DSNClassDialFn = prevDSN, prevL, prevD })
}

func refuseDial(context.Context, string, string) error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
}

func TestDSNClassify(t *testing.T) {
	t.Run("engine-down-connection-refused", func(t *testing.T) {
		withDSNSeams(t, "postgresql://flint:pw@127.0.0.1:5434/flint",
			func(context.Context, string) error { return nil }, refuseDial)
		c := CheckDSNClass()
		if c.Status != "fail" {
			t.Fatalf("status=%q, want fail", c.Status)
		}
		if !strings.Contains(c.Detail, "engine down") {
			t.Fatalf("detail %q, want engine-down classification", c.Detail)
		}
		if !strings.HasPrefix(c.Fix, "fix:") {
			t.Fatalf("fix=%q, want guidance", c.Fix)
		}
	})

	t.Run("dsn-defect-unresolvable-host", func(t *testing.T) {
		withDSNSeams(t, "postgresql://flint:pw@no-such-host.invalid:5434/flint",
			func(context.Context, string) error { return errors.New("no such host") }, refuseDial)
		c := CheckDSNClass()
		if c.Status != "fail" {
			t.Fatalf("status=%q, want fail", c.Status)
		}
		if !strings.Contains(c.Detail, "dsn defect") {
			t.Fatalf("detail %q, want dsn-defect classification", c.Detail)
		}
		if strings.Contains(c.Detail, "engine down") {
			t.Fatalf("dsn defect must not be classified as engine down: %q", c.Detail)
		}
	})

	t.Run("dsn-defect-malformed", func(t *testing.T) {
		withDSNSeams(t, "not a url at all",
			func(context.Context, string) error { return nil }, refuseDial)
		c := CheckDSNClass()
		if c.Status != "fail" {
			t.Fatalf("status=%q, want fail", c.Status)
		}
		if !strings.Contains(c.Detail, "malformed") {
			t.Fatalf("detail %q, want malformed classification", c.Detail)
		}
	})

	t.Run("reachable-pass", func(t *testing.T) {
		withDSNSeams(t, "postgresql://flint:pw@127.0.0.1:5434/flint",
			func(context.Context, string) error { return nil },
			func(context.Context, string, string) error { return nil })
		c := CheckDSNClass()
		if c.Status != "pass" {
			t.Fatalf("status=%q, want pass", c.Status)
		}
	})
}
