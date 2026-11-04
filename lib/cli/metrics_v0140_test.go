package cli

import (
	"strings"
	"testing"
)

func TestBumpZeroNear(t *testing.T) {
	kv := map[string]string{}
	get := func(k string) (string, error) { return kv[k], nil }
	set := func(k, v string) error { kv[k] = v; return nil }
	if err := BumpZeroNear(get, set); err != nil {
		t.Fatal(err)
	}
	if v := kv["read_stats"]; !strings.Contains(v, `"zero_near":1`) {
		t.Errorf("zero_near 미증분: %s", v)
	}
	kv["read_stats"] = `{"calls":5,"zero":2,"zero_near":1}`
	if err := BumpZeroNear(get, set); err != nil {
		t.Fatal(err)
	}
	v := kv["read_stats"]
	for _, want := range []string{`"calls":5`, `"zero":2`, `"zero_near":2`} {
		if !strings.Contains(v, want) {
			t.Errorf("기존 필드 보존 실패(%s): %s", want, v)
		}
	}
}

// TestBumpReadStatsPreservesZeroNear — 2-read 시퀀스 회귀(코디네이터
// 지정 결함): 근접 제로히트 read가 zero_near를 기록한 뒤 다음 read(히트)의
// BumpReadStats가 zero_near를 와이프하지 않는다. calls·zero는 정상 누적.
func TestBumpReadStatsPreservesZeroNear(t *testing.T) {
	kv := map[string]string{}
	get := func(k string) (string, error) { return kv[k], nil }
	set := func(k, v string) error { kv[k] = v; return nil }
	// read 1: 근접 제로히트 — calls/zero는 BumpReadStats가, zero_near는
	// BumpZeroNear가 각각 기록(read.go 실행 순서와 동일).
	if err := BumpReadStats(true, get, set); err != nil {
		t.Fatal(err)
	}
	if err := BumpZeroNear(get, set); err != nil {
		t.Fatal(err)
	}
	// read 2: 히트 read — 기존 zero_near 보존 검증.
	if err := BumpReadStats(false, get, set); err != nil {
		t.Fatal(err)
	}
	if v := kv["read_stats"]; v != `{"calls":2,"zero":1,"zero_near":1}` {
		t.Errorf("read_stats=%q, want zero_near 잔존 {\"calls\":2,\"zero\":1,\"zero_near\":1}", v)
	}
	// zero_near 0 보존 시 저장 바이트 불변(omitempty).
	delete(kv, "read_stats")
	if err := BumpReadStats(true, get, set); err != nil {
		t.Fatal(err)
	}
	if v := kv["read_stats"]; v != `{"calls":1,"zero":1}` {
		t.Errorf("zero_near 0 바이트 불변 실패: %q", v)
	}
}
