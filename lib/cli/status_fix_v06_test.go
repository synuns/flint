// status_fix_v06_test.go — T70 회귀: v0.6 신규 DB(EnsureSchemaV4 승격 →
// meta.schema_version=4)에서 `flint status`가 exit 3으로 거부되던 결함.
// 게이트는 v4도 정상 상태로 승인해야 한다(인시던트: lib/cli/status.go:61).
package cli

import "testing"

// TestStatusSchemaV4OK — v4 DB(계측 컬럼 승격 완료)에서 status는 exit 0·
// 버전 그대로 보고. 수정 전 게이트가 {"1","2","3"} 하드코드라 exit 3.
func TestStatusSchemaV4OK(t *testing.T) {
	m, code := statusWithVersion(t, "4")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(v4 호환)", code)
	}
	if m["schema_version"] != "4" || m["status"] != "ok" {
		t.Fatalf("status 출력=%v, want schema_version=4/ok", m)
	}
}
