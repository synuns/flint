// usage_bridge_test.go — bridge guards (owned as part of the ruling).
// The gap left when the usage-drift guard moved to the wire-internal seed —
// the homogeneity between the real command surface registered by cli init and wire usageOrder (+hook-only) —
// is now verified directly in the cli package (the registering owner). The cli->wire import
// matches the production dependency direction, so there is no cycle.
package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

// hookOnlyDeclared — a fixed declaration of the hidden (not shown in usage) set.
// It used to be possible to silently remove a command from usage merely by adding its name to
// hiddenFromUsage — now the observed hidden set must exactly match this declaration.
// Adding a new hidden command must be an intentional act that updates this table
// (= stating the intent).
var hookOnlyDeclared = map[string]bool{
	"session-start": true,
	"session-end":   true,
}

// hiddenAudit — a 3-way consistency check across registration, usageOrder, and the fixed declaration. Returns the
// problem list (registered but in neither usage nor the declaration / declared but not actually
// hidden).
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

// TestRegisteredMatchesUsage — the wire.Registered() set ==
// UsageOrder ∪ hidden (fixed declaration). Registering a new command without putting it in
// usageOrder (or vice versa) FAILs — a permanent drift guard.
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
	// observed hidden (registered, non-usage) == the fixed declaration — no silent hiding.
	for _, p := range hiddenAudit(wire.Registered(), wire.UsageOrder(), hookOnlyDeclared) {
		t.Error(p)
	}
	// the hiddenFromUsage determination itself must not conflict with usageOrder membership either
	// (defense against smuggling an exposed command into hiddenFromUsage).
	for name := range reg {
		if wire.HiddenFromUsage(name) != !order[name] {
			t.Errorf("hiddenFromUsage(%q)가 usageOrder 노출 여부와 상충 — 은닉 표기 정합 필요", name)
		}
	}
	// usageOrder entries must be actually registered (no dead entries).
	for name := range order {
		if !reg[name] {
			t.Errorf("usageOrder 항목 %q이 registry에 미등록", name)
		}
	}
	// core commands always registered (verifying the migration of the former registry.go init).
	for _, name := range []string{"write", "read", "hot", "status", "backup", "restore"} {
		if !reg[name] {
			t.Errorf("핵심 커맨드 %q 미등록", name)
		}
	}
}

// TestHiddenAuditFlagsSilentHide — regression: registering a new command and turning it
// hidden without updating the fixed declaration is caught immediately by the audit
// (and so is a stale declaration). Asserted with synthetic input.
func TestHiddenAuditFlagsSilentHide(t *testing.T) {
	reg := []string{"write", "read", "session-start", "session-end", "shadow-cmd"}
	order := []string{"write", "read"}
	declared := map[string]bool{"session-start": true, "session-end": true}
	problems := hiddenAudit(reg, order, declared)
	if len(problems) != 1 || !strings.Contains(problems[0], "shadow-cmd") {
		t.Fatalf("조용한 은닉 미검출: %v", problems)
	}
	// over-declaration (declaration entries not in the observed hidden set) is caught too.
	stale := hiddenAudit([]string{"write"}, []string{"write"}, declared)
	if len(stale) != 2 {
		t.Fatalf("낡은 선언 미검출: %v", stale)
	}
	// the consistent state has 0 problems (both declared kinds exist in the observed hidden set).
	ok := hiddenAudit([]string{"write", "session-start", "session-end"}, []string{"write"}, declared)
	if len(ok) != 0 {
		t.Fatalf("정합 오탐: %v", ok)
	}
}
