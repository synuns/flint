// metrics_test.go — v0.42.0 T5 Review Focus: hotLens 상한 절단 시
// hotPersisted 오프셋 정합(fr-v0400 기록 엣지). 절단이 persisted 선두를
// 깎으면 오프셋도 함께 줄어야 한다 — 그렇지 않으면 미저장(pending)
// 샘플이 절단분만큼 누락되어 calls·p95가 과소 보고된다.
package ops

import "testing"

// TestHotLensTruncateConsistency — 상한 초과 세션에서 lens·persisted 정합.
// 1단계: persist 후 추가 기록이 상한을 넘어 절단 — pending 전량 유지.
func TestHotLensTruncateConsistency(t *testing.T) {
	ResetMetrics()
	// 1회차: 400 샘플 기록 후 persist(전량 baseline 이관).
	for i := 0; i < 400; i++ {
		RecordHotLen(10)
	}
	if err := PersistHotStats(func(_, _ string) error { return nil }); err != nil {
		t.Fatalf("persist(1) 오류: %v", err)
	}
	// 2단계: 장기 실행 세션 시뮬레이션 — 200 샘플 추가로 총 600 →
	// 선두 100(persisted분) 절단 → 보관 500 = persisted 300 + pending 200.
	for i := 0; i < 200; i++ {
		RecordHotLen(20)
	}
	calls, p95, max := HotStats()
	// baseline(400) + pending(200) = 600 — 절단으로 pending이 줄면 안 된다.
	if calls != 600 {
		t.Fatalf("calls=%d, want 600(pending 200 전량 반영)", calls)
	}
	// 정렬 샘플: 10×400 + 20×200 → p95 인덱스 ceil(0.95*600)=570 → 20.
	if p95 != 20 {
		t.Fatalf("p95=%d, want 20", p95)
	}
	if max != 20 {
		t.Fatalf("max=%d, want 20", max)
	}
	// 3단계: 재 persist — 누적도 600을 유지해야 한다(과소 합산 금지).
	stored := ""
	if err := PersistHotStats(func(_, v string) error { stored = v; return nil }); err != nil {
		t.Fatalf("persist(2) 오류: %v", err)
	}
	ResetMetrics() // 새 프로세스 시뮬레이션
	gotCalls, _, ok := LoadHotStats(func(string) (string, error) { return stored, nil })
	if !ok || gotCalls != 600 {
		t.Fatalf("왕복 calls=%d ok=%v, want 600/true", gotCalls, ok)
	}
}

// TestHotLensTruncatePastPersisted — 절단이 persisted 경계를 완전히 넘는
// 경우: 절단분 전체가 미저장 샘플이면 오프셋은 0으로 바닥 붙는다.
func TestHotLensTruncatePastPersisted(t *testing.T) {
	ResetMetrics()
	for i := 0; i < 10; i++ {
		RecordHotLen(5)
	}
	if err := PersistHotStats(func(_, _ string) error { return nil }); err != nil {
		t.Fatalf("persist 오류: %v", err)
	}
	// 610 추가 → 총 620 → 선두 120 절단(persisted 10 포함) →
	// 보관 500은 전량 미저장 → pending = 500.
	for i := 0; i < 610; i++ {
		RecordHotLen(7)
	}
	calls, _, _ := HotStats()
	if want := 10 + 500; calls != want {
		t.Fatalf("calls=%d, want %d(baseline 10 + pending 500)", calls, want)
	}
}
