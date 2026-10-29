// rules_sync_v097_test.go — T1 R1: 스킬 싱크(syncRules 멱등·삭제 금지),
// skillSrcDir 탐지, setup --update-rules 보고, doctor skill-file warn 계약.
// FLINT_CLAUDE_HOME 픽스처로 실제 ~/.claude에는 접촉하지 않는다.
package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSyncRulesCopiesNew(t *testing.T) {
	src := writeTree(t, t.TempDir(), map[string]string{
		"skills/flint/SKILL.md": "rules v097",
		"skills/flint/extra.md": "x",
	})
	dest := t.TempDir()
	n, err := syncRules(filepath.Join(src, "skills", "flint"), filepath.Join(dest, "flint"))
	if err != nil || n != 2 {
		t.Fatalf("copied=%d err=%v", n, err)
	}
	b, _ := os.ReadFile(filepath.Join(dest, "flint", "SKILL.md"))
	if string(b) != "rules v097" {
		t.Errorf("내용 불일치: %q", b)
	}
}

func TestSyncRulesIdempotent(t *testing.T) {
	src := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "same"})
	dest := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "same"})
	if n, err := syncRules(src, dest); err != nil || n != 0 {
		t.Errorf("멱등 위반: %d %v", n, err)
	}
}

func TestSyncRulesPreservesExtra(t *testing.T) {
	src := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "new"})
	dest := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "old", "user-notes.md": "keep"})
	if _, err := syncRules(src, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "user-notes.md")); err != nil {
		t.Error("대상 전용 파일이 삭제됨")
	}
}

// skillSrcDir — 바이너리 인접 flint-plugin/skills/flint 탐지.
func TestSkillSrcDirAdjacent(t *testing.T) {
	bin := t.TempDir()
	want := filepath.Join(bin, "flint-plugin", "skills", "flint")
	writeTree(t, bin, map[string]string{"flint-plugin/skills/flint/SKILL.md": "s"})
	if got := skillSrcDir(filepath.Join(bin, "flint")); got != want {
		t.Errorf("skillSrcDir = %q, want %q", got, want)
	}
}

// skillSrcDir — 인접·cwd 어디에도 없으면 빈 문자열(검사 생략 신호).
func TestSkillSrcDirNotFound(t *testing.T) {
	if got := skillSrcDir(t.TempDir()); got != "" {
		t.Errorf("미탐지 시 빈 문자열 아님: %q", got)
	}
}

// checkSkillFile — 대상 SKILL.md 부재·불일치는 warn + update-rules 안내.
func TestDoctorSkillFileWarnOnMismatch(t *testing.T) {
	src := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "v097"})
	dest := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "v093"})
	c, ok := checkSkillFile(src, dest)
	if !ok {
		t.Fatal("소스 탐지됐는데 검사 생략(ok=false)")
	}
	if c.Name != "skill-file" || c.Status != "warn" {
		t.Errorf("결과 = %+v, want skill-file warn", c)
	}
	if !strings.Contains(c.Fix, "update-rules") {
		t.Errorf("fix에 update-rules 안내 없음: %q", c.Fix)
	}

	emptyDest := t.TempDir()
	c2, ok2 := checkSkillFile(src, emptyDest)
	if !ok2 || c2.Status != "warn" {
		t.Errorf("부재 결과 = %+v ok=%v, want warn", c2, ok2)
	}
	if !strings.Contains(c2.Fix, "update-rules") {
		t.Errorf("부재 fix에 update-rules 안내 없음: %q", c2.Fix)
	}
}

// checkSkillFile — 일치 시 ok.
func TestDoctorSkillFileOkWhenMatched(t *testing.T) {
	src := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "same"})
	dest := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "same"})
	c, ok := checkSkillFile(src, dest)
	if !ok || c.Status != "ok" {
		t.Errorf("결과 = %+v ok=%v, want ok", c, ok)
	}
}

// checkSkillFile — 소스 부재(빈 문자열)·소스 SKILL.md 부재는 검사 생략.
func TestDoctorSkillFileOmittedWhenNoSource(t *testing.T) {
	if _, ok := checkSkillFile("", t.TempDir()); ok {
		t.Error("소스 미탐지인데 검사 생성됨")
	}
	noSkill := t.TempDir() // SKILL.md 없는 소스 디렉터리
	if _, ok := checkSkillFile(noSkill, t.TempDir()); ok {
		t.Error("소스 SKILL.md 부재인데 검사 생성됨")
	}
}

