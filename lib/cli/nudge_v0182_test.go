// nudge_v0182_test.go — v0.18.2: 마커 파일 분리 회귀. (a) Stop 이벤트에서
// recall 마커는 무출력(증류 넛지 오발동 방지), (b) recall 기록이 증류 마커를
// 덮어쓰지 않는다, (c) SessionStart에서 둘 다 안내(회상만 자동 소진),
// (d) v0.18.1 잔존(pending-distill의 Reason=recall) 마이그레이션.
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNudgeRecallSilentOnStop — recall 마커 + Stop → 무출력·마커 유지.
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

// TestNudgeRecallDoesNotClobberDistill — recall 기록 후에도 증류 마커 존재.
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

// TestNudgeSessionStartBothMarkers — 두 마커 동시: recall 문구 + 증류 안내
// 2줄, recall만 소진.
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
	if !strings.Contains(lines[1], nudgeStartLine) {
		t.Fatalf("둘째 줄=%q, want 증류 안내", lines[1])
	}
	if _, err := os.Stat(RecallMarkerFile()); !os.IsNotExist(err) {
		t.Fatalf("recall 마커 미소진")
	}
	if _, err := os.Stat(MarkerFile()); err != nil {
		t.Fatalf("증류 마커가 소진됨(내구 계약 위반): %v", err)
	}
}

// TestNudgeLegacyRecallInDistillFile — v0.18.1 잔존(증류 파일에 Reason
// recall)은 회상 취급: SessionStart 문구 표시 후 그 파일 소진.
func TestNudgeLegacyRecallInDistillFile(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteMarker("s1", nudgeReasonRecall); err != nil { // 구배포 기록 형태
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
