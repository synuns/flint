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
