// doctor_v094_test.go — doctor --fix self-repair (applyHookFix).
// Fixtures use a FLINT_CLAUDE_HOME temp directory; the real ~/.claude is untouched.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, hooks map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".claude"), 0o755)
	b, _ := json.Marshal(map[string]any{"hooks": hooks})
	p := filepath.Join(dir, ".claude", "settings.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestApplyHookFixAddsFlintHooks(t *testing.T) {
	p := writeFixture(t, map[string]any{
		"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "orca"}}}},
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
	json.Unmarshal(b, &got)
	hooks := got["hooks"].(map[string]any)
	for _, ev := range []string{"SessionStart", "Stop", "PostCompact"} {
		if _, ok := hooks[ev]; !ok {
			t.Errorf("%s 이벤트 누락", ev)
		}
	}
	s := string(b)
	for _, cmd := range []string{"session-start", "session-end", "compact"} {
		if !strings.Contains(s, "'/x/flint' "+cmd) {
			t.Errorf("%s 명령 누락", cmd)
		}
	}
	if !strings.Contains(s, "orca") {
		t.Error("기존 orca 훅 삭제됨")
	}
}

// file — top-level keys besides hooks (env, permissions, etc.) and existing event-array
// entries must be lossless after the merge (spec R1, "all other keys unchanged").
func TestApplyHookFixPreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	orig := []byte(`{"permissions":{"allow":["Bash"]},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"orca"}]}]},"env":{"FOO":"bar"}}`)
	if err := os.WriteFile(p, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := applyHookFix(p, "/x/flint"); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("결과 JSON 파싱 실패: %v", err)
	}
	if perm, ok := got["permissions"].(map[string]any); !ok || perm["allow"] == nil {
		t.Errorf("permissions 키 무손실 아님: %v", got["permissions"])
	}
	if env, ok := got["env"].(map[string]any); !ok || env["FOO"] != "bar" {
		t.Errorf("env 키 무손실 아님: %v", got["env"])
	}
	// the existing Stop entries stay at the front of the array (after replace/append: orca 1 + flint 1).
	stop, _ := got["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 2 {
		t.Fatalf("Stop 배열 길이=%d, want 2", len(stop))
	}
	first, _ := stop[0].(map[string]any)
	inner, _ := first["hooks"].([]any)
	cmd, _ := inner[0].(map[string]any)["command"].(string)
	if cmd != "orca" {
		t.Errorf("Stop[0] 기존 orca 엔트리 아님: %q", cmd)
	}
}

func TestApplyHookFixIdempotent(t *testing.T) {
	p := writeFixture(t, map[string]any{})
	if _, _, err := applyHookFix(p, "/x/flint"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	applied, backup, err := applyHookFix(p, "/x/flint")
	if err != nil || applied || backup != "" {
		t.Errorf("멱등 위반: applied=%v backup=%q err=%v", applied, backup, err)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Error("2회째 파일 변형")
	}
}

// file — the backup file must be byte-identical to the original (the original-preservation contract).
func TestApplyHookFixBackupCreated(t *testing.T) {
	p := writeFixture(t, map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "orca"}}}},
	})
	orig, _ := os.ReadFile(p)
	applied, backup, err := applyHookFix(p, "/x/flint")
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	bk, rerr := os.ReadFile(backup)
	if rerr != nil {
		t.Fatalf("백업 파일 없음: %v", rerr)
	}
	if string(bk) != string(orig) {
		t.Error("백업이 원본과 다름")
	}
}

// file — corrupt JSON aborts the repair: err, file unchanged, no backup.
func TestApplyHookFixBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	applied, backup, err := applyHookFix(p, "/x/flint")
	if err == nil || applied {
		t.Fatalf("파손 JSON인데 applied=%v err=%v", applied, err)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Error("파손 JSON 파일을 변형함")
	}
	if backup != "" {
		t.Errorf("파손 JSON에 백업 생성: %q", backup)
	}
	matches, _ := filepath.Glob(p + ".flint-bak-*")
	if len(matches) != 0 {
		t.Errorf("백업 파일 잔존: %v", matches)
	}
}

// protocol — the one-line JSON shape of the --fix report; unchanged output without --fix.
func TestDoctorFixReportFormat(t *testing.T) {
	r := doctorFixReport([]doctorCheck{{Name: "hook-file", Status: "ok"}})
	var m map[string]string
	if err := json.Unmarshal([]byte(r), &m); err != nil {
		t.Fatalf("JSON 아님: %q", r)
	}
	if m["status"] != "fixed" {
		t.Errorf("status=%q, want fixed (%q)", m["status"], r)
	}
	if m["hook-file"] != "ok" {
		t.Errorf("hook-file=%q, want ok (%q)", m["hook-file"], r)
	}

	// the path without --fix keeps the existing doctor output — no fixed report printed.
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home) // no settings.json -> hook-file fail
	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f)
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (출력 %q)", code, out)
	}
	if strings.Contains(out, `"fixed"`) {
		t.Errorf("무 --fix인데 fixed 리포트 출력: %q", out)
	}
}

// state — doctor --fix: auto-repairs the hook-file fail and re-evaluates it as ok.
func TestDoctorFixFlagAppliesHookFix(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("stub: docker 없음 가정") // port skip — environment dependency removed
	})
	dir := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", dir)
	setHarnessEnvs(t)
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"orca"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fakeStore{queryFn: freshDoctorFn()}
	oldBin := flintBinPathFn
	flintBinPathFn = func() string { return "/x/flint" } // inject a test binary path
	t.Cleanup(func() { flintBinPathFn = oldBin })
	out, code := runDoctorArgs(t, f, "--fix")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (출력 %q)", code, out)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님: %q", out)
	}
	if m["status"] != "fixed" {
		t.Errorf("status=%q, want fixed (출력 %q)", m["status"], out)
	}
	if m["hook-file"] != "ok" {
		t.Errorf("hook-file=%q, want ok (출력 %q)", m["hook-file"], out)
	}
	body, _ := os.ReadFile(p)
	s := string(body)
	if !strings.Contains(s, "orca") || !strings.Contains(s, " session-start ") {
		t.Errorf("훅 병합 결과 이상: %q", s)
	}
	matches, _ := filepath.Glob(p + ".flint-bak-*")
	if len(matches) != 1 {
		t.Errorf("백업 1개 기대: %v", matches)
	}
}
