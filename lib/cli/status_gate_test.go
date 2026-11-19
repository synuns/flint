// status_gate_test.go — the schema_version compatibility gate (ruling: {"1","2"} allowed).
// Fast verification that status keeps exit 0 after the v2 promotion (newRealStore -> EnsureSchemaV2)
// and still rejects unknown versions with exit 3 (the backend regression of e2e case 4).
package cli

import (
	"strconv"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

// statusWithVersion — runs status against a fake store whose meta.schema_version is version.
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

// TestStatusSchemaV2OK — on a v2 DB (promotion done), status exits 0 and reports the version as-is.
func TestStatusSchemaV2OK(t *testing.T) {
	m, code := statusWithVersion(t, "2")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(v2 호환)", code)
	}
	if m["schema_version"] != "2" || m["status"] != "ok" {
		t.Fatalf("status 출력=%v, want schema_version=2/ok", m)
	}
}

// TestStatusSchemaV3OK — on a v3 DB (EnsureSchemaV3 index promotion), status
// exits 0 and reports the version as-is (a lossless promotion, so not a compatibility-rejection target).
func TestStatusSchemaV3OK(t *testing.T) {
	m, code := statusWithVersion(t, "3")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(v3 호환)", code)
	}
	if m["schema_version"] != "3" || m["status"] != "ok" {
		t.Fatalf("status 출력=%v, want schema_version=3/ok", m)
	}
}

// TestStatusUnknownSchemaExit3 — an unknown version (latest+1) exits 3 on the compatibility check.
// The allowed range derives from db.LatestSchemaVersion — so does "unknown"
// (the past literal "9" became a valid version after the v9 promotion, cementing a misjudging gate).
func TestStatusUnknownSchemaExit3(t *testing.T) {
	_, code := statusWithVersion(t, strconv.Itoa(db.LatestSchemaVersion+1))
	if code != 3 {
		t.Fatalf("exit=%d, want 3(미지 버전 거부)", code)
	}
}
