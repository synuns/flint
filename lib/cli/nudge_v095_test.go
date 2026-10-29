// nudge_v095_test.go — v0.9.5 R2: SessionStart 안내에 read 촉진 힌트
// (nudgeReadHint)가 병합되었는지, T73 고정 문구(nudgeStopDirective·
// nudgeStartLine)는 훼손되지 않았는지 검증한다.
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
	if !strings.Contains(nudgeStopDirective, "미증류 컴팩트 요약이 있다") {
		t.Error("기존 Stop directive 훼손")
	}
	if !strings.Contains(nudgeStartLine, "nudge --clear") {
		t.Error("기존 SessionStart 라인 훼손")
	}
}
