// maintain_v0330_test.go — v0.33.0 지식 위생: meta-audit 분류·prune dry-run/commit.
package cli

import (
	"context"
	"strings"
	"testing"
)

// TestMaintainMetaAuditClassification — R1: known/threshold/daily/unknown 분류.
func TestMaintainMetaAuditClassification(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key = 'pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "FROM meta ORDER BY key") {
			return [][]any{
				{"schema_version", "8"},
				{"daily:2026-11-09", "{}"},
				{"read_stats", "{}"}, // known(레거시 관측)
				{"mystery_key", "x"}, // unknown
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
	for _, want := range []string{`"known":2`, `"daily":1`, `"unknown":1`, `"mystery_key"`} {
		if !strings.Contains(s, want) {
			t.Errorf("분류 누락 %q: %s", want, s)
		}
	}
}

// TestMaintainPruneInvalidatedDryRunCommit — R2: dry-run 기본·--commit 확정.
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
	// --commit: DELETE 실행·커밋.
	out.Reset()
	if code := runMaintain(context.Background(), f, []string{"--prune-invalidated", "30", "--commit"}, nil, &out); code != 0 {
		t.Fatalf("commit exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"deleted":2`) {
		t.Errorf("commit 출력: %s", out.String())
	}
	deleted := false
	for _, q := range f.queries {
		if strings.Contains(q, "DELETE FROM knowledge") {
			deleted = true
		}
	}
	if !deleted {
		t.Error("DELETE 미실행")
	}
	// N 부재 → exit 3.
	out.Reset()
	if code := runMaintain(context.Background(), f, []string{"--prune-invalidated"}, nil, &out); code != 1 {
		t.Errorf("N 부재 exit=%d, want 1(사용법 오류): %s", code, out.String())
	}
}
