// metrics_test.go — hotPersisted offset consistency when hotLens is
// truncated at the cap. When a truncation shaves the front of persisted,
// the offset must shrink with it — otherwise the unsaved (pending)
// samples lose exactly the truncated amount and calls/p95 are under-reported.
package ops

import "testing"

// TestHotLensTruncateConsistency — lens/persisted consistency in a session exceeding the cap.
// Step 1: after persist, additional records exceed the cap and truncate — all pending kept.
func TestHotLensTruncateConsistency(t *testing.T) {
	ResetMetrics()
	// round 1: record 400 samples then persist (all moved to the baseline).
	for i := 0; i < 400; i++ {
		RecordHotLen(10)
	}
	if err := PersistHotStats(func(_, _ string) error { return nil }); err != nil {
		t.Fatalf("persist(1) 오류: %v", err)
	}
	// step 2: simulate a long-running session — 200 more samples, 600 total ->
	// the front 100 (the persisted part) truncated -> 500 kept = persisted 300 + pending 200.
	for i := 0; i < 200; i++ {
		RecordHotLen(20)
	}
	calls, p95, max := HotStats()
	// baseline (400) + pending (200) = 600 — the pending must not shrink from the truncation.
	if calls != 600 {
		t.Fatalf("calls=%d, want 600(pending 200 전량 반영)", calls)
	}
	// sorted samples: 10x400 + 20x200 -> p95 index ceil(0.95*600)=570 -> 20.
	if p95 != 20 {
		t.Fatalf("p95=%d, want 20", p95)
	}
	if max != 20 {
		t.Fatalf("max=%d, want 20", max)
	}
	// step 3: re-persist — the cumulative must also stay 600 (no under-summing).
	stored := ""
	if err := PersistHotStats(func(_, v string) error { stored = v; return nil }); err != nil {
		t.Fatalf("persist(2) 오류: %v", err)
	}
	ResetMetrics() // simulate a new process
	gotCalls, _, ok := LoadHotStats(func(string) (string, error) { return stored, nil })
	if !ok || gotCalls != 600 {
		t.Fatalf("왕복 calls=%d ok=%v, want 600/true", gotCalls, ok)
	}
}

// TestHotLensTruncatePastPersisted — when the truncation crosses the persisted
// boundary entirely: if the whole truncated portion is unsaved samples, the offset bottoms out at 0.
func TestHotLensTruncatePastPersisted(t *testing.T) {
	ResetMetrics()
	for i := 0; i < 10; i++ {
		RecordHotLen(5)
	}
	if err := PersistHotStats(func(_, _ string) error { return nil }); err != nil {
		t.Fatalf("persist 오류: %v", err)
	}
	// 610 more -> 620 total -> the front 120 truncated (including 10 persisted) ->
	// the 500 kept are all unsaved -> pending = 500.
	for i := 0; i < 610; i++ {
		RecordHotLen(7)
	}
	calls, _, _ := HotStats()
	if want := 10 + 500; calls != want {
		t.Fatalf("calls=%d, want %d(baseline 10 + pending 500)", calls, want)
	}
}
