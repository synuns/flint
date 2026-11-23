package cli

import "testing"

func TestStatusSchemaV4OK(t *testing.T) {
	m, code := statusWithVersion(t, "4")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(v4 호환)", code)
	}
	if m["schema_version"] != "4" || m["status"] != "ok" {
		t.Fatalf("status 출력=%v, want schema_version=4/ok", m)
	}
}
