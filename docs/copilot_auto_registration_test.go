// copilot_auto_registration_test.go — copilot 자동 등록 정정·QUICKSTART 검사 수 회귀.
package docs

import (
	"strings"
	"testing"
)

// TestCopilotAdapterDocAutoRegistration — copilot.md 서두가 자동 등록 대상(v0.21.0
// A급 승격)을 설명하고, 수동 절차는 대체 절차로 남아 있어야 한다.
func TestCopilotAdapterDocAutoRegistration(t *testing.T) {
	d := docStr(t, "adapters/copilot.md")
	for _, w := range []string{
		"flint setup --mcp",
		"자동 등록",
		"v0.21.0",
		"대체 절차",
	} {
		if !strings.Contains(d, w) {
			t.Errorf("adapters/copilot.md: %q 누락", w)
		}
	}
	if strings.Contains(d, "자동 등록 대상이\n아니다") || strings.Contains(d, "자동 등록 대상이 아니다") {
		t.Error("adapters/copilot.md: '자동 등록 대상이 아니다' 잔존 — v0.21.0 이후 부정확")
	}
	// 수동 스니펫 본문은 유지(대체 절차).
	if !strings.Contains(d, "mcp-config.json") {
		t.Error("adapters/copilot.md: 수동 스니펫(mcp-config.json)이 삭제됨 — 대체 절차로 유지 필요")
	}
}

// TestQuickstartCopilotAuto — QUICKSTART: Copilot 자동 등록 서술.
func TestQuickstartCopilotAuto(t *testing.T) {
	d := docStr(t, "QUICKSTART.md")
	if strings.Contains(d, "Copilot은 수동") {
		t.Error("QUICKSTART.md: 'Copilot은 수동 설정' 잔존 — 자동 등록 서술로 정정 필요")
	}
	if !strings.Contains(d, "Copilot") {
		t.Error("QUICKSTART.md: Copilot 서술 누락")
	}
}

// TestQuickstartDoctorCheckCount — 검사 16종(코어 6 + tail 10, harness-*·
// skill-file 제외 구조 수치).
func TestQuickstartDoctorCheckCount(t *testing.T) {
	d := docStr(t, "QUICKSTART.md")
	if !strings.Contains(d, "검사 16종") {
		t.Error("QUICKSTART.md: '검사 16종' 누락")
	}
	if strings.Contains(d, "검사 10종") {
		t.Error("QUICKSTART.md: '검사 10종' 잔존 — 16종(코어 6 + tail 10)이 실제")
	}
}
