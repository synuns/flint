// status_fix_v06_test.go — regression: on a v0.6 fresh DB (EnsureSchemaV4 promotion ->
// meta.schema_version=4), `flint status` was rejected with exit 3.
// The gate must also accept v4 as a normal state (incident: lib/cli/status.go:61).
package cli

import "testing"

// TestStatusSchemaV4OK — on a v4 DB (metering-column promotion done), status exits 0
// and reports the version as-is. Before the fix the gate hardcoded {"1","2","3"}, hence exit 3.
func TestStatusSchemaV4OK(t *testing.T) {
	m, code := statusWithVersion(t, "4")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(v4 호환)", code)
	}
	if m["schema_version"] != "4" || m["status"] != "ok" {
		t.Fatalf("status 출력=%v, want schema_version=4/ok", m)
	}
}
