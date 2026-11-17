// maintain_test.go — v1.6.1 T3(B3·B4): maintain 소형 계약. ① TUI 로더가
// 보고 3종에 명시하는 --limit 500 호출 패턴이 모드 귀속 검증을 통과하고
// 저장소 조회에 상한이 그대로 반영된다(잘림 무표시 폐지의 ops측 절반 —
// TUI측 인자 조립은 ops.MaintainTUILoadLimit 단일 소스). ② usage 라인이
// --tui 진입을 안내한다. fakeMaintStore·runMaintainArgs는 care_test.go
// 관례 재사용.
package ops

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

// limitCapturingStore — DupGroups·ConflictGroups 상한 캡처 fake
// (fakeMaintStore은 limit을 기록하지 않는다 — 여기서만 필요한 얇은 랩).
type limitCapturingStore struct {
	*fakeMaintStore
	dupLimit      int
	conflictLimit int
}

func (f *limitCapturingStore) DupGroups(ctx context.Context, limit int) ([]db.DupGroup, error) {
	f.dupLimit = limit
	return nil, nil
}
func (f *limitCapturingStore) ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error) {
	f.conflictLimit = limit
	return nil, nil
}

// runLimitArgs — 랩 store을 그대로 RunMaintain에 넣는 실행 헬퍼
// (runMaintainArgs는 *fakeMaintStore 고정이라 여기선 로컬 판).
func runLimitArgs(t *testing.T, f *limitCapturingStore, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := RunMaintain(context.Background(), f, args, nil, &out)
	return out.String(), code
}

// TestMaintainTUILoadLimitContract — B3: TUI suspects 로더의 호출 패턴
// (--limit 500 + 모드)이 세 보고 모드 전부에서 무효 인자(exit 1/3)로
// 거부되지 않고 상한 500이 스캔에 그대로 흐른다. 기본 상한
// (dup/conflict 10·stale 20)은 사람 대화형용 — TUI는 잘림이 화면에
// 표시되지 않는 만큼 명시적 상한으로 대체한다.
func TestMaintainTUILoadLimitContract(t *testing.T) {
	if MaintainTUILoadLimit != 500 {
		t.Fatalf("MaintainTUILoadLimit = %d, want 500(스펙 v1.6.1 B3)", MaintainTUILoadLimit)
	}
	f := &limitCapturingStore{fakeMaintStore: &fakeMaintStore{fakeStore: &fakeStore{}}}

	// dup-suspects — TUI가 조립하는 인자 패턴 그대로.
	out, code := runLimitArgs(t, f, "--limit", "500", "--dup-suspects")
	if code != 0 || !strings.Contains(out, `"status":"dup-suspects"`) {
		t.Fatalf("dup-suspects exit=%d out=%s", code, out)
	}
	if f.dupLimit != 500 {
		t.Errorf("DupGroups limit = %d, want 500", f.dupLimit)
	}

	out, code = runLimitArgs(t, f, "--limit", "500", "--conflict-suspects")
	if code != 0 || !strings.Contains(out, `"status":"conflict-suspects"`) {
		t.Fatalf("conflict-suspects exit=%d out=%s", code, out)
	}
	if f.conflictLimit != 500 {
		t.Errorf("ConflictGroups limit = %d, want 500", f.conflictLimit)
	}

	out, code = runLimitArgs(t, f, "--limit", "500", "--stale")
	if code != 0 || !strings.Contains(out, `"status":"ok"`) {
		t.Fatalf("stale exit=%d out=%s", code, out)
	}
}

// TestMaintainUsageLineMentionsTUI — B4: bare usage(모드 미지정 exit 3)
// 안내에 --tui 옵트인 진입이 포함된다(에이전트 자가수신 — 에러 메시지
// 공통 접미가 유일한 발견 경로인 경우).
func TestMaintainUsageLineMentionsTUI(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f)
	if code != 3 {
		t.Fatalf("bare exit = %d, want 3", code)
	}
	if !strings.Contains(out, "usage: flint maintain") {
		t.Fatalf("usage 부재: %s", out)
	}
	if !strings.Contains(out, "--tui") {
		t.Fatalf("usage에 --tui 부재: %s", out)
	}
}
