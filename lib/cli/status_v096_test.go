// status_v096_test.go — v0.9.6 T2: status 출력에 read_fallback_calls(T1
// 계측 meta) 노출 검증. 키 "3" → 3, 미설정 → null. 기존 필드·순서는
// status_v06/v08 리플렉션 테스트가 담보.
package cli

import "testing"

func TestStatusIncludesFallbackField(t *testing.T) {
	// schema_version은 헬퍼 특성상 kv로 심어야 exit 3(스키마 불일치)을 피한다.
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
