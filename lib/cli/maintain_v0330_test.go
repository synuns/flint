package cli

import (
	"context"
	"strings"
	"testing"
)

func TestMaintainMetaAuditClassification(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key = 'pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM meta ORDER BY key") {
			return [][]any{
				{"schema_version", "8"},
				{"daily:2026-11-09", "{}"},
				{"read_stats", "{}"},
				{"last_brief_at", "x"},
				{"daily:write:2026-11-09", "1"},
				{"mystery_key", "x"},
			}, nil
		}
		return nil, nil
	}}}
	var out strings.Builder
	code := runMaintain(context.Background(), f, []string{"--meta-audit"}, nil, &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	s := out.String()
	for _, want := range []string{`"known":3`, `"daily":2`, `"unknown":1`, `"mystery_key"`} {
		if !strings.Contains(s, want) {
			t.Errorf("분류 누락 %q: %s", want, s)
		}
	}
}

func TestMaintainPruneInvalidatedDryRunCommit(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "status = 'invalidated'") && strings.Contains(sql, "SELECT") {
			return [][]any{{int32(3)}, {int32(7)}}, nil
		}
		return nil, nil
	}}}
	var out strings.Builder
	if code := runMaintain(context.Background(), f, []string{"--prune-invalidated", "30"}, nil, &out); code != 0 {
		t.Fatalf("dry-run exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"would_delete":2`) || !strings.Contains(out.String(), `"status":"dry-run"`) {
		t.Errorf("dry-run 출력: %s", out.String())
	}
	if len(f.pruneCalls) != 0 {
		t.Error("dry-run에서 DELETE 위임 실행")
	}

	out.Reset()
	if code := runMaintain(context.Background(), f, []string{"--prune-invalidated", "30", "--commit"}, nil, &out); code != 0 {
		t.Fatalf("commit exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"deleted":2`) {
		t.Errorf("commit 출력: %s", out.String())
	}
	if len(f.pruneCalls) != 1 {
		t.Fatalf("DELETE 위임 실행 %d회, want 1", len(f.pruneCalls))
	}

	out.Reset()
	if code := runMaintain(context.Background(), f, []string{"--prune-invalidated"}, nil, &out); code != 1 {
		t.Errorf("N 부재 exit=%d, want 1(사용법 오류): %s", code, out.String())
	}
}
