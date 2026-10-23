// maintain_v08_test.go — T87 수명주기 액션 회귀(spec §2):
// --stale 사유·요약 강화 · --invalidate-stale dry-run/--yes · --invalidate
// --ids 스냅샷 · --supersede. 전부 fake Store(UPDATE 문·파라미터 단언,
// 컬럼 스키마 무관 — T82 병행).
package cli

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// v08StaleRows — stale 조회 픽스처: 미참조 1건(never_read)·경과 1건(stale).
func v08StaleRows() [][]any {
	return [][]any{
		{1, "미참조 노트", nil, ago(100 * 24 * time.Hour), 0},
		{2, "읽었지만 오래됨", ago(95 * 24 * time.Hour), ago(100 * 24 * time.Hour), 5},
	}
}

// shortRetryDelays — Retry 재시도를 즉시 실행(테스트 지연 제거).
func shortRetryDelays(t *testing.T) {
	t.Helper()
	prev := maintainRetryDelays
	maintainRetryDelays = []time.Duration{0, 0}
	t.Cleanup(func() { maintainRetryDelays = prev })
}

// TestStaleListsReasonsSummary — Review Focus: 각 항목에 사유
// stale|never_read, 요약 "N to invalidate". 기존 --days·--limit 불변.
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

// mustJSON — 한 줄 출력 파싱 헬퍼.
func mustJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("JSON 아님: %q (%v)", out, err)
	}
	return m
}

// TestInvalidateStaleDryRunDefault — 마커(--yes) 없으면 갱신 없이 --stale과
// 동일 출력·exit 0(dry-run 기본).
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

// TestInvalidateStaleYesApplies — Review Focus: --yes 시 대상 전체 UPDATE
// (status·invalid_at=now)+Commit, 쓰기 경로 Retry(40001 1회 후 회복).
func TestInvalidateStaleYesApplies(t *testing.T) {
	shortRetryDelays(t)
	updateCalls := 0
	var updateSQL string
	var updateArgs []any
	f := &fakeMaint{fakeStore: &fakeStore{}}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta") {
			return nil, nil
		}
		if strings.HasPrefix(strings.TrimSpace(sql), "UPDATE") {
			updateCalls++
			updateSQL, updateArgs = sql, args
			if updateCalls == 1 {
				return nil, errors.New("40001 serialization failure")
			}
			return nil, nil
		}
		return v08StaleRows(), nil
	}
	out, code := runMaintainArgs(t, f, "--invalidate-stale", "--yes")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if updateCalls != 2 {
		t.Errorf("UPDATE 시도=%d회, want 2(Retry 후 회복)", updateCalls)
	}
	if !strings.Contains(updateSQL, "status = 'invalidated'") ||
		!strings.Contains(updateSQL, "invalid_at = now()") {
		t.Errorf("UPDATE 문 미충족: %q", updateSQL)
	}
	if len(updateArgs) != 1 || !reflect.DeepEqual(updateArgs[0], []int{1, 2}) {
		t.Errorf("UPDATE 파라미터=%v, want [[1 2]]", updateArgs)
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

// TestInvalidateIdsSnapshot — Review Focus: --ids는 dry-run 재평가 무관
// 지정 id 고정(stale 질의 없음). 부재 id → exit 1. --yes 병행 필수.
func TestInvalidateIdsSnapshot(t *testing.T) {
	shortRetryDelays(t)
	var updateArgs []any
	var updateSQL string
	f := &fakeMaint{fakeStore: &fakeStore{}}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		s := strings.TrimSpace(sql)
		if strings.HasPrefix(s, "UPDATE") {
			updateSQL, updateArgs = sql, args
			return nil, nil
		}
		if strings.Contains(s, "SELECT id FROM knowledge") {
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
	if !strings.Contains(updateSQL, "status = 'invalidated'") {
		t.Errorf("UPDATE 문 미충족: %q", updateSQL)
	}
	if len(updateArgs) != 1 || !reflect.DeepEqual(updateArgs[0], []int{4, 9}) {
		t.Errorf("UPDATE 파라미터=%v, want [[4 9]]", updateArgs)
	}
	if len(f.commits) != 1 {
		t.Errorf("Commit 호출=%d회, want 1", len(f.commits))
	}

	// 부재 id — 존재 확인 1건만 → exit 1, UPDATE·Commit 없음.
	f2 := &fakeMaint{fakeStore: &fakeStore{}}
	f2.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.HasPrefix(strings.TrimSpace(sql), "UPDATE") {
			t.Errorf("부재 시 UPDATE 발생: %q", sql)
		}
		return [][]any{{4}}, nil
	}
	out2, code2 := runMaintainArgs(t, f2, "--invalidate", "--ids", "4,9", "--yes")
	if code2 != 1 {
		t.Errorf("부재 exit=%d, want 1; out=%s", code2, out2)
	}
	if !strings.Contains(out2, `"error"`) || !strings.Contains(out2, "9") {
		t.Errorf("부재 안내 부재: %q", out2)
	}
	if len(f2.commits) != 0 {
		t.Errorf("부재 시 Commit 발생: %v", f2.commits)
	}

	// --yes 없는 --ids — 실행 확정 미수반 → exit 1.
	_, code3 := runMaintainArgs(t, &fakeMaint{fakeStore: &fakeStore{}},
		"--invalidate", "--ids", "4")
	if code3 != 1 {
		t.Errorf("--ids 단독 exit=%d, want 1", code3)
	}
}

// supersedeFixture — OLD 상태·NEW valid_at 조회 응답.
func supersedeFixture(t *testing.T, f *fakeMaint, row []any) (updateSQL *string, updateArgs *[]any) {
	t.Helper()
	sqlp, argp := new(string), new([]any)
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "SELECT (SELECT status") {
			return [][]any{row}, nil
		}
		if strings.HasPrefix(strings.TrimSpace(sql), "UPDATE") {
			*sqlp, *argp = sql, args
			return nil, nil
		}
		return nil, errors.New("unexpected query: " + sql)
	}
	return sqlp, argp
}

