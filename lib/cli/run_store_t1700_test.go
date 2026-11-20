// run_store_t1700_test.go — the injectedStore's TelemetryCommitter
// delegation. Verifies that the read/hot/done metering commits on the MCP
// injection path are not blocked by the wrapper.
package cli

import (
	"context"
	"testing"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

// wireStoreStub — a zero-behavior wire.Store stub (for delegation-path tests only —
// promoted via embedding; the rest passes through).
type wireStoreStub struct{ wire.Store }

// t1700TelStore — the original fake implementing CommitTelemetry (the rest of wire.Store is
// promoted via embedding — only the delegation path is called).
type t1700TelStore struct {
	wireStoreStub
	commands []string
}

func (s *t1700TelStore) CommitTelemetry(ctx context.Context, cmd string) error {
	s.commands = append(s.commands, cmd)
	return nil
}

// TestInjectedStoreForwardsCommitTelemetry — injectedStore delegates the original's CommitTelemetry (an unimplemented original is a silent
// no-op — the existing metering-capability fallback pattern).
func TestInjectedStoreForwardsCommitTelemetry(t *testing.T) {
	orig := &t1700TelStore{}
	var st knowledge.TelemetryCommitter = injectedStore{orig}
	if err := st.CommitTelemetry(context.Background(), "read"); err != nil {
		t.Fatalf("CommitTelemetry 위임: %v", err)
	}
	if len(orig.commands) != 1 || orig.commands[0] != "read" {
		t.Fatalf("원본 전달 = %v, want [read] 1회", orig.commands)
	}
	// unimplemented original — silent no-op (the §13 metering pattern).
	var bare knowledge.TelemetryCommitter = injectedStore{wireStoreStub{}}
	if err := bare.CommitTelemetry(context.Background(), "read"); err != nil {
		t.Fatalf("미구현 원본 폴백이 오류 반환: %v", err)
	}
}
