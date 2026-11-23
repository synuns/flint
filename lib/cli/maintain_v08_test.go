package cli

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func v08StaleRows() [][]any {
	return [][]any{
		{1, "미참조 노트", nil, ago(100 * 24 * time.Hour), 0},
		{2, "읽었지만 오래됨", ago(95 * 24 * time.Hour), ago(100 * 24 * time.Hour), 5},
	}
}

func shortRetryDelays(t *testing.T) {
	t.Helper()
	prev := maintainRetryDelays
	maintainRetryDelays = []time.Duration{0, 0}
	t.Cleanup(func() { maintainRetryDelays = prev })
}

func TestStaleListsReasonsSummary(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f, nil, v08StaleRows())
	out, code := runMaintainArgs(t, f, "--stale")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	m := mustJSON(t, out)
	items, _ := m["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items=%d건, want 2: %s", len(items), out)
	}
	first, _ := items[0].(map[string]any)
	if first["reason"] != "never_read" {
		t.Errorf("id=1 reason=%v, want never_read", first["reason"])
	}
	second, _ := items[1].(map[string]any)
	if second["reason"] != "stale" {
		t.Errorf("id=2 reason=%v, want stale", second["reason"])
	}
	if m["summary"] != "2 to invalidate" {
		t.Errorf("summary=%v, want %q", m["summary"], "2 to invalidate")
	}
}

func mustJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님: %q (%v)", out, err)
	}
	return m
}

func TestInvalidateStaleDryRunDefault(t *testing.T) {
	f := &fakeMaint{fakeStore: &fakeStore{}}
	staleQueryFixture(t, f, nil, v08StaleRows())
	out, code := runMaintainArgs(t, f, "--invalidate-stale")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if m := mustJSON(t, out); m["summary"] != "2 to invalidate" {
		t.Errorf("dry-run summary=%v", m["summary"])
	}
	for _, q := range f.queries {
		if strings.Contains(q, "UPDATE") {
			t.Errorf("dry-run에 UPDATE 발생: %q", q)
		}
	}
	if len(f.commits) != 0 {
		t.Errorf("dry-run에 Commit 발생: %v", f.commits)
	}
}

func TestInvalidateStaleYesApplies(t *testing.T) {
	shortRetryDelays(t)
	f := &fakeMaint{fakeStore: &fakeStore{}}
	f.invalSeqErrs = []error{errors.New("40001 serialization failure")}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return nil, nil
		}
		return v08StaleRows(), nil
	}
	out, code := runMaintainArgs(t, f, "--invalidate-stale", "--yes")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.invalidateCalls) != 2 {
		t.Errorf("Invalidate 시도=%d회, want 2(Retry 후 회복)", len(f.invalidateCalls))
	}
	if len(f.invalidateCalls[0]) != 2 || f.invalidateCalls[0][0] != 1 || f.invalidateCalls[0][1] != 2 {
		t.Errorf("Invalidate 파라미터=%v, want [[1 2]]", f.invalidateCalls)
	}
	if len(f.invalidateMsgs) != 2 || f.invalidateMsgs[0] != "invalidate-stale" {
		t.Errorf("Invalidate 사유=%v, want invalidate-stale", f.invalidateMsgs)
	}
	if len(f.commits) != 1 {
		t.Fatalf("Commit 호출=%d회, want 1: %v", len(f.commits), f.commits)
	}
	if !strings.Contains(f.commits[0], "invalidate-stale") {
		t.Errorf("커밋 메시지=%q", f.commits[0])
	}
	if m := mustJSON(t, out); m["count"] != float64(2) {
		t.Errorf("count=%v, want 2", m["count"])
	}
}

func TestInvalidateIdsSnapshot(t *testing.T) {
	shortRetryDelays(t)
	f := &fakeMaint{fakeStore: &fakeStore{}}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT id FROM knowledge") {
			return [][]any{{4}, {9}}, nil
		}
		return nil, errors.New("unexpected query: " + sql)
	}
	out, code := runMaintainArgs(t, f, "--invalidate", "--ids", "4,9", "--yes")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "last_read_at") {
			t.Errorf("stale 재평가 질의 발생(스냅샷 위반): %q", q)
		}
	}
	if len(f.invalidateCalls) != 1 ||
		!reflect.DeepEqual(f.invalidateCalls[0], []int{4, 9}) {
		t.Errorf("Invalidate 파라미터=%v, want [[4 9]]", f.invalidateCalls)
	}
	if len(f.commits) != 1 {
		t.Errorf("Commit 호출=%d회, want 1", len(f.commits))
	}

	f2 := &fakeMaint{fakeStore: &fakeStore{}}
	f2.queryFn = func(sql string, args []any) ([][]any, error) {
		return [][]any{{4}}, nil
	}
	out2, code2 := runMaintainArgs(t, f2, "--invalidate", "--ids", "4,9", "--yes")
	if code2 != 1 {
		t.Errorf("부재 exit=%d, want 1; out=%s", code2, out2)
	}
	if !strings.Contains(out2, `"error"`) || !strings.Contains(out2, "9") {
		t.Errorf("부재 안내 부재: %q", out2)
	}
	if len(f2.invalidateCalls) != 0 {
		t.Errorf("부재 시 Invalidate 발생: %v", f2.invalidateCalls)
	}
	if len(f2.commits) != 0 {
		t.Errorf("부재 시 Commit 발생: %v", f2.commits)
	}

	_, code3 := runMaintainArgs(t, &fakeMaint{fakeStore: &fakeStore{}},
		"--invalidate", "--ids", "4")
	if code3 != 1 {
		t.Errorf("--ids 단독 exit=%d, want 1", code3)
	}
}

