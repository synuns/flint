// maintain_reason_flag_v103_test.go — v1.0.3 R3 회귀: maintain --reason
// 값이 뒤 플래그를 흡수하는 오타. '--reason --commit'이 사유 "--commit"과
// 확정 마커로 갈라져 "not valid" exit 1을 내던 경로를, 공용 가드
// (knowledge.ValidateFlagValue) 무효 인자 exit 3으로 선행 차단한다.
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
