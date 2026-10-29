package flintplugin

import (
	"os"
	"strings"
	"testing"
)

func skillStr(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("skills/flint/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSkillHasDuplicateIDRule(t *testing.T) {
	s := skillStr(t)
	for _, kw := range []string{"duplicate", "id", "재제출"} {
		if !strings.Contains(s, kw) {
			t.Errorf("dup 규칙 키워드 누락: %q", kw)
		}
	}
}

func TestSkillHasPreDistillRead(t *testing.T) {
	s := skillStr(t)
	if !strings.Contains(s, "증류 전") || !strings.Contains(s, "flint read") {
		t.Error("증류 전 사전 read 규칙 누락")
	}
}
