package cli

import (
	"context"
	"testing"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/query"
)

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
