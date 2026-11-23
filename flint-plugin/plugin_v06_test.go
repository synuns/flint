package flintplugin

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func hookWriteCommands(t *testing.T, doc map[string]any) []string {
	t.Helper()
	events, ok := doc["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("'hooks' 객체 없음")
	}
	var cmds []string
	for event, groups := range events {
		list, ok := groups.([]any)
		if !ok {
			t.Fatalf("이벤트 %s 그룹 목록 없음", event)
		}
		for _, g := range list {
			group, ok := g.(map[string]any)
			if !ok {
				continue
			}
			hs, ok := group["hooks"].([]any)
			if !ok {
				continue
			}
			for _, h := range hs {
				hm, ok := h.(map[string]any)
				if !ok {
					continue
				}
				cmd, _ := hm["command"].(string)
				if cmd != "" {
					cmds = append(cmds, cmd)
				}
			}
		}
	}
	if len(cmds) == 0 {
		t.Fatalf("실행 가능한 훅 커맨드 없음")
	}
	return cmds
}

func TestHooksPassSourceFlag(t *testing.T) {
	raw, err := os.ReadFile("hooks/hooks.json")
	if err != nil {
		t.Fatalf("hooks.json 읽기 실패: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("hooks.json 파싱 실패: %v", err)
	}

	for _, cmd := range hookWriteCommands(t, doc) {
		if strings.Contains(cmd, "flint write") {
			if !strings.Contains(cmd, "--source hook") {
				t.Errorf("write 훅 커맨드에 --source hook 없음: %q", cmd)
			}
		}

		if strings.Contains(cmd, "flint hot") || strings.Contains(cmd, "flint read") {
			if strings.Contains(cmd, "--source") {
				t.Errorf("hot/read 훅 커맨드가 변경됨(--source 불필요): %q", cmd)
			}
		}
	}
}

func TestPluginDocsPassSourceHook(t *testing.T) {
	sources := []string{"hook", "manual", "mcp", "correction", "gap"}
	for _, path := range []string{
		"commands/flint.md",
		"skills/flint/SKILL.md",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s 읽기 실패: %v", path, err)
		}
		writeLines := 0
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, "flint write") {
				continue
			}
			writeLines++
			ok := false
			for _, src := range sources {
				if strings.Contains(line, "--source "+src) {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("%s: write 호출에 등록된 --source 없음: %q", path, line)
			}
		}
		if writeLines == 0 {
			t.Errorf("%s: flint write 예시 없음 — 소스 플래그 안내 누락", path)
		}
	}
}
