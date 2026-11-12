// usage_bridge_test.go — v0.38.0 T3 bridge 가드(룰링 편입 소유). T1에서
// usage 드리프트 가드가 wire 내부 시드 기반으로 이동하며 생긴 공백 —
// cli init이 등록한 실 커맨드 표면 ↔ wire usageOrder(+훅 전용) 동질성 —
// 을 cli 패키지(실등록 주체)에서 직접 검증한다. cli→wire 임포트는
// 프로덕션 의존 방향과 동일해 사이클이 없다.
package cli

import (
	"testing"

	"github.com/synuns/flint/lib/wire"
)

// TestRegisteredMatchesUsage — Review Focus: wire.Registered() 집합 ==
// UsageOrder ∪ HiddenFromUsage 집합. 신규 커맨드를 Register만 하고
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
	// 등록됐으나 usage에 없는 이름은 훅 전용이어야 한다.
	for name := range reg {
		if !order[name] && !wire.HiddenFromUsage(name) {
			t.Errorf("등록 커맨드 %q이 usageOrder·훅 전용 어디에도 없음 — usageOrder 갱신 필요", name)
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
