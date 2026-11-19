// nudge_v095_test.go — verifies that the SessionStart notice merges the read-promotion
// hint (nudgeReadHint), and that the fixed wordings (nudgePendingLine —
// updated to the short wording, ruling —) are not corrupted.
package cli

import (
	"strings"
	"testing"
)

func TestNudgeReadHintConstant(t *testing.T) {
	if !strings.Contains(nudgeReadHint, "flint read") {
		t.Errorf("read 힌트 문구 이상: %q", nudgeReadHint)
	}
}

func TestNudgeStopDirectivePreserved(t *testing.T) {
	if !strings.Contains(nudgePendingLine, "distillation pending") {
		t.Error("Stop 짧은 directive 훼손")
	}
	if !strings.Contains(nudgePendingLine, "flint nudge") {
		t.Error("SessionStart 라인 훼손")
	}
}
