// sessionhook_v0110_test.go — fast tests for the unified session-start/session-end
// commands (inheriting the hot+nudge / done+nudge sequences) and the hooks.json 3-entry rework;
// with SUPERLITE_REAL=1, re-verified against the real implementation via the asofFakeOrReal seam.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSessionStartRunsHotAndNudge(t *testing.T) {
	var out bytes.Buffer
	code := runSessionStart(context.Background(), asofFakeOrReal(t, &fakeStore{}), []string{"--session", "s1"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, out.String())
	}
	if !strings.Contains(out.String(), "[flint]") {
		t.Errorf("hot 흔적 없음: %q", out.String())
	}
}

// TestSessionEndRunsDoneAndNudge — runSessionEnd continues into nudge after the done
// determination (output and order inherited). autoBackup is a seam stub (side-effect-free unit test).
func TestSessionEndRunsDoneAndNudge(t *testing.T) {
	stubAutoBackup(t)
	var out bytes.Buffer
	code := runSessionEnd(context.Background(), asofFakeOrReal(t, &fakeStore{}), []string{"--session", "s1"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, out.String())
	}
	if !strings.Contains(out.String(), `{"status":`) {
		t.Errorf("done 흔적 없음: %q", out.String())
	}
}

func TestHooksJsonThreeEntries(t *testing.T) {
	// the plan-validation code's path was given as "../flint-plugin/..." but relative to lib/cli
	// it becomes lib/flint-plugin and fails — corrected to "../../" from the root.
	b, err := os.ReadFile("../../flint-plugin/hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var h struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &h); err != nil {
		t.Fatal(err)
	}
	if len(h.Hooks["SessionStart"]) != 1 || !strings.Contains(h.Hooks["SessionStart"][0].Hooks[0].Command, "session-start") {
		t.Error("SessionStart 미통합")
	}
	if len(h.Hooks["Stop"]) != 1 || !strings.Contains(h.Hooks["Stop"][0].Hooks[0].Command, "session-end") {
		t.Error("Stop 미통합")
	}
	if len(h.Hooks["PostCompact"]) != 1 {
		t.Error("PostCompact 변경됨")
	}
}

// TestHooksJsonSessionIdQuotedSafe — the session_id substitution is
// jq @sh + eval — the double-quoted substitution ("$(jq ...)") was a shell-injection window where the id's " $ ` were interpreted inside
// double quotes. Pass-through of normal ids and empty-argument equivalence were
// demonstrated by running the hooks.json command with bash (audit run).
func TestHooksJsonSessionIdQuotedSafe(t *testing.T) {
	b, err := os.ReadFile("../../flint-plugin/hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var h struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &h); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"SessionStart", "Stop"} {
		cmd := h.Hooks[ev][0].Hooks[0].Command
		if strings.Contains(cmd, `"$(jq`) {
			t.Errorf("%s: 이중따옴표 jq 치환 잔존(삽입 창): %q", ev, cmd)
		}
		if !strings.Contains(cmd, "@sh") || !strings.HasPrefix(cmd, "eval ") {
			t.Errorf("%s: @sh+eval 패턴 부재: %q", ev, cmd)
		}
	}
}

// TestApplyHookFixReplacesLegacyFlintEntries — the existing 5 entries (running hot, nudge, and done
// directly) are replaced by 3 flint entries (session-start, session-end, compact).
// Other tools' hooks (orca) and the rest of the settings are unchanged.
func TestApplyHookFixReplacesLegacyFlintEntries(t *testing.T) {
	legacyCmd := func(sub string) map[string]any {
		return map[string]any{"type": "command", "command": "flint " + sub}
	}
	p := writeFixture(t, map[string]any{
		"SessionStart": []any{
			map[string]any{"matcher": "startup|resume|clear|compact", "hooks": []any{legacyCmd(`hot --session "$(jq x)"`)}},
			map[string]any{"matcher": "startup|resume|clear|compact", "hooks": []any{legacyCmd("nudge")}},
		},
		"Stop": []any{
			map[string]any{"hooks": []any{legacyCmd(`done --session "$(jq x)"`)}},
			map[string]any{"hooks": []any{legacyCmd("nudge")}},
			map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "orca"}}},
		},
	})
	applied, backup, err := applyHookFix(p, "/x/flint")
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if backup == "" || !strings.Contains(backup, ".flint-bak-") {
		t.Errorf("백업 경로 이상: %q", backup)
	}
	var got map[string]any
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("결과 JSON 파싱 실패: %v", err)
	}
	hooks := got["hooks"].(map[string]any)
	if ss, _ := hooks["SessionStart"].([]any); len(ss) != 1 {
		t.Errorf("SessionStart 엔트리 %d개, want 1(교체 미동작)", len(ss))
	}
	if st, _ := hooks["Stop"].([]any); len(st) != 2 {
		t.Errorf("Stop 엔트리 %d개, want 2(flint 1 + orca 1)", len(st))
	}
	s := string(b)
	for _, want := range []string{"session-start", "session-end", "compact"} {
		if !strings.Contains(s, want) {
			t.Errorf("%s 커맨드 누락", want)
		}
	}
	for _, legacy := range []string{"flint hot", "flint nudge", "flint done"} {
		if strings.Contains(s, legacy) {
			t.Errorf("구형 flint 직접 실행 훅 잔존(%s)", legacy)
		}
	}
	if !strings.Contains(s, "orca") {
		t.Error("타 도구 훅(orca) 삭제됨")
	}
}
