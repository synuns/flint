package tui

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/wire"
)

func TestRunTuningTUIDefaultTerminalGate(t *testing.T) {
	var out bytes.Buffer
	code := RunTuningTUI(context.Background(), nil, &out, TuningDeps{
		Rows: func(ctx context.Context) ([]TuningKeyRow, error) {
			return []TuningKeyRow{{Key: "dead_days", Value: "30", Default: "30"}}, nil
		},
	})
	if code != 3 {
		t.Fatalf("non-terminal exit = %d, want 3 (out: %s)", code, out.String())
	}
	if !strings.Contains(out.String(), "TUI requires a terminal") {
		t.Fatalf("output = %q, want ErrTerminalRequired message", out.String())
	}
}

func TestRunTuningTUIDefaultRunSeamDelegation(t *testing.T) {
	var gotArgs []string
	var gotOut io.Writer
	orig := tuningRunner
	tuningRunner = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		gotArgs, gotOut = args, stdout
		return 5
	}
	t.Cleanup(func() { tuningRunner = orig })
	var out bytes.Buffer
	code := RunTuningTUI(context.Background(), nil, &out, TuningDeps{
		IsTerminal: func() bool { return true },
		Rows: func(ctx context.Context) ([]TuningKeyRow, error) {
			return []TuningKeyRow{{Key: "dead_days", Value: "30", Default: "30"}}, nil
		},
		Form: func(sel *TuningSelection, rows []TuningKeyRow) error {
			sel.Key, sel.Value, sel.Action = "dead_days", "45", "set"
			return nil
		},
	})
	if code != 5 {
		t.Fatalf("exit = %d, want runner-returned 5", code)
	}
	if strings.Join(gotArgs, " ") != "set dead_days 45" {
		t.Fatalf("runner args = %v, want set dead_days 45", gotArgs)
	}
	if gotOut != io.Writer(&out) {
		t.Fatal("runner must receive the TUI stdout")
	}
}

func TestRunTuningTUIDefaultRunSeamResetArgs(t *testing.T) {
	var gotArgs []string
	orig := tuningRunner
	tuningRunner = func(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
		gotArgs = args
		return 0
	}
	t.Cleanup(func() { tuningRunner = orig })
	var out bytes.Buffer
	code := RunTuningTUI(context.Background(), nil, &out, TuningDeps{
		IsTerminal: func() bool { return true },
		Rows: func(ctx context.Context) ([]TuningKeyRow, error) {
			return []TuningKeyRow{{Key: "dead_days", Value: "30", Default: "30"}}, nil
		},
		Form: func(sel *TuningSelection, rows []TuningKeyRow) error {
			sel.Key, sel.Value, sel.Action = "dead_days", "45", "reset"
			return nil
		},
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.Join(gotArgs, " ") != "reset dead_days" {
		t.Fatalf("runner args = %v, want reset dead_days", gotArgs)
	}
}

func TestRunTuningTUIRunnerSeamDefault(t *testing.T) {
	if reflect.ValueOf(tuningRunner).Pointer() != reflect.ValueOf(ops.RunTuning).Pointer() {
		t.Fatal("tuningRunner seam must default to ops.RunTuning")
	}
}
