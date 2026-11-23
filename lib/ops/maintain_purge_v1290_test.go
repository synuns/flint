package ops

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func withPurgeDSNV1290(t *testing.T, dsn string) {
	t.Helper()
	old := PurgeDSNFn
	PurgeDSNFn = func() string { return dsn }
	t.Cleanup(func() { PurgeDSNFn = old })
}

func undropListErrV1290(names ...string) error {
	if len(names) == 0 {
		return errors.New("ERROR: no database name specified. there are no databases currently available to be undropped (SQLSTATE XX000)")
	}
	return errors.New("ERROR: no database name specified. available databases that can be undropped: " +
		strings.Join(names, ", ") + " (SQLSTATE XX000)")
}

func runPurgeArgsV1290(t *testing.T, f *fakeMaintStore, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := RunMaintain(context.Background(), f, args, nil, &out)
	return out.String(), code
}

func TestMaintainPurgeDroppedDryRunV1290(t *testing.T) {
	withPurgeDSNV1290(t, "postgresql://flint:flint-dev@localhost:5434/flint")
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, undropListErrV1290("flint_demo_a", "flint_demo_b")
	}}}
	out, code := runPurgeArgsV1290(t, f, "--purge-dropped")
	if code != 0 {
		t.Fatalf("exit=%d out=%s, want 0", code, out)
	}
	if !strings.Contains(out, `"status":"dry_run"`) || !strings.Contains(out, `"dropped":2`) {
		t.Fatalf("dry_run 응답 아님: %s", out)
	}
	if !strings.Contains(out, `"flint_demo_a"`) || !strings.Contains(out, `"flint_demo_b"`) {
		t.Fatalf("목록 미포함: %s", out)
	}
	if len(f.queries) != 1 || f.queries[0] != "SELECT dolt_undrop()" {
		t.Fatalf("조회 쿼리=%v, want dolt_undrop() 1건", f.queries)
	}
}

func TestMaintainPurgeDroppedDryRunEmptyV1290(t *testing.T) {
	withPurgeDSNV1290(t, "postgresql://flint:flint-dev@127.0.0.1:5499/flint")
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, undropListErrV1290()
	}}}
	out, code := runPurgeArgsV1290(t, f, "--purge-dropped")
	if code != 0 {
		t.Fatalf("exit=%d out=%s, want 0", code, out)
	}
	if !strings.Contains(out, `"dropped":0`) || !strings.Contains(out, `"databases":[]`) {
		t.Fatalf("빈 목록 응답 아님: %s", out)
	}
}

func TestMaintainPurgeDroppedYesV1290(t *testing.T) {
	withPurgeDSNV1290(t, "postgresql://flint:flint-dev@localhost:5434/flint")
	calls := []string{}
	f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		calls = append(calls, sql)
		if strings.Contains(sql, "dolt_undrop") {
			return nil, undropListErrV1290("flint_demo_a", "flint_demo_b")
		}
		return [][]any{{int32(0)}}, nil
	}}}
	out, code := runPurgeArgsV1290(t, f, "--purge-dropped", "--yes")
	if code != 0 {
		t.Fatalf("exit=%d out=%s, want 0", code, out)
	}
	if !strings.Contains(out, `"status":"purged"`) || !strings.Contains(out, `"dropped":2`) {
		t.Fatalf("purged 응답 아님: %s", out)
	}
	if len(calls) != 2 || calls[1] != "SELECT dolt_purge_dropped_databases()" {
		t.Fatalf("purge 쿼리=%v, want 2건(목록+purge)", calls)
	}
}

func TestMaintainPurgeDroppedNonLoopbackExit3V1290(t *testing.T) {
	withPurgeDSNV1290(t, "postgresql://flint:pw@db-host:5432/flint")
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	out, code := runPurgeArgsV1290(t, f, "--purge-dropped")
	if code != 3 {
		t.Fatalf("exit=%d out=%s, want 3", code, out)
	}
	if !strings.Contains(out, "loopback") {
		t.Fatalf("루프백 안내 부재: %s", out)
	}
	if len(f.queries) != 0 {
		t.Fatalf("비루프백에서 쿼리 실행됨: %v", f.queries)
	}
}

func TestMaintainPurgeDroppedUnsupportedYesForbiddenV1290(t *testing.T) {
	withPurgeDSNV1290(t, "postgresql://flint:flint-dev@localhost:5434/flint")
	for _, args := range [][]string{{"--purge-dropped"}, {"--purge-dropped", "--yes"}} {
		f := &fakeMaintStore{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			return nil, errors.New("function: 'dolt_undrop' not found")
		}}}
		out, code := runPurgeArgsV1290(t, f, args...)
		if code != 3 {
			t.Fatalf("%v exit=%d out=%s, want 3", args, code, out)
		}
		if !strings.Contains(out, "unsupported") {
			t.Fatalf("미지원 안내 부재: %s", out)
		}
		for _, q := range f.queries {
			if strings.Contains(q, "dolt_purge_dropped_databases") {
				t.Fatalf("미지원 엔진에 purge 실행됨: %v", f.queries)
			}
		}
	}
}

func TestMaintainPurgeDroppedFlagGuardsV1290(t *testing.T) {
	withPurgeDSNV1290(t, "postgresql://flint:flint-dev@localhost:5434/flint")
	f := &fakeMaintStore{fakeStore: &fakeStore{}}
	out, code := runPurgeArgsV1290(t, f, "--purge-dropped", "--limit", "5")
	if code != 1 {
		t.Fatalf("exit=%d out=%s, want 1", code, out)
	}
	if !strings.Contains(out, "not valid with --purge-dropped") {
		t.Fatalf("플래그 거부 안내 부재: %s", out)
	}
}

func TestRunPurgeDroppedDirectV1290(t *testing.T) {
	withPurgeDSNV1290(t, "postgresql://flint:flint-dev@localhost:5434/flint")
	calls := []string{}
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		calls = append(calls, sql)
		if strings.Contains(sql, "dolt_undrop") {
			return nil, undropListErrV1290("flint_demo_c")
		}
		return [][]any{{int32(0)}}, nil
	}}
	var out strings.Builder
	if code := RunPurgeDropped(context.Background(), f, true, &out); code != 0 {
		t.Fatalf("exit=%d out=%s, want 0", code, out.String())
	}
	if !strings.Contains(out.String(), `"status":"purged"`) || !strings.Contains(out.String(), `"dropped":1`) {
		t.Fatalf("purged 응답 아님: %s", out.String())
	}
	if len(calls) != 2 {
		t.Fatalf("쿼리 %v, want 목록+purge 2건", calls)
	}
}
