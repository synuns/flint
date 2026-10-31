// doctor_v094_test.go — v0.9.4 T1: doctor --fix 자가수복(applyHookFix).
// 픽스처는 FLINT_CLAUDE_HOME 임시 디렉터리로 실사용 ~/.claude 미접촉.
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
		if !strings.Contains(s, "\"/x/flint "+cmd) {
			t.Errorf("%s 명령 누락", cmd)
		}
	}
	if !strings.Contains(s, "orca") {
		t.Error("기존 orca 훅 삭제됨")
	}
}

// file — hooks 외 최상위 키(env·permissions 등)와 기존 이벤트 배열
// 엔트리는 병합 후에도 무손실이어야 한다(스펙 R1 "다른 키 전부 불변").
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
	// 기존 Stop 엔트리는 배열 앞쪽 그대로(교체·append 후 orca 1 + flint 1).
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

// file — 백업 파일은 원본 바이트와 동일해야 한다(원본 보존 계약).
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

// file — 파손 JSON은 복구 중단: err·파일 무변경·백업 없음.
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

// protocol — --fix 리포트 한 줄 JSON 형식·무 --fix 출력 불변.
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

	// 무 --fix 경로는 기존 doctor 출력 그대로 — fixed 리포트 미출력.
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home) // settings.json 부재 → hook-file fail
	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f)
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (출력 %q)", code, out)
	}
	if strings.Contains(out, `"fixed"`) {
		t.Errorf("무 --fix인데 fixed 리포트 출력: %q", out)
	}
}

// state — doctor --fix: hook-file fail을 자동 복구해 ok로 재평가.
func TestDoctorFixFlagAppliesHookFix(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("stub: docker 없음 가정") // port skip — 환경 의존 제거
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
	flintBinPathFn = func() string { return "/x/flint" } // 테스트 바이너리 경로 주입
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