// withSkillBin — flintBinPathFn seam을 픽스처 bin 인접 경로로 교체.
func withSkillBin(t *testing.T, files map[string]string) string {
	t.Helper()
	bin := t.TempDir()
	writeTree(t, bin, files)
	prev := flintBinPathFn
	flintBinPathFn = func() string { return filepath.Join(bin, "flint") }
	t.Cleanup(func() { flintBinPathFn = prev })
	return bin
}

// doctor 전체 실행 — skill-file warn은 exit 0(룰링: fail은 과잉).
func TestDoctorSkillFileWarnExitZero(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("stub: docker 없음 가정") // port skip — 환경 의존 제거
	})
	withSkillBin(t, map[string]string{"flint-plugin/skills/flint/SKILL.md": "v097"})
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)
	setHarnessEnvs(t) // 하니스 5종 격리
	writeTree(t, home, map[string]string{
		"settings.json":        `{"hooks":{"PostToolUse":[{"command":"flint write"}]}}`,
		"skills/flint/SKILL.md": "v093", // 불일치 → warn
	})

	f := &fakeStore{queryFn: freshDoctorFn()}
	out, code := runDoctorArgs(t, f)
	if code != 0 {
		t.Fatalf("warn exit=%d, want 0 (출력 %q)", code, out)
	}
	if !strings.Contains(out, `"skill-file"`) || !strings.Contains(out, `"warn"`) {
		t.Errorf("skill-file warn 없음: %q", out)
	}
}

// claudeHome 미탐지(FLINT_CLAUDE_HOME·HOME 모두 빈) 시 skill-file 검사는
// 생성되지 않는다 — filepath.Join("",…) 상대경로를 cwd에서 오탐하지 않는다.
func TestDoctorSkillFileOmittedWhenNoHome(t *testing.T) {
	withComposePs(t, func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("stub: docker 없음 가정") // port skip
	})
	withSkillBin(t, map[string]string{"flint-plugin/skills/flint/SKILL.md": "v097"}) // 소스는 탐지됨
	t.Setenv("FLINT_CLAUDE_HOME", "")
	t.Setenv("HOME", "")
	setHarnessEnvs(t)

	out, code := runDoctorArgs(t, &fakeStore{queryFn: freshDoctorFn()})
	if code != 1 { // hook-file fail(home 미탐지)은 정상 — 여기선 skill-file 부재만 본다
		t.Logf("exit=%d(참고)", code)
	}
	if strings.Contains(out, `"skill-file"`) {
		t.Errorf("home 미탐지인데 skill-file 검사 생성됨: %q", out)
	}
}

// setup --update-rules — 싱크 실행 보고 {"status":"synced","copied":N,"dest":…}.
func TestSetupUpdateRulesReport(t *testing.T) {
	withFake(t, &fakeStore{})
	src := writeTree(t, t.TempDir(), map[string]string{
		"SKILL.md":  "rules v097",
		"extra.md":  "x",
	})
	home := t.TempDir()
	t.Setenv("FLINT_CLAUDE_HOME", home)

	out, code := mustRun(t, []string{"setup", "--update-rules", "--src", src}, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := decode(t, out)
	if m["status"] != "synced" {
		t.Fatalf("status=%v, want synced; out=%s", m["status"], out)
	}
	if m["copied"] != float64(2) {
		t.Fatalf("copied=%v, want 2; out=%s", m["copied"], out)
	}
	if d, _ := m["dest"].(string); !strings.HasPrefix(d, home) {
		t.Fatalf("dest=%q, want %q 하위", d, home)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "skills", "flint", "SKILL.md")); string(b) != "rules v097" {
		t.Errorf("SKILL.md 싱크 안 됨: %q", b)
	}

	// 재실행 — 멱등(copied 0).
	out2, code2 := mustRun(t, []string{"setup", "--update-rules", "--src", src}, "")
	if code2 != 0 {
		t.Fatalf("재실행 exit=%d, want 0; out=%s", code2, out2)
	}
	if m2 := decode(t, out2); m2["copied"] != float64(0) {
		t.Errorf("재실행 copied=%v, want 0(멱등); out=%s", m2["copied"], out2)
	}
}

// setup --update-rules — 소스 미탐지 시 한 줄 에러 JSON + exit 1.
func TestSetupUpdateRulesNoSource(t *testing.T) {
	withFake(t, &fakeStore{})
	t.Setenv("FLINT_CLAUDE_HOME", t.TempDir())
	out, code := mustRun(t, []string{"setup", "--update-rules"}, "")
	if code != 1 {
		t.Fatalf("exit=%d, want 1; out=%s", code, out)
	}
	if !strings.Contains(out, `"error"`) {
		t.Errorf("에러 JSON 아님: %q", out)
	}
}
