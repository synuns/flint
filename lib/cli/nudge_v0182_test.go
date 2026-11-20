// nudge_v0182_test.go — marker-file separation regression. (a) On a Stop event,
// a recall marker produces no output (preventing distillation-nudge misfiring); (b) a recall record does not
// overwrite the distillation marker; (c) SessionStart announces both (only recall auto-consumes);
// (d) migration of residue (pending-distill with Reason=recall).
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNudgeRecallSilentOnStop — recall marker + Stop -> no output, marker kept.
func TestNudgeRecallSilentOnStop(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteTypedMarker(markerKindRecall, "s1", nudgeReasonRecall); err != nil {
		t.Fatalf("WriteTypedMarker err = %v", err)
	}
	out, code := nudgeRun(nil, `{"hook_event_name":"Stop","session_id":"s1","stop_hook_active":false}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("out=%q, want 무출력(recall은 SessionStart 전용)", out)
	}
	if _, err := os.Stat(RecallMarkerFile()); err != nil {
		t.Fatalf("recall 마커가 Stop에서 소진됨: %v", err)
	}
}

// TestNudgeRecallDoesNotClobberDistill — even after a recall record, the distillation marker exists.
func TestNudgeRecallDoesNotClobberDistill(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir)
	if err := WriteMarker("s1", "compact"); err != nil {
		t.Fatalf("WriteMarker err = %v", err)
	}
	if err := WriteTypedMarker(markerKindRecall, "s1", nudgeReasonRecall); err != nil {
		t.Fatalf("WriteTypedMarker err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pending-distill.json")); err != nil {
		t.Fatalf("증류 마커가 recall 기록에 덮어써짐: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "recall-hint.json")); err != nil {
		t.Fatalf("recall 마커 미기록: %v", err)
	}
}

// TestNudgeSessionStartBothMarkers — both markers at once: the recall wording + a 2-line distillation
// notice; only recall is consumed.
func TestNudgeSessionStartBothMarkers(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteMarker("s1", "compact"); err != nil {
		t.Fatalf("WriteMarker err = %v", err)
	}
	if err := WriteTypedMarker(markerKindRecall, "s1", nudgeReasonRecall); err != nil {
		t.Fatalf("WriteTypedMarker err = %v", err)
	}
	out, code := nudgeRun(nil, `{"hook_event_name":"SessionStart","session_id":"s2"}`)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("출력 %d줄, want 2: %q", len(lines), out)
	}
	if lines[0] != nudgeRecallStartLine {
		t.Fatalf("첫 줄=%q, want recall 문구", lines[0])
	}
	if !strings.Contains(lines[1], nudgePendingLine) {
		t.Fatalf("둘째 줄=%q, want 증류 안내", lines[1])
	}
	if _, err := os.Stat(RecallMarkerFile()); !os.IsNotExist(err) {
		t.Fatalf("recall 마커 미소진")
	}
	if _, err := os.Stat(MarkerFile()); err != nil {
		t.Fatalf("증류 마커가 소진됨(내구 계약 위반): %v", err)
	}
}

// TestNudgeLegacyRecallInDistillFile — residue (Reason=recall in the distillation file) is treated as
// recall: SessionStart shows the wording and consumes that file.
func TestNudgeLegacyRecallInDistillFile(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteMarker("s1", nudgeReasonRecall); err != nil { // the legacy-deployment record form
		t.Fatalf("WriteMarker err = %v", err)
	}
	out, code := nudgeRun(nil, `{"hook_event_name":"SessionStart","session_id":"s2"}`)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if strings.TrimSpace(out) != nudgeRecallStartLine {
		t.Fatalf("out=%q, want recall 문구(마이그레이션)", out)
	}
	if _, err := os.Stat(MarkerFile()); !os.IsNotExist(err) {
		t.Fatalf("레거시 recall 마커 미소진")
	}
}
