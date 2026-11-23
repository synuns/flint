package cli

import "testing"

func TestStatusIncludesFallbackField(t *testing.T) {

	res, code := statusWithMeta(t, map[string]string{"schema_version": "3", "read_fallback_calls": "3"})
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if res["read_fallback_calls"] != float64(3) {
		t.Errorf("read_fallback_calls = %v, want 3", res["read_fallback_calls"])
	}
}

func TestStatusFallbackFieldNull(t *testing.T) {
	res, code := statusWithMeta(t, map[string]string{"schema_version": "3"})
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if res["read_fallback_calls"] != nil {
		t.Errorf("미설정 키 = %v, want nil", res["read_fallback_calls"])
	}
}
