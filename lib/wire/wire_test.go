// Directly verifies the contract layer Run dispatch's special paths (the 3
// store-free ones, soft setup, quiet compact/nudge) with the factory-injected
// signature. When the wire package runs standalone the registry is empty, so each subtest
// registers and cleans up its own probe handlers (inheriting the original cli wiring-test structure).
package wire

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/store"
)

// fakeStore — a minimal Store interface fake (for dispatch verification).
// The HistoryStubs embedding absorbs the 5 history methods as unsupported defaults.
type fakeStore struct {
	queries []string
	HistoryStubs
}

func (f *fakeStore) Migrate(ctx context.Context) error { return nil }
func (f *fakeStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return nil, nil
}
func (f *fakeStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}
func (f *fakeStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (f *fakeStore) Commit(ctx context.Context, msg string) (string, error) {
	return "abc123", nil
}
func (f *fakeStore) Ping(ctx context.Context) error { return nil }
func (f *fakeStore) Close()                         {}
func (f *fakeStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	f.queries = append(f.queries, sql)
	return nil, nil
}

// probeHandler — registers the name + cleanup. The handler body comes as an argument.
func probeHandler(t *testing.T, name string, h Handler) {
	t.Helper()
	Register(name, h)
	t.Cleanup(func() { Unregister(name) })
}

// failFactory — a factory that must never be called (for store-free path verification).
func failFactory(t *testing.T) func(ctx context.Context, dsn string) (Store, error) {
	return func(ctx context.Context, dsn string) (Store, error) {
		t.Errorf("newStore 공장 호출됨 — %q는 store-free여야 함", dsn)
		return nil, errors.New("must not connect")
	}
}

// errFactory — a factory that always fails (simulating DB down).
func errFactory(err error) func(ctx context.Context, dsn string) (Store, error) {
	return func(ctx context.Context, dsn string) (Store, error) {
		return nil, err
	}
}

// TestWireRunDispatch — Run's special-path dispatch.
// The 3 store-free paths (version, teardown, mcp — factory not called, st nil
// injected); soft setup (handler runs even on factory failure, st nil); quiet
// compact (on factory failure, handler with st nil, exit = the handler's
// return); quiet nudge (on factory failure, handler not run, only the MapExit code — no error JSON leaked).
func TestWireRunDispatch(t *testing.T) {
	t.Run("store_free_skips_factory", func(t *testing.T) {
		for _, name := range []string{"version", "teardown", "mcp", "update"} {
			probeHandler(t, name, func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
				if st != nil {
					t.Errorf("%s: store-free인데 store 주입됨", name)
				}
				return 7 // the handler's own code — proof of dispatch passthrough
			})
			var out bytes.Buffer
			if code := Run([]string{name}, strings.NewReader(""), &out, failFactory(t)); code != 7 {
				t.Fatalf("Run(%s) exit=%d, want 7(핸들러 반환)", name, code)
			}
		}
	})
	t.Run("soft_setup_survives_factory_failure", func(t *testing.T) {
		ran := false
		probeHandler(t, "setup", func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
			ran = true
			if st != nil {
				t.Error("setup: 공장 실패 시 st는 nil(degraded 경로)")
			}
			return 0
		})
		var out bytes.Buffer
		if code := Run([]string{"setup"}, strings.NewReader(""), &out, errFactory(errors.New("db down"))); code != 0 {
			t.Fatalf("Run(setup) exit=%d, want 0(soft 경로)", code)
		}
		if !ran {
			t.Fatal("setup 핸들러 미실행 — 공장 실패에도 소프트 진입해야 함(T40)")
		}
	})
	t.Run("quiet_compact_runs_handler_nil_store", func(t *testing.T) {
		probeHandler(t, "compact", func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
			if st != nil {
				t.Error("compact: 공장 실패 시 st는 nil(무동작 0 경로)")
			}
			return 0
		})
		var out bytes.Buffer
		if code := Run([]string{"compact"}, strings.NewReader("not json"), &out, errFactory(errors.New("db down"))); code != 0 {
			t.Fatalf("Run(compact) exit=%d, want 0(훅 비간섭 §3)", code)
		}
	})
	t.Run("quiet_nudge_maps_exit_without_handler", func(t *testing.T) {
		probeHandler(t, "nudge", func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
			t.Error("nudge 핸들러 실행됨 — 공장 실패 시 MapExit로 조용히 종료해야 함(N5)")
			return 0
		})
		var out bytes.Buffer
		code := Run([]string{"nudge"}, strings.NewReader(`{"hook_event_name":"Stop"}`), &out, errFactory(store.ErrServerDown))
		if code != 2 {
			t.Fatalf("Run(nudge) exit=%d, want 2(server_down MapExit)", code)
		}
		if out.Len() != 0 {
			t.Fatalf("nudge quiet 계약 위반 — stdout 유출: %q", out.String())
		}
	})
	t.Run("server_down_quiet_line_exit2", func(t *testing.T) {
		// moved from the original cli TestServerDownExit2: on a general command's store
		// connection failure, 1 ErrLine (server_down) + the MapExit code. The handler is not run.
		probeHandler(t, "write", func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
			t.Error("write 핸들러 실행됨 — 공장 실패 시 핸들러 미실행")
			return 0
		})
		var out bytes.Buffer
		code := Run([]string{"write", "knowledge"}, strings.NewReader(`{}`), &out, errFactory(store.ErrServerDown))
		if code != 2 {
			t.Fatalf("exit=%d, want 2", code)
		}
		if strings.TrimSpace(out.String()) != `{"error":"server_down"}` {
			t.Fatalf("조용히 한 줄 JSON만: %q", out.String())
		}
	})
	t.Run("unknown_command_single_line_json_exit3", func(t *testing.T) {
		// unknown command is also unified to the invalid-arg contract exit 3.
		// The output is the guidance form without an error key — message (the
		// unknown-command fact) + usage (the command list) on one line, informational content kept.
		var out bytes.Buffer
		if code := Run([]string{"no-such-cmd"}, strings.NewReader(""), &out, func(ctx context.Context, dsn string) (Store, error) {
			return &fakeStore{}, nil
		}); code != 3 {
			t.Fatalf("exit=%d, want 3", code)
		}
		line := strings.TrimSpace(out.String())
		if strings.Count(line, "\n") != 0 {
			t.Fatalf("unknown command 출력=%q, want 한 줄 JSON", line)
		}
		m := decodeErrLine(t, line)
		if _, hasErr := m["error"]; hasErr {
			t.Fatalf("unknown command 안내에 error 키 잔존: %q", line)
		}
		if !strings.Contains(m["message"], "unknown command: no-such-cmd") {
			t.Fatalf("unknown command 사실 유지 실패: %q", m["message"])
		}
		if !strings.Contains(m["usage"], "flint <") {
			t.Fatalf("usage 정보 부재: %q", m["usage"])
		}
	})
	t.Run("no_args_prints_usage_exit1", func(t *testing.T) {
		var out bytes.Buffer
		if code := Run(nil, strings.NewReader(""), &out, failFactory(t)); code != 1 {
			t.Fatalf("exit=%d, want 1", code)
		}
		// UsageLine is the guidance form without an error key — exit 1 is kept.
		if !strings.Contains(out.String(), `"usage":"flint <`) {
			t.Fatalf("usage 미출력: %q", out.String())
		}
	})
}
