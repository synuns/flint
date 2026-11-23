package cli

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSetupMcpDetectsInstalled(t *testing.T) {

	saved := lookPathFn
	lookPathFn = func(name string) (string, error) {
		if name == "codex" || name == "gemini" {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	defer func() { lookPathFn = saved }()
	var out strings.Builder
	code := runSetupMCP(&out)
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	s := out.String()
	if !strings.Contains(s, `"registered":["codex","gemini"]`) {
		t.Errorf("등록 목록: %s", s)
	}

	if !strings.Contains(s, `"skipped":["opencode","copilot","cursor","qwen","devin","cline"]`) {
		t.Errorf("스킵 목록: %s", s)
	}
}

func TestSetupMcpAllUninstalled(t *testing.T) {
	saved := lookPathFn
	lookPathFn = func(string) (string, error) { return "", exec.ErrNotFound }
	defer func() { lookPathFn = saved }()
	var out strings.Builder
	if code := runSetupMCP(&out); code != 0 {
		t.Errorf("전부 미설치 exit %d, want 0(무해)", code)
	}
}
