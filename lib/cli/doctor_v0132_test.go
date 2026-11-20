// doctor_v0132_test.go — --check conflicts
// standalone execution (generalizing the tail-chain subset) and the query-cap-reached wording.
package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/db"
)

// TestDoctorCheckConflictsSubset — N7: `doctor --check conflicts` is not an unknown-check
// exit 3 but runs just the 1 check (conflicts) and exits 0.
// fakeStore provides the ConflictGroups seam (conflicts_v0131_test.go), so with
// 0 groups it confirms the standalone-execution contract via the ok path.
func TestDoctorCheckConflictsSubset(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil // 0 contradiction-candidate groups
	}}
	var out bytes.Buffer
	code := runDoctor(context.Background(), st, []string{"--check", "conflicts"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d out=%s, want 0", code, out.String())
	}
	if !strings.Contains(out.String(), `"name":"conflicts"`) {
		t.Errorf("출력에 conflicts 검사 없음: %s", out.String())
	}
}

// TestConflictsLimitReachedNotesOverflow — N8: when the group count reaches the doctor cap
// (doctorConflictLimit), the detail carries a possible-overflow note.
func TestConflictsLimitReachedNotesOverflow(t *testing.T) {
	groups := make([]db.ConflictGroup, doctorConflictLimit)
	for i := range groups {
		groups[i] = db.ConflictGroup{
			Topic: fmt.Sprintf("topic-%d", i),
			IDs:   []int{2*i + 1, 2*i + 2},
			// the summaries are completely different, so isConflictGroup is true — 50 contradiction-candidate groups.
			Summaries: []string{"포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다"},
		}
	}
	inner := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}
	s := &seamConflictStore{fakeStore: inner, groups: groups}
	c := checkConflicts(context.Background(), s)
	if c.Status != "warn" {
		t.Fatalf("status = %s, want warn", c.Status)
	}
	if !strings.Contains(c.Detail, "group query limit") {
		t.Errorf("상한 도달 문구 없음: %q", c.Detail)
	}
	if len(s.calls) != 1 || s.calls[0] != doctorConflictLimit {
		t.Errorf("조회 limit = %v, want [%d]", s.calls, doctorConflictLimit)
	}
}
