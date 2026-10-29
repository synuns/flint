package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

// TestApplyHookFixReadErrorNotMisreadAsMissing — os.ErrNotExist 외 읽기
// 오류(권한 등)는 신규 생성으로 오분류되지 않고 그대로 err로 나가야
// 한다(파일·백업 무변경).
func TestApplyHookFixReadErrorNotMisreadAsMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 시맨틱 미지원")
	}
	if os.Geteuid() == 0 {
		t.Skip("루트는 권한 비트 무시 — 읽기 오류 재현 불가")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	orig := []byte(`{"hooks":{}}`)
	if err := os.WriteFile(p, orig, 0o000); err != nil {
		t.Fatalf("쓰기 실패: %v", err)
	}
	applied, backup, err := applyHookFix(p, "/x/flint")
	if err == nil {
		t.Fatal("권한 오류가 조용히 무시됨 — 신규 생성 오분류 위험")
	}
	if applied {
		t.Error("읽기 불가인데 applied=true")
	}
	if backup != "" {
		t.Errorf("읽기 실패 경로에 백업 생성됨: %q", backup)
	}
	// 원본 권한 복구(템프디렉터리 정리 보장).
	_ = os.Chmod(p, 0o644)
	got, rerr := os.ReadFile(p)
	if rerr != nil || !bytes.Equal(got, orig) {
		t.Errorf("원본 무변경 위반: %q (err=%v)", got, rerr)
	}
}
