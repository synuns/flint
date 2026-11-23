package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

var hookOnlyDeclared = map[string]bool{
	"session-start": true,
	"session-end":   true,
}

func hiddenAudit(reg, order []string, declared map[string]bool) []string {
	inOrder := map[string]bool{}
	for _, name := range order {
		inOrder[name] = true
	}
	hidden := map[string]bool{}
	for _, name := range reg {
		if !inOrder[name] {
			hidden[name] = true
		}
	}
	var problems []string
	for name := range hidden {
		if !declared[name] {
			problems = append(problems, fmt.Sprintf(
				"등록 커맨드 %q이 usageOrder 밖 은닉 — hidden 고정 선언 갱신(의도 서술) 필요", name))
		}
	}
	for name := range declared {
		if !hidden[name] {
			problems = append(problems, fmt.Sprintf(
				"hidden 고정 선언 %q이 실측 은닉 집합에 없음 — 선언 갱신 필요", name))
		}
	}
	return problems
}

func TestRegisteredMatchesUsage(t *testing.T) {
	order := map[string]bool{}
	for _, name := range wire.UsageOrder() {
		if order[name] {
			t.Fatalf("usageOrder 중복: %q", name)
		}
		order[name] = true
	}
	reg := map[string]bool{}
	for _, name := range wire.Registered() {
		if reg[name] {
			t.Fatalf("registry 중복: %q", name)
		}
		reg[name] = true
	}

	for _, p := range hiddenAudit(wire.Registered(), wire.UsageOrder(), hookOnlyDeclared) {
		t.Error(p)
	}

	for name := range reg {
		if wire.HiddenFromUsage(name) != !order[name] {
			t.Errorf("hiddenFromUsage(%q)가 usageOrder 노출 여부와 상충 — 은닉 표기 정합 필요", name)
		}
	}

	for name := range order {
		if !reg[name] {
			t.Errorf("usageOrder 항목 %q이 registry에 미등록", name)
		}
	}

	for _, name := range []string{"write", "read", "hot", "status", "backup", "restore"} {
		if !reg[name] {
			t.Errorf("핵심 커맨드 %q 미등록", name)
		}
	}
}

func TestHiddenAuditFlagsSilentHide(t *testing.T) {
	reg := []string{"write", "read", "session-start", "session-end", "shadow-cmd"}
	order := []string{"write", "read"}
	declared := map[string]bool{"session-start": true, "session-end": true}
	problems := hiddenAudit(reg, order, declared)
	if len(problems) != 1 || !strings.Contains(problems[0], "shadow-cmd") {
		t.Fatalf("조용한 은닉 미검출: %v", problems)
	}

	stale := hiddenAudit([]string{"write"}, []string{"write"}, declared)
	if len(stale) != 2 {
		t.Fatalf("낡은 선언 미검출: %v", stale)
	}

	ok := hiddenAudit([]string{"write", "session-start", "session-end"}, []string{"write"}, declared)
	if len(ok) != 0 {
		t.Fatalf("정합 오탐: %v", ok)
	}
}
