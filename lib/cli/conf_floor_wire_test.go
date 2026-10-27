// conf_floor_wire_test.go — meta conf_floor가 쓰기 판정 Classify에 도달하는
// 배선(newRealStore → applyClassifyThresholds → store.SetClassifyThresholds)의
// 회귀. write.go 판정 호출부는 그대로 두되(시그니처 불변), 생성 시점 주입으로
// conf_floor를 소비한다.
package cli

import (
	"context"
	"errors"
	"testing"

	"flint/lib/store"
)

// TestApplyClassifyThresholdsWiresConfFloor — meta conf_floor=0.6 로드 시
// Classify(knowledge, 0.7)이 active(회귀: 0.8 고정이면 pending).
func TestApplyClassifyThresholdsWiresConfFloor(t *testing.T) {
	t.Cleanup(func() { store.SetClassifyThresholds(store.DefaultThresholds()) })
	f := &fakeStore{rows: [][]any{{"conf_floor", "0.6"}}}
	applyClassifyThresholds(context.Background(), f)
	if got := store.Classify("knowledge", 0.7); got != "active" {
		t.Errorf("conf_floor=0.6 배선 후 Classify(knowledge, 0.7) = %q, want active", got)
	}
	if got := store.Classify("knowledge", 0.5); got != "pending" {
		t.Errorf("conf_floor=0.6 배선 후 Classify(knowledge, 0.5) = %q, want pending", got)
	}
}

// TestApplyClassifyThresholdsFailureKeepsDefault — meta 조회 실패 시 기본 0.8
// 불변(하위호환: 판정 로드 실패가 쓰기 결과·exit을 바꾸지 않는다).
func TestApplyClassifyThresholdsFailureKeepsDefault(t *testing.T) {
	t.Cleanup(func() { store.SetClassifyThresholds(store.DefaultThresholds()) })
	store.SetClassifyThresholds(store.DefaultThresholds())
	f := &fakeStore{queryErr: errors.New("meta down")}
	applyClassifyThresholds(context.Background(), f)
	if got := store.Classify("knowledge", 0.7); got != "pending" {
		t.Errorf("조회 실패 후 Classify(knowledge, 0.7) = %q, want pending(기본 0.8)", got)
	}
}
