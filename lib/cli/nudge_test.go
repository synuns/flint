package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nudgeRun(args []string, stdin string) (string, int) {
	var out bytes.Buffer
	code := runNudge(context.Background(), nil, args, strings.NewReader(stdin), &out)
	return out.String(), code
}

func TestNudgeStopContextJson(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteMarker("s1", "compact"); err != nil {
		t.Fatalf("WriteMarker err = %v", err)
	}

	out, code := nudgeRun(nil, `{"hook_event_name":"Stop","session_id":"s1","stop_hook_active":false}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout %d줄, want 1: %q", len(lines), out)
	}
	var got struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("유효 JSON 아님 %q: %v", lines[0], err)
	}
	if got.HookSpecificOutput.HookEventName != "Stop" {
		t.Errorf("hookEventName = %q, want Stop", got.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(got.HookSpecificOutput.AdditionalContext, "flint:") ||
		!strings.Contains(got.HookSpecificOutput.AdditionalContext, "distillation pending") ||
		!strings.Contains(got.HookSpecificOutput.AdditionalContext, "flint nudge") {
		t.Errorf("additionalContext = %q, 지시문 누락", got.HookSpecificOutput.AdditionalContext)
	}
}

func TestNudgeStopActiveGuard(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteMarker("s1", "compact"); err != nil {
		t.Fatalf("WriteMarker err = %v", err)
	}

	out, code := nudgeRun(nil, `{"hook_event_name":"Stop","session_id":"s1","stop_hook_active":true}`)
	if code != 0 || out != "" {
		t.Errorf("stop_hook_active: (out=%q, exit=%d), want (%q, 0)", out, code, "")
	}
}

func TestNudgeStartPlain(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteMarker("s1", "compact"); err != nil {
		t.Fatalf("WriteMarker err = %v", err)
	}

	out, code := nudgeRun(nil, `{"hook_event_name":"SessionStart","session_id":"s1"}`)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout %d줄, want 1: %q", len(lines), out)
	}
	if strings.HasPrefix(lines[0], "{") {
		t.Errorf("SessionStart 출력이 JSON: %q, want plain", lines[0])
	}
	if !strings.Contains(lines[0], "distillation pending") || !strings.Contains(lines[0], "flint nudge") {
		t.Errorf("plain 안내 누락: %q", lines[0])
	}
}

func TestNudgeClear(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteMarker("s1", "compact"); err != nil {
		t.Fatalf("WriteMarker err = %v", err)
	}

	out, code := nudgeRun([]string{"--clear"}, "")
	if code != 0 || out != "" {
		t.Fatalf("--clear: (out=%q, exit=%d), want (%q, 0)", out, code, "")
	}
	m, err := ReadMarker()
	if m != nil || err != nil {
		t.Errorf("소비 후 ReadMarker = (%v, %v), want (nil, nil)", m, err)
	}
}

func TestNudgeNoMarkerSilent(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())

	out, code := nudgeRun(nil, `{"hook_event_name":"Stop","session_id":"s1","stop_hook_active":false}`)
	if code != 0 || out != "" {
		t.Errorf("마커 부재: (out=%q, exit=%d), want (%q, 0)", out, code, "")
	}
}

func TestNudgeBadStdinExit0(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	if err := WriteMarker("s1", "compact"); err != nil {
		t.Fatalf("WriteMarker err = %v", err)
	}

	out, code := nudgeRun(nil, "not json")
	if code != 0 || out != "" {
		t.Errorf("파손 stdin: (out=%q, exit=%d), want (%q, 0)", out, code, "")
	}

	dir2 := t.TempDir()
	t.Setenv("FLINT_CONFIG_DIR", dir2)
	if err := os.WriteFile(filepath.Join(dir2, "pending-distill.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("파손 마커 기록 실패: %v", err)
	}
	out2, code2 := nudgeRun(nil, `{"hook_event_name":"Stop","session_id":"s1","stop_hook_active":false}`)
	if code2 != 0 || out2 != "" {
		t.Errorf("파손 마커: (out=%q, exit=%d), want (%q, 0)", out2, code2, "")
	}
}
