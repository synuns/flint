// registry_test.go — v0.38.0 T3: 계약층 registry 읽기 접근자 단위 검증.
// bridge 동질 가드(실 cli 등록 ↔ usageOrder 대조)는 lib/cli
// usage_bridge_test.go가 담당 — 여기는 접근자 자체의 계약만.
package wire

import (
	"context"
	"io"
	"sort"
	"testing"
)

// TestRegisteredEnumerates — Registered는 Register 후 포함·Unregister 후
// 제외되는 등록 표면의 충실한 열거(정렬 순).
func TestRegisteredEnumerates(t *testing.T) {
	before := map[string]bool{}
	for _, n := range Registered() {
		before[n] = true
	}
	Register("probe-accessor", func(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int { return 0 })
	t.Cleanup(func() { Unregister("probe-accessor") })
	got := Registered()
	if sort.SearchStrings(got, "probe-accessor") >= len(got) || got[sort.SearchStrings(got, "probe-accessor")] != "probe-accessor" {
		t.Fatalf("Registered에 등록 항목 없음: %v", got)
	}
	// 정렬 계약 — 호출부 비교 안정성.
	if !sort.StringsAreSorted(got) {
		t.Fatalf("Registered 미정렬: %v", got)
	}
	Unregister("probe-accessor")
	for _, n := range Registered() {
		if n == "probe-accessor" {
			t.Fatal("Unregister 후에도 Registered에 잔존")
		}
	}
}

// TestUsageOrderCopyNotAlias — UsageOrder는 사본 반환 — 호출부 변경이
// 내부 usageOrder를 오염시키지 않는다.
func TestUsageOrderCopyNotAlias(t *testing.T) {
	got := UsageOrder()
	if len(got) == 0 {
		t.Fatal("UsageOrder 빈 슬라이스")
	}
	got[0] = "mutated"
	if UsageOrder()[0] == "mutated" {
		t.Fatal("UsageOrder가 내부 슬라이스 별칭 — 사본이어야 함")
	}
}

// TestHiddenFromUsagePublic — 훅 전용 2종만 true.
func TestHiddenFromUsagePublic(t *testing.T) {
	for _, name := range []string{"session-start", "session-end"} {
		if !HiddenFromUsage(name) {
			t.Errorf("HiddenFromUsage(%q)=false, want true", name)
		}
	}
	for _, name := range []string{"write", "compact", "nudge"} {
		if HiddenFromUsage(name) {
			t.Errorf("HiddenFromUsage(%q)=true, want false", name)
		}
	}
}
