package cli

import (
	"os/exec"
	"strings"
	"testing"
)

// 시임: lookPathFn 선언은 setup.go(비테스트 빌드 포함) — 여기선 교체만.

func TestSetupMcpDetectsInstalled(t *testing.T) {
	// codex·gemini 설치, opencode·cursor·copilot 미설치 가짜 환경
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
	// v0.21.0: A급 확장 — 미설치 하니스는 레지스트리 순서(opencode·
	// copilot·cursor·qwen·devin·cline). B급(파일 설정형)은 목록 제외.
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
