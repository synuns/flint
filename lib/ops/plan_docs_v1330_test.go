package ops

import (
	"os"
	"strings"
	"testing"
)

func TestPlanDocsSync(t *testing.T) {
	data, err := os.ReadFile("../../docs/PLAN.md")
	if err != nil {
		t.Fatalf("PLAN.md: %v", err)
	}
	doc := string(data)
	if !strings.Contains(doc, "2뷰 + serve — 동일 Diagnose 원천") {
		t.Fatal("PLAN.md에 '2뷰 + serve — 동일 Diagnose 원천' 문구 부재")
	}
	if strings.Contains(doc, "대시보드 동적 서버(정적 export만 유지)") {
		t.Fatal("PLAN.md §7 대시보드 동적 서버 비목표 미해제")
	}
}
