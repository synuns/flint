// conf_floor_wire_test.go — meta conf_floor가 쓰기 판정에 도달하는 배선
// (runWrite → loadThresholds → Classify(kind, conf, floor) 값 전달)의 회귀.
// 전역 주입(SetClassifyThresholds)은 폐지 — 판정 기준은 항상 이번 쓰기가
// 읽은 meta 값(갱신 즉시 재적용, TUNING.md §2).
package cli

import (
	"errors"
	"testing"
)

// TestWriteConsumesConfFloor — meta conf_floor=0.6 로드 시 confidence 0.7이
// active로 판정·저장(회귀: 0.8 고정이면 pending).
func TestWriteConsumesConfFloor(t *testing.T) {
	f := &fakeStore{rows: [][]any{{"conf_floor", "0.6"}}}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"cw-a","summary":"s","confidence":0.7}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 1 || f.inserted[0]["status"] != "active" {
		t.Fatalf("conf_floor=0.6에서 conf 0.7 판정=%v, want active", f.inserted)
	}
}

// TestWriteConfFloorLoadFailureKeepsDefault — meta 조회 실패 시 기본 0.8
// 불변(하위호환: 판정 로드 실패가 쓰기 결과·exit을 바꾸지 않는다).
func TestWriteConfFloorLoadFailureKeepsDefault(t *testing.T) {
	f := &fakeStore{queryErr: errors.New("meta down")}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"cw-b","summary":"s","confidence":0.7}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 1 || f.inserted[0]["status"] != "pending" {
		t.Fatalf("조회 실패 시 기본 0.8 판정=%v, want pending", f.inserted)
	}
}

// TestWriteConfFloorZeroAccepted — conf_floor=0(수용 범위 0~1의 하한)도
// 파서·TUNING.md대로 적용된다(회귀: 판정측 경계 거부로 pending).
func TestWriteConfFloorZeroAccepted(t *testing.T) {
	f := &fakeStore{rows: [][]any{{"conf_floor", "0"}}}
	withFake(t, f)
	out, code := mustRun(t, []string{"write", "knowledge"},
		`{"topic":"cw-c","summary":"s","confidence":0.5}`)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.inserted) != 1 || f.inserted[0]["status"] != "active" {
		t.Fatalf("conf_floor=0에서 conf 0.5 판정=%v, want active", f.inserted)
	}
}
