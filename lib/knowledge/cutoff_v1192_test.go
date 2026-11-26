package knowledge

import (
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/store"
)

func TestVerdictForPendingAgeOverflow(t *testing.T) {
	now := time.Now()
	th := store.DefaultThresholds()
	th.PendingAgeDays = 200000
	it := pendingItem{ID: 7, Topic: "demo-topic", Summary: "demo summary", ValidAt: now.Add(-200 * 24 * time.Hour)}
	v := verdictFor(it, th, now, nil)
	if v.Kind == "expire" {
		t.Fatalf("판정 = expire(%s), want expire 아님(래프 시 전 건 만료 판정)", v.Why)
	}
	if v.Kind != "hold" {
		t.Fatalf("판정 = %s, want hold", v.Kind)
	}
}

func TestVerdictForPromoteOverflow(t *testing.T) {
	now := time.Now()
	th := store.DefaultThresholds()
	th.PendingPromoteDays = 200000
	it := pendingItem{ID: 8, Topic: "demo-topic", Summary: strings.Repeat("요약", 30), ValidAt: now.Add(-1 * time.Hour)}
	v := verdictFor(it, th, now, nil)
	if v.Kind != "promote" {
		t.Fatalf("판정 = %s(%s), want promote(래프 시 승급 불가)", v.Kind, v.Why)
	}
}

