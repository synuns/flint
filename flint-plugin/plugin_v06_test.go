package flintplugin

// T67 plugin --source — 플러그인의 모든 flint write 호출은 --source hook를
// 전달한다(hot/read·done 무변경). 소스 미지정 write 경로 누락 없음.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// hookWriteCommands — hooks.json의 모든 훅 커맨드를 평면화한다.
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

// TestHooksPassSourceFlag — v0.6: JSON 파싱 가능 + write 호출 전수에
// --source hook 포함 + hot/read 호출은 무변경(--source 없음).
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
		// hot/read는 계측을 CLI 내부에서 하므로 훅 명령은 무변경이어야 한다.
		if strings.Contains(cmd, "flint hot") || strings.Contains(cmd, "flint read") {
			if strings.Contains(cmd, "--source") {
				t.Errorf("hot/read 훅 커맨드가 변경됨(--source 불필요): %q", cmd)
			}
		}
	}
}

// TestPluginDocsPassSourceHook — 커맨드·스킬 문서의 flint write 예시
// 전수에 --source hook 포함, write 예시 자체가 존재해야 한다.
func TestPluginDocsPassSourceHook(t *testing.T) {
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
			if !strings.Contains(line, "--source hook") {
				t.Errorf("%s: write 호출에 --source hook 없음: %q", path, line)
			}
		}
		if writeLines == 0 {
			t.Errorf("%s: flint write 예시 없음 — 소스 플래그 안내 누락", path)
		}
	}
}
