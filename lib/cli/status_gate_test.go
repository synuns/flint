package cli

import (
	"strconv"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

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

func TestStatusSchemaV2OK(t *testing.T) {
	m, code := statusWithVersion(t, "2")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(v2 호환)", code)
	}
	if m["schema_version"] != "2" || m["status"] != "ok" {
		t.Fatalf("status 출력=%v, want schema_version=2/ok", m)
	}
}

func TestStatusSchemaV3OK(t *testing.T) {
	m, code := statusWithVersion(t, "3")
	if code != 0 {
		t.Fatalf("exit=%d, want 0(v3 호환)", code)
	}
	if m["schema_version"] != "3" || m["status"] != "ok" {
		t.Fatalf("status 출력=%v, want schema_version=3/ok", m)
	}
}

func TestStatusUnknownSchemaExit3(t *testing.T) {
	_, code := statusWithVersion(t, strconv.Itoa(db.LatestSchemaVersion+1))
	if code != 3 {
		t.Fatalf("exit=%d, want 3(미지 버전 거부)", code)
	}
}
