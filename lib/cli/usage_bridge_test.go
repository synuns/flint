// usage_bridge_test.go — v0.38.0 T3 bridge 가드(룰링 편입 소유). T1에서
// usage 드리프트 가드가 wire 내부 시드 기반으로 이동하며 생긴 공백 —
// cli init이 등록한 실 커맨드 표면 ↔ wire usageOrder(+훅 전용) 동질성 —
// 을 cli 패키지(실등록 주체)에서 직접 검증한다. cli→wire 임포트는
// 프로덕션 의존 방향과 동일해 사이클이 없다.
package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

// hookOnlyDeclared — hidden(usage 비노출) 집합 고정 선언. v1.0.1 R1-D:
// hiddenFromUsage에 이름을 추가하는 것만으로 커맨드를 usage에서 조용히
// 지우는 남용이 가능했다 — 이제 hidden 실측 집합이 이 선언과 정확히
// 일치해야 한다. 신규 hidden 추가는 이 표 갱신(= 의도 서술)을 수반하는
// 의도적 행위여야 한다.
var hookOnlyDeclared = map[string]bool{
	"session-start": true,
	"session-end":   true,
}

// hiddenAudit — 등록·usageOrder·고정 선언 3자 정합 검사. 문제 목록을
// 반환한다(등록됐으나 usage·선언 어디에도 없음 / 선언됐으나 실측
// hidden 아님).
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

// TestRegisteredMatchesUsage — Review Focus: wire.Registered() 집합 ==
// UsageOrder ∪ hidden(고정 선언). 신규 커맨드를 Register만 하고
// usageOrder에 넣지 않으면(또는 그 반대) FAIL — 영구 드리프트 가드.
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
	// hidden 실측(등록·비usage) == 고정 선언 — 조용한 은닉 불가.
	for _, p := range hiddenAudit(wire.Registered(), wire.UsageOrder(), hookOnlyDeclared) {
		t.Error(p)
	}
	// hiddenFromUsage 판정 자체도 usageOrder 멤버십과 상충하면 안 된다
	// (노출 커맨드를 hiddenFromUsage에 끼워 넣는 남용 방어).
	for name := range reg {
		if wire.HiddenFromUsage(name) != !order[name] {
			t.Errorf("hiddenFromUsage(%q)가 usageOrder 노출 여부와 상충 — 은닉 표기 정합 필요", name)
		}
	}
	// usageOrder 항목은 실제 등록이어야 한다(죽은 항목 금지).
	for name := range order {
		if !reg[name] {
			t.Errorf("usageOrder 항목 %q이 registry에 미등록", name)
		}
	}
	// 핵심 커맨드 상시 등록(구 registry.go init 이관 확인).
	for _, name := range []string{"write", "read", "hot", "status", "backup", "restore"} {
		if !reg[name] {
			t.Errorf("핵심 커맨드 %q 미등록", name)
		}
	}
}

// TestHiddenAuditFlagsSilentHide — R1-D 회귀: 신규 커맨드를 등록하고
// 고정 선언 갱신 없이 hidden으로 돌리는 남용은 감사가 즉시 잡는다
// (반대로 선언만 남는 경우도). 합성 입력으로 단언.
func TestHiddenAuditFlagsSilentHide(t *testing.T) {
	reg := []string{"write", "read", "session-start", "session-end", "shadow-cmd"}
	order := []string{"write", "read"}
	declared := map[string]bool{"session-start": true, "session-end": true}
	problems := hiddenAudit(reg, order, declared)
	if len(problems) != 1 || !strings.Contains(problems[0], "shadow-cmd") {
		t.Fatalf("조용한 은닉 미검출: %v", problems)
	}
	// 선언 과잉(실측 hidden에 없는 선언 항목)도 잡는다.
	stale := hiddenAudit([]string{"write"}, []string{"write"}, declared)
	if len(stale) != 2 {
		t.Fatalf("낡은 선언 미검출: %v", stale)
	}
	// 정합 상태는 문제 0(선언 2종 모두 실측 hidden에 존재).
	ok := hiddenAudit([]string{"write", "session-start", "session-end"}, []string{"write"}, declared)
	if len(ok) != 0 {
		t.Fatalf("정합 오탐: %v", ok)
	}
}
