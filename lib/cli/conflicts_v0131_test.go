// conflicts_v0131_test.go — v0.13.1 심층 리뷰 회귀(F6·F7·F9):
// F6 conflictDice가 lib/query.BigramDiceMultiset로 위임(로컬 재구현 폐지),
// F7/F9 checkConflicts가 ConflictStore.ConflictGroups seam 경유(인라인
// GROUP BY SQL 폐지·상한 있는 조회). 기존 테스트 함수는 불변.
package cli

import (
	"context"
	"testing"

	"flint/lib/db"
	"flint/lib/query"
)

// fakeStore.ConflictGroups — ConflictStore seam fake(F7). 실 Repo의 그룹
// 조회를 Query 경로로 흉내 낸다. queryFn·rows는 기존 checkConflicts
// 인라인 SQL과 같은 행 모양({topic, []string summaries})을 그대로 쓴다 —
// 형태가 다른 행은 분류 불가로 건너뛴다.
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

// TestConflictDiceDelegatesToQuery — F6 회귀: conflictDice는 lib/query
// BigramDiceMultiset 위임. "아아아아"/"아아"는 집합판(1)과 다중집합판(0.5)이
// 갈리는 지점 — 기존 분류 측정(다중집합)이 집합판으로 바뀌지 않았음을
// 같이 고정한다.
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

// seamConflictStore — ConflictGroups seam만 제공하는 검사용 store.
// 내포 fakeStore의 queryFn은 호출 자체를 실패로 간주한다 — checkConflicts가
// SQL을 직접 굴리는 옛 동작(F7)이 돌아오면 잡힌다.
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

// TestConflictsUsesConflictGroupsSeam — F7/F9 회귀: checkConflicts의 그룹
// 조회는 ConflictStore.ConflictGroups seam만 경유하고(직접 SQL 조회 금지),
// 상한은 doctor 전용 doctorConflictLimit=50이다(v0.13.2 N8 갱신 — 상위
// 10그룹만 세면 초과 모순이 미보고로 누락).
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
