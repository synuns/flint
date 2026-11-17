// run_store_t1700_test.go — v1.7.0 T6: injectedStore의 TelemetryCommitter
// 위임(R33-3 F1 패턴 승계). MCP 주입 경로의 read/hot/done 계측 커밋이
// 래퍼에서 차단되지 않는지 검증.
package cli

import (
	"context"
	"testing"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

// wireStoreStub — wire.Store 영(零) 동작 stub(위임 경로 테스트 전용 —
// 임베딩으로 승격, 미호출 메서드는 간다).
type wireStoreStub struct{ wire.Store }

// t1700TelStore — CommitTelemetry 구현 원본 fake(나머지 wire.Store는
// 임베딩으로 승격 — 위임 경로만 호출된다).
type t1700TelStore struct {
	wireStoreStub
	commands []string
}

func (s *t1700TelStore) CommitTelemetry(ctx context.Context, cmd string) error {
	s.commands = append(s.commands, cmd)
	return nil
}

// TestInjectedStoreForwardsCommitTelemetry — Review Focus(v1.7.0 T6):
// injectedStore은 원본의 CommitTelemetry를 위임한다(미구현 원본은 조용
// no-op — 기존 계측 capability 폴백 패턴).
func TestInjectedStoreForwardsCommitTelemetry(t *testing.T) {
	orig := &t1700TelStore{}
	var st knowledge.TelemetryCommitter = injectedStore{orig}
	if err := st.CommitTelemetry(context.Background(), "read"); err != nil {
		t.Fatalf("CommitTelemetry 위임: %v", err)
	}
	if len(orig.commands) != 1 || orig.commands[0] != "read" {
		t.Fatalf("원본 전달 = %v, want [read] 1회", orig.commands)
	}
	// 미구현 원본 — 조용 no-op(§13 계측 패턴).
	var bare knowledge.TelemetryCommitter = injectedStore{wireStoreStub{}}
	if err := bare.CommitTelemetry(context.Background(), "read"); err != nil {
		t.Fatalf("미구현 원본 폴백이 오류 반환: %v", err)
	}
}
