// nudge_v095_test.go — v0.9.5 R2: SessionStart 안내에 read 촉진 힌트
// (nudgeReadHint)가 병합되었는지, 고정 문구(nudgeStopShort·nudgeStartLine,
// v0.11.0 R3 짧은 문구로 갱신 — 룰링)는 훼손되지 않았는지 검증한다.
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
	if !strings.Contains(nudgeStopShort, "증류 대기") {
		t.Error("Stop 짧은 directive 훼손")
	}
	if !strings.Contains(nudgeStartLine, "flint nudge") {
		t.Error("SessionStart 라인 훼손")
	}
}
