// conflicts_v0131_test.go
// conflictDice delegates to lib/query.BigramDiceMultiset (local reimplementation abolished),
// and checkConflicts goes through the ConflictStore.ConflictGroups seam (inline
// GROUP BY SQL abolished; bounded lookup).
package cli

import (
	"context"
	"testing"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/query"
)

// fakeStore.ConflictGroups — a ConflictStore seam fake (F7). It mimics the real Repo's group
// lookup via the Query path. queryFn and rows use the same row shape as the old checkConflicts
// inline SQL ({topic, []string summaries}) —
// differently shaped rows are skipped as unclassifiable.
func (f *fakeStore) ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error) {
	rows, err := f.Query(ctx, `SELECT topic, array_agg(summary ORDER BY id) FROM knowledge
		WHERE invalid_at IS NULL AND status = 'active'
		GROUP BY topic HAVING count(*) > 1 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	groups := make([]db.ConflictGroup, 0, len(rows))
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		topic, _ := r[0].(string)
		sums, ok := r[1].([]string)
		if !ok {
			continue
		}
		groups = append(groups, db.ConflictGroup{Topic: topic, Summaries: sums})
	}
	return groups, nil
}

// TestConflictDiceDelegatesToQuery — F6 regression: conflictDice delegates to lib/query
// BigramDiceMultiset. "아아아아"/"아아" is the point where the set version (1) and the multiset
// version (0.5) diverge — also pinning that the existing classification measure (multiset) has not
// changed to the set version.
func TestConflictDiceDelegatesToQuery(t *testing.T) {
	pairs := [][2]string{
		{"flint 백업은 매일 자동이다", "flint 백업은 매일 자동이다"},
		{"포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다"},
		{"아아아아", "아아"},
	}
	for _, p := range pairs {
		if got, want := conflictDice(p[0], p[1]), query.BigramDiceMultiset(p[0], p[1]); got != want {
			t.Errorf("conflictDice(%q, %q) = %v, want %v(query.BigramDiceMultiset)", p[0], p[1], got, want)
		}
	}
	if got := conflictDice("아아아아", "아아"); got != 0.5 {
		t.Errorf("반복 bigram 다중집합 Dice = %v, want 0.5(집합판 1과 구분)", got)
	}
}

// seamConflictStore — an inspection store providing only the ConflictGroups seam.
// The embedded fakeStore's queryFn treats any call as a failure — so if checkConflicts ever
// returns to the old behavior of running SQL directly (F7), it gets caught.
type seamConflictStore struct {
	*fakeStore
	groups []db.ConflictGroup
	err    error
	calls  []int
}

func (s *seamConflictStore) ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error) {
	s.calls = append(s.calls, limit)
	return s.groups, s.err
}

// TestConflictsUsesConflictGroupsSeam — F7/F9 regression: checkConflicts's group
// lookup goes only through the ConflictStore.ConflictGroups seam (no direct SQL),
// and the cap is the doctor-only doctorConflictLimit=50 (counting only the top
// 10 groups would miss the excess contradiction as unreported).
func TestConflictsUsesConflictGroupsSeam(t *testing.T) {
	inner := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		t.Errorf("checkConflicts가 인라인 SQL 조회 사용: %q", sql)
		return nil, nil
	}}
	s := &seamConflictStore{fakeStore: inner, groups: []db.ConflictGroup{
		{Topic: "port-config", IDs: []int{3, 4},
			Summaries: []string{"포트 기본값은 5433이다", "환경설정 초기화는 setup이 담당한다"}}}}
	c := checkConflicts(context.Background(), s)
	if c.Status != "warn" {
		t.Errorf("모순 1그룹 = %s, want warn", c.Status)
	}
	if len(s.calls) != 1 || s.calls[0] != doctorConflictLimit {
		t.Errorf("ConflictGroups 호출 limit = %v, want [%d](doctor 상한)", s.calls, doctorConflictLimit)
	}
	if doctorConflictLimit != 50 {
		t.Errorf("doctorConflictLimit = %d, want 50(N8)", doctorConflictLimit)
	}
	if suspectsDefaultLimit != 10 {
		t.Errorf("suspectsDefaultLimit = %d, want 10(스펙 R4 기본)", suspectsDefaultLimit)
	}
}
