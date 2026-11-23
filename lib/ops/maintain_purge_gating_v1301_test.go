package ops

import (
	"strings"
	"testing"
)

func withPurgeEngineModeV1301(t *testing.T, mode string) {
	t.Helper()
	old := PurgeEngineModeFn
	PurgeEngineModeFn = func() string { return mode }
	t.Cleanup(func() { PurgeEngineModeFn = old })
}

func TestMaintainPurgeDroppedExternalModeRefusedV1301(t *testing.T) {
	withPurgeDSNV1290(t, "postgresql://flint:flint-dev@localhost:5434/flint")
	withPurgeEngineModeV1301(t, "external")
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, undropListErrV1290("flint_demo_a")
	}}}
	out, code := runPurgeArgsV1290(t, f, "--purge-dropped", "--yes")
	if code != 3 {
		t.Fatalf("exit=%d out=%s, want 3", code, out)
	}
	if !strings.Contains(out, "managed-mode engine only") {
		t.Fatalf("관리형 전용 안내 부재: %s", out)
	}
	if len(f.queries) != 0 {
		t.Fatalf("external 모드에서 쿼리 실행됨: %v", f.queries)
	}
}

func TestMaintainPurgeDroppedManagedModeAllowedV1301(t *testing.T) {
	withPurgeDSNV1290(t, "postgresql://flint:flint-dev@localhost:5434/flint")
	for _, mode := range []string{"", "managed"} {
		withPurgeEngineModeV1301(t, mode)
		f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			return nil, undropListErrV1290("flint_demo_a")
		}}}
		out, code := runPurgeArgsV1290(t, f, "--purge-dropped")
		if code != 0 {
			t.Fatalf("mode=%q exit=%d out=%s, want 0", mode, code, out)
		}
		if !strings.Contains(out, `"status":"dry_run"`) {
			t.Fatalf("mode=%q dry_run 응답 아님: %s", mode, out)
		}
		if len(f.queries) != 1 {
			t.Fatalf("mode=%q 조회 쿼리=%v, want 1건", mode, f.queries)
		}
	}
}
