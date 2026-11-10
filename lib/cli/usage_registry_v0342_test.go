// usage_registry_v0342_test.go — v0.34.2 R2: usage 문장을 usageOrder에서
// 파생 조립한다. 하드코딩 리터럴 시대의 드리프트(pending 누락)를
// 영구적으로 막는 가드.
package cli

import (
	"reflect"
	"strings"
	"testing"
)

// TestUsageLineListsAllRegistered — usageLine은 registry에 등록된 모든
// 사용자 노출 커맨드를 나열해야 한다. session-start/end는 훅 전용
// 진입점이라 제외(wire_v08 TestUsageRestInvariant 계약과 양립 불가).
func TestUsageLineListsAllRegistered(t *testing.T) {
	line := usageLine()
	for name := range registry {
		if hiddenFromUsage(name) {
			continue
		}
		if !strings.Contains(line, name) {
			t.Fatalf("usage missing registered command %q", name)
		}
	}
	if !strings.Contains(line, "pending") {
		t.Fatal("pending missing — R2")
	}
}

// TestUsageOrderMatchesRegistry — usageOrder 집합 == registry 키 집합
// (훅 전용 제외). 신규 커맨드를 Register만 하고 usageOrder에 넣지 않으면
// 이 테스트가 FAIL한다 — 영구 드리프트 가드.
func TestUsageOrderMatchesRegistry(t *testing.T) {
	want := map[string]bool{}
	for name := range registry {
		if !hiddenFromUsage(name) {
			want[name] = true
		}
	}
	got := map[string]bool{}
	for _, name := range usageOrder {
		if got[name] {
			t.Fatalf("usageOrder 중복: %q", name)
		}
		got[name] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("usageOrder/registry 불일치\n usageOrder=%v\nregistry=%v", got, want)
	}
}

// TestUsageLineAssembledFromOrder — usageLine은 usageOrder 순서 그대로
// 조립(리터럴 금지, v0.34.2 Produces 계약).
func TestUsageLineAssembledFromOrder(t *testing.T) {
	want := `{"error":"usage: flint <` + strings.Join(usageOrder, "|") + `> [args]"}`
	if usageLine() != want {
		t.Fatalf("usageLine이 usageOrder 조립과 불일치:\n got=%s\nwant=%s", usageLine(), want)
	}
}
