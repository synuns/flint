package cli

import (
	"context"
	"testing"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

type wireStoreStub struct{ wire.Store }

type t1700TelStore struct {
	wireStoreStub
	commands []string
}

func (s *t1700TelStore) CommitTelemetry(ctx context.Context, cmd string) error {
	s.commands = append(s.commands, cmd)
	return nil
}

func TestInjectedStoreForwardsCommitTelemetry(t *testing.T) {
	orig := &t1700TelStore{}
	var st knowledge.TelemetryCommitter = injectedStore{orig}
	if err := st.CommitTelemetry(context.Background(), "read"); err != nil {
		t.Fatalf("CommitTelemetry 위임: %v", err)
	}
	if len(orig.commands) != 1 || orig.commands[0] != "read" {
		t.Fatalf("원본 전달 = %v, want [read] 1회", orig.commands)
	}

	var bare knowledge.TelemetryCommitter = injectedStore{wireStoreStub{}}
	if err := bare.CommitTelemetry(context.Background(), "read"); err != nil {
		t.Fatalf("미구현 원본 폴백이 오류 반환: %v", err)
	}
}
