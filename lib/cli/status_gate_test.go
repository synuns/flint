// status_gate_test.go — schema_version 호환 검사 게이트(룰링: {"1","2"} 허용).
// v2 승격(newRealStore→EnsureSchemaV2) 후에도 status가 exit 0을 유지하는지,
// 미지 버전은 여전히 exit 3으로 거부하는지를 fast로 검증한다(e2e ④의 백엔드 회귀).
package cli

import (
	"strings"
	"testing"
)

// statusWithVersion — meta.schema_version이 version인 fake 저장소로 status 실행.
func statusWithVersion(t *testing.T, version string) (map[string]any, int) {
	t.Helper()
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return [][]any{{version}}, nil
		}
		return [][]any{{int64(0)}}, nil
	}
	withFake(t, f)
	out, code := mustRun(t, []string{"status"}, "")
	var m map[string]any
	if code == 0 {
		m = decode(t, out)
	}
	return m, code
}

// TestStatusSchemaV2OK — v2 DB(승격 완료)에서 status는 exit 0·버전 그대로 보고.
func TestStatusSchemaV2OK(t *testing.T) {
	m, code := statusWithVersion(t, "2")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(v2 호환)", code)
	}
	if m["schema_version"] != "2" || m["status"] != "ok" {
		t.Fatalf("status 출력=%v, want schema_version=2/ok", m)
	}
}

// TestStatusUnknownSchemaExit3 — 미지 버전(9)은 호환 검사 실패로 exit 3.
func TestStatusUnknownSchemaExit3(t *testing.T) {
	_, code := statusWithVersion(t, "9")
	if code != 3 {
		t.Fatalf("exit=%d, want 3(미지 버전 거부)", code)
	}
}
