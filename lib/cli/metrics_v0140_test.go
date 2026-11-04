package cli

import (
	"testing"
)

// v0.15.0 룰링(스펙 §4): BumpZeroNear 제거로 TestBumpZeroNear은
// TestBumpReadStatsUnified(metrics_v0150_test.go)이 승계한다.

// TestBumpReadStatsPreservesZeroNear — 2-read 시퀀스 회귀(코디네이터
// 지정 결함, v0.15.0 단일 라이터로 승계 재작성): 근접 제로히트 read가
// zero_near를 기록한 뒤 다음 read(히트)가 zero_near를 와이프하지 않는다.
// calls·zero는 정상 누적.
func TestBumpReadStatsPreservesZeroNear(t *testing.T) {
	kv := map[string]string{}
	get := func(k string) (string, error) { return kv[k], nil }
	set := func(k, v string) error { kv[k] = v; return nil }
	// read 1: 근접 제로히트 — 단일 호출에 calls/zero/zero_near 전부 기록.
	if err := BumpReadStats(true, true, get, set); err != nil {
		t.Fatal(err)
	}
	// read 2: 히트 read — 기존 zero_near 보존 검증.
	if err := BumpReadStats(false, false, get, set); err != nil {
		t.Fatal(err)
	}
	if v := kv["read_stats"]; v != `{"calls":2,"zero":1,"zero_near":1}` {
		t.Errorf("read_stats=%q, want zero_near 잔존 {\"calls\":2,\"zero\":1,\"zero_near\":1}", v)
	}
	// zero_near 0 보존 시 저장 바이트 불변(omitempty).
	delete(kv, "read_stats")
	if err := BumpReadStats(true, false, get, set); err != nil {
		t.Fatal(err)
	}
	if v := kv["read_stats"]; v != `{"calls":1,"zero":1}` {
		t.Errorf("zero_near 0 바이트 불변 실패: %q", v)
	}
}
