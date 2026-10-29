package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyHookFixCreatesMissingSettings(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".claude", "settings.json")
	applied, backup, err := applyHookFix(p, "/x/flint")
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if backup != "" {
		t.Errorf("신규 생성에 백업 불필요: %q", backup)
	}
	b, rerr := os.ReadFile(p)
	if rerr != nil {
		t.Fatalf("파일 미생성: %v", rerr)
	}
	var got map[string]any
	if uerr := json.Unmarshal(b, &got); uerr != nil {
		t.Fatalf("생성 파일 파싱 실패: %v", uerr)
	}
	if _, ok := got["hooks"]; !ok {
		t.Error("hooks 키 누락")
	}
	if !strings.Contains(string(b), "/x/flint") {
		t.Error("flint 훅 미포함")
	}
}
