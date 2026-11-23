package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

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

	out2, _ := nudgeRun(nil, `{"hook_event_name":"SessionStart","session_id":"s2"}`)
	if strings.TrimSpace(out2) != "" {
		t.Fatalf("소진 후 out=%q, want 무출력", out2)
	}
}

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
	if !strings.Contains(out.String(), nudgePendingLine) {
		t.Fatalf("out=%q, want 기존 증류 안내", out.String())
	}
	if _, err := os.Stat(MarkerFile()); err != nil {
		t.Fatalf("compact 마커가 소진됨(불변 계약 위반): %v", err)
	}
}
