package ops

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

const recallActiveMarkerTestKey = "recall_active:sess-t4"

type recallSessionFixture struct {
	st      *fakeStore
	mu      sync.Mutex
	markers map[string]string
	calls   int
	hotUsed int
	panics  bool
	lines   []string
}

func newRecallSessionFixture(t *testing.T) *recallSessionFixture {
	t.Helper()
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	t.Setenv("FLINT_CLAUDE_HOME", t.TempDir())
	injectSessionLabelFn(t)
	prevCur, prevValid := CurrentVersionFn, ValidVersionFn
	CurrentVersionFn, ValidVersionFn = nil, nil
	t.Cleanup(func() { CurrentVersionFn, ValidVersionFn = prevCur, prevValid })

	f := &recallSessionFixture{markers: map[string]string{}}
	f.st = &fakeStore{}
	f.st.queryFn = func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "SELECT value FROM meta WHERE key"):
			key := ""
			if len(args) > 0 {
				key = fmtString(args[0])
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if v, ok := f.markers[key]; ok {
				return [][]any{{v}}, nil
			}
			return nil, nil
		case strings.Contains(sql, "INSERT INTO meta"):
			key, val := "", ""
			if len(args) >= 2 {
				key, val = fmtString(args[0]), fmtString(args[1])
			}
			f.mu.Lock()
			f.markers[key] = val
			f.mu.Unlock()
			return nil, nil
		}
		return nil, nil
	}

	prevHot, prevInject := runSessionHotFn, injectActiveRecallFn
	t.Cleanup(func() { runSessionHotFn, injectActiveRecallFn = prevHot, prevInject })
	runSessionHotFn = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		return 0
	}
	injectActiveRecallFn = func(ctx context.Context, st wire.Store, session string, stdout io.Writer, hotUsed int) int {
		f.mu.Lock()
		f.calls++
		f.hotUsed = hotUsed
		panics := f.panics
		lines := append([]string{}, f.lines...)
		f.mu.Unlock()
		if panics {
			panic("inject exploded")
		}
		for _, l := range lines {
			fmt.Fprintln(stdout, l)
		}
		return 0
	}
	return f
}

func TestSessionStartInjectOnce(t *testing.T) {
	f := newRecallSessionFixture(t)
	f.mu.Lock()
	f.lines = []string{"recall-active|id=7|demo"}
	f.mu.Unlock()

	var first, second bytes.Buffer
	if code := RunSessionStart(context.Background(), f.st, []string{"--session", "sess-t4"}, strings.NewReader(""), &first); code != 0 {
		t.Fatalf("first exit = %d", code)
	}
	if code := RunSessionStart(context.Background(), f.st, []string{"--session", "sess-t4"}, strings.NewReader(""), &second); code != 0 {
		t.Fatalf("second exit = %d", code)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls != 1 {
		t.Fatalf("inject calls = %d, want 1 (once per session)", f.calls)
	}
	if _, ok := f.markers[recallActiveMarkerTestKey]; !ok {
		t.Fatalf("guard marker missing: %v", f.markers)
	}
	if !strings.Contains(first.String(), "recall-active|id=7") {
		t.Fatalf("first run missing injection row: %q", first.String())
	}
	if strings.Contains(second.String(), "recall-active|id=7") {
		t.Fatalf("second run must not inject: %q", second.String())
	}
}

func TestSessionStartQuietPath(t *testing.T) {
	f := newRecallSessionFixture(t)
	var out bytes.Buffer
	if code := RunSessionStart(context.Background(), f.st, []string{"--session", "sess-quiet"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(out.String(), "recall-active") {
		t.Fatalf("no burst must keep output unchanged: %q", out.String())
	}

	var baseline bytes.Buffer
	prevInject := injectActiveRecallFn
	injectActiveRecallFn = func(ctx context.Context, st wire.Store, session string, stdout io.Writer, hotUsed int) int { return 0 }
	code := RunSessionStart(context.Background(), f.st, []string{"--session", "sess-quiet2"}, strings.NewReader(""), &baseline)
	injectActiveRecallFn = prevInject
	if code != 0 {
		t.Fatalf("baseline exit = %d", code)
	}
	if out.String() != baseline.String() {
		t.Fatalf("quiet output diverges from baseline:\n got %q\nwant %q", out.String(), baseline.String())
	}
}

func TestSessionStartHookNonBlocking(t *testing.T) {
	f := newRecallSessionFixture(t)
	f.mu.Lock()
	f.panics = true
	f.mu.Unlock()

	var out bytes.Buffer
	if code := RunSessionStart(context.Background(), f.st, []string{"--session", "sess-panic"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("panic in injection must not fail session-start, exit = %d: %q", code, out.String())
	}
	if strings.Contains(out.String(), "inject exploded") {
		t.Fatalf("panic leaked to output: %q", out.String())
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls != 1 {
		t.Fatalf("inject calls = %d, want 1", f.calls)
	}
}

func TestSessionStartHotBytesReachInjector(t *testing.T) {
	f := newRecallSessionFixture(t)
	prevHot := runSessionHotFn
	t.Cleanup(func() { runSessionHotFn = prevHot })
	hotBytes := 0
	runSessionHotFn = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		fmt.Fprint(stdout, strings.Repeat("h", 400))
		hotBytes = 400
		return 0
	}

	var out bytes.Buffer
	if code := RunSessionStart(context.Background(), f.st, []string{"--session", "sess-hot"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls != 1 {
		t.Fatalf("inject calls = %d, want 1", f.calls)
	}
	if f.hotUsed != hotBytes {
		t.Fatalf("injector hotUsed = %d, want session-start hot step bytes %d (ruling: 잔여 = hot_budget − hot recordBytes)", f.hotUsed, hotBytes)
	}
}
