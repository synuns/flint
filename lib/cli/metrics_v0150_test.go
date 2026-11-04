package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestBumpReadStatsUnified(t *testing.T) {
	kv := map[string]string{}
	get := func(k string) (string, error) { return kv[k], nil }
	set := func(k, v string) error { kv[k] = v; return nil }
	if err := BumpReadStats(true, true, get, set); err != nil {
		t.Fatal(err)
	}
	if v := kv["read_stats"]; !strings.Contains(v, `"calls":1`) || !strings.Contains(v, `"zero":1`) || !strings.Contains(v, `"zero_near":1`) {
		t.Errorf("제로히트+근접: %s", v)
	}
	kv["read_stats"] = `{"calls":5,"zero":2,"zero_near":1}`
	if err := BumpReadStats(false, false, get, set); err != nil {
		t.Fatal(err)
	}
	v := kv["read_stats"]
	for _, want := range []string{`"calls":6`, `"zero":2`, `"zero_near":1`} {
		if !strings.Contains(v, want) {
			t.Errorf("보존 실패(%s): %s", want, v)
		}
	}
	// 와이프 방어(v0.14.0 fix 승계): getKV 전송 오류 시 setKV 미호출·기존 stats 보존.
	boom := errors.New("kv transport down")
	sets := 0
	errSet := func(k, val string) error { sets++; return nil }
	if err := BumpReadStats(true, false, func(string) (string, error) { return "", boom }, errSet); !errors.Is(err, boom) {
		t.Errorf("getKV 전송 오류 미반환: %v", err)
	}
	if sets != 0 {
		t.Errorf("setKV %d회 호출, want 0(전송 오류 시 기존 stats 보존)", sets)
	}
}
