// sessionhook_v0110_test.go — v0.11.0 R4: session-start·session-end 통합
// 커맨드(hot+nudge / done+nudge 시퀀스 승계)와 hooks.json 3엔트리 재편
// fast 테스트. SUPERLITE_REAL=1이면 asofFakeOrReal seam으로 실구현 재검증.
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

// TestSessionEndRunsDoneAndNudge — runSessionEnd는 done 판정 뒤 nudge로
// 이어진다(출력·순서 승계). autoBackup은 seam 스텁(단위 테스트 무부작용).
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
	// 플랜 검증 코드의 경로는 "../flint-plugin/…"로 주어졌으나 lib/cli
	// 기준으로는 lib/flint-plugin이 되어 실패 — 루트 기준 "../../"로 교정.
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

// TestApplyHookFixReplacesLegacyFlintEntries — 기존 5엔트리(hot·nudge·done
// 직접 실행) flint 훅은 3엔트리(session-start·session-end·compact)로 교체.
// 타 도구 훅(orca)과 그 외 설정은 불변.
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