func supersedeFixture(t *testing.T, f *fakeMaint, row []any) {
	t.Helper()
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT (SELECT status") {
			return [][]any{row}, nil
		}
		if strings.Contains(sql, "WITH RECURSIVE") {

			return [][]any{{false, false}}, nil
		}
		return nil, errors.New("unexpected query: " + sql)
	}
}

func TestSupersedeSetsFields(t *testing.T) {
	shortRetryDelays(t)
	f := &fakeMaint{fakeStore: &fakeStore{}}
	newAt := time.Now().Add(-24 * time.Hour)
	supersedeFixture(t, f, []any{"active", newAt})
	out, code := runMaintainArgs(t, f, "--supersede", "3", "--by", "7")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if len(f.supersedeCalls) != 1 {
		t.Fatalf("Supersede 호출=%d회, want 1", len(f.supersedeCalls))
	}
	if got := f.supersedeCalls[0]; got.oldID != 3 || got.newID != 7 || !got.at.Equal(newAt) {
		t.Errorf("Supersede 위임=%+v, want {3 7 %v}", got, newAt)
	}
	if len(f.commits) != 1 || !strings.Contains(f.commits[0], "supersede") {
		t.Errorf("Commit=%v", f.commits)
	}
	if !strings.Contains(out, `"status":"superseded"`) || !strings.Contains(out, `"superseded_by":7`) {
		t.Errorf("출력 미충족: %q", out)
	}
}

func TestSupersedeRejects(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		row  []any
		args []string
		want int
	}{
		{"NEW 부재", []any{"active", nil}, []string{"--supersede", "3", "--by", "99"}, 1},
		{"자기참조", []any{"active", now}, []string{"--supersede", "3", "--by", "3"}, 3},
		{"OLD 비active", []any{"invalidated", now}, []string{"--supersede", "3", "--by", "7"}, 1},
		{"OLD 부재", []any{nil, now}, []string{"--supersede", "3", "--by", "7"}, 1},
	}
	for _, c := range cases {
		f := &fakeMaint{fakeStore: &fakeStore{}}
		supersedeFixture(t, f, c.row)
		out, code := runMaintainArgs(t, f, c.args...)
		if code != c.want {
			t.Errorf("%s: exit=%d, want %d; out=%s", c.name, code, c.want, out)
		}
		if !strings.Contains(out, `"error"`) {
			t.Errorf("%s: 에러 JSON 아님: %q", c.name, out)
		}
		if len(f.commits) != 0 {
			t.Errorf("%s: Commit 발생: %v", c.name, f.commits)
		}
		if len(f.supersedeCalls) != 0 {
			t.Errorf("%s: Supersede 발생: %v", c.name, f.supersedeCalls)
		}
	}
}

func TestMaintainV08ModeFlagConflicts(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"stale에 --id", []string{"--stale", "--id", "5"}},
		{"invalidate-stale에 --id", []string{"--invalidate-stale", "--id", "5"}},
		{"invalidate-stale에 --reason", []string{"--invalidate-stale", "--reason", "x"}},
		{"supersede에 --days", []string{"--supersede", "3", "--by", "7", "--days", "5"}},
		{"check에 --yes", []string{"--check", "--yes"}},
		{"stale에 --yes", []string{"--stale", "--yes"}},
		{"merge에 --yes", []string{"--merge", "--from", "1", "--to", "2", "--yes"}},
		{"supersede에 --by 없음", []string{"--supersede", "3"}},
		{"check에 --ids", []string{"--check", "--ids", "1"}},
		{"ids 파싱 실패", []string{"--invalidate", "--ids", "1,x", "--yes"}},
	}
	for _, c := range cases {
		f := &fakeMaint{fakeStore: &fakeStore{}}
		out, code := runMaintainArgs(t, f, c.args...)
		if code != 1 {
			t.Errorf("%s: exit=%d, want 1; out=%s", c.name, code, out)
		}
		if !strings.Contains(out, `"error"`) {
			t.Errorf("%s: 에러 JSON 아님: %q", c.name, out)
		}
	}
}
