package ops

import (
	"strings"
	"testing"
)

func TestMaintainReasonFlagAbsorptionExits3(t *testing.T) {
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	out, code := runMaintainArgs(t, f, "--invalidate", "--id", "1", "--reason", "--commit")
	if code != 3 {
		t.Fatalf("exit=%d, want 3(플래그 흡수 오타 무효 인자); out=%s", code, out)
	}
	if !strings.Contains(out, `"field":"reason"`) {
		t.Errorf("reason 스키마 에러 아님: %s", out)
	}
	if len(f.invalidatedIDs) != 0 {
		t.Errorf("흡수된 사유로 무효화 실행: %v", f.invalidatedIDs)
	}
}