// TestSupersedeSetsFields — Review Focus: OLD active일 때만
// status·superseded_by=NEW·invalid_at=NEW valid_at 서브쿼리 + Commit.
func TestSupersedeSetsFields(t *testing.T) {
	shortRetryDelays(t)
	f := &fakeMaint{fakeStore: &fakeStore{}}
	updateSQL, updateArgs := supersedeFixture(t, f,
		[]any{"active", time.Now().Add(-24 * time.Hour)})
	out, code := runMaintainArgs(t, f, "--supersede", "3", "--by", "7")
	if code != 0 {
		t.Fatalf("exit=%d, want 0; out=%s", code, out)
	}
	if !strings.Contains(*updateSQL, "status = 'invalidated'") ||
		!strings.Contains(*updateSQL, "superseded_by = $2") ||
		!strings.Contains(*updateSQL, "invalid_at = (SELECT valid_at FROM knowledge WHERE id = $2)") {
		t.Errorf("UPDATE 문 미충족: %q", *updateSQL)
	}
	if !reflect.DeepEqual(*updateArgs, []any{3, 7}) {
		t.Errorf("UPDATE 파라미터=%v, want [3 7]", *updateArgs)
	}
	if len(f.commits) != 1 || !strings.Contains(f.commits[0], "supersede") {
		t.Errorf("Commit=%v", f.commits)
	}
	if !strings.Contains(out, `"status":"superseded"`) || !strings.Contains(out, `"superseded_by":7`) {
		t.Errorf("출력 미충족: %q", out)
	}
}

// TestSupersedeRejects — Review Focus: NEW 부재 exit 1 · 자기참조 exit 3 ·
// OLD 비active(부재 포함) exit 1. 각각 UPDATE·Commit 없음.
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
		for _, q := range f.queries {
			if strings.HasPrefix(strings.TrimSpace(q), "UPDATE") {
				t.Errorf("%s: UPDATE 발생: %q", c.name, q)
			}
		}
	}
}

// TestMaintainV08ModeFlagConflicts — 기존 모드 귀속 검증 패턴 준수:
// 무관 값 플래그·--yes 남용은 조용한 무시가 아니라 에러.
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
