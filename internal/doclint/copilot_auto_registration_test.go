package doclint

import (
	"strings"
	"testing"
)

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

	if !strings.Contains(d, "mcp-config.json") {
		t.Error("adapters/copilot.md: 수동 스니펫(mcp-config.json)이 삭제됨 — 대체 절차로 유지 필요")
	}
}

func TestQuickstartCopilotAuto(t *testing.T) {
	d := docStr(t, "QUICKSTART.md")
	if strings.Contains(d, "Copilot은 수동") {
		t.Error("QUICKSTART.md: 'Copilot은 수동 설정' 잔존 — 자동 등록 서술로 정정 필요")
	}
	if !strings.Contains(d, "Copilot") {
		t.Error("QUICKSTART.md: Copilot 서술 누락")
	}
}

func TestQuickstartDoctorCheckCount(t *testing.T) {
	d := docStr(t, "QUICKSTART.md")
	if !strings.Contains(d, "30 checks") {
		t.Error("QUICKSTART.md: '30 checks' 누락 — v1.37.0 (코어 6 + tail 24, smoke 편입) 반영")
	}
	if strings.Contains(d, "29 checks") {
		t.Error("QUICKSTART.md: '29 checks' 잔존 — 30종(코어 6 + tail 24)이 실제")
	}
	if strings.Contains(d, "24 checks") {
		t.Error("QUICKSTART.md: '24 checks' 잔존 — 30종(코어 6 + tail 24)이 실제")
	}
	if strings.Contains(d, "23 checks") {
		t.Error("QUICKSTART.md: '23 checks' 잔존 — 30종(코어 6 + tail 24)이 실제")
	}
	if strings.Contains(d, "21 checks") {
		t.Error("QUICKSTART.md: '21 checks' 잔존 — 30종(코어 6 + tail 24)이 실제")
	}
}
