// nudge_v0181_test.go — v0.18.1: recall 마커(Reason "recall")의 SessionStart
// 소비. 회상 부재 경고가 자동 배선(Stop 훅 stdout)에서 안 보이던 갭 폐쇄 —
// done이 마커를 남기면 다음 세션 시작에 전용 문구 1회 표시 후 자동 소진.
package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

// TestNudgeRecallMarkerOneShot — recall 마커 + SessionStart → 전용 문구
// 1줄 출력 후 마커 자동 소진(재실행은 무출력). v0.18.2: recall 전용 파일.
func TestNudgeRecallMarkerOneShot(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteTypedMarker(markerKindRecall, "s1", nudgeReasonRecall); err != nil {
		t.Fatalf("WriteTypedMarker err = %v", err)
	}
	out, code := nudgeRun(nil, `{"hook_event_name":"SessionStart","session_id":"s2"}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if strings.TrimSpace(out) != nudgeRecallStartLine {
		t.Fatalf("out=%q, want recall 문구", out)
	}
	if _, err := os.Stat(RecallMarkerFile()); !os.IsNotExist(err) {
		t.Fatalf("recall 마커 미소진: %v", err)
	}
	// 소진 후 재실행 — 무출력(1회성).
	out2, _ := nudgeRun(nil, `{"hook_event_name":"SessionStart","session_id":"s2"}`)
	if strings.TrimSpace(out2) != "" {
		t.Fatalf("소진 후 out=%q, want 무출력", out2)
	}
}

// TestNudgeCompactMarkerUnchanged — 기존 증류 마커(compact)는 소진 없이
// 기존 문구·마커 유지(회귀 — recall 분기가 기본 경로를 삼키지 않음).
func TestNudgeCompactMarkerUnchanged(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteMarker("s1", "compact"); err != nil {
		t.Fatalf("WriteMarker err = %v", err)
	}
	var out bytes.Buffer
	code := runNudge(context.Background(), nil, nil,
		strings.NewReader(`{"hook_event_name":"SessionStart","session_id":"s2"}`), &out)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(out.String(), nudgeStartLine) {
		t.Fatalf("out=%q, want 기존 증류 안내", out.String())
	}
	if _, err := os.Stat(MarkerFile()); err != nil {
		t.Fatalf("compact 마커가 소진됨(불변 계약 위반): %v", err)
	}
}
