package knowledge

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

type survivedKreadsRow struct {
	id      int
	session string
	kind    string
}

func survivedFakeEngine(rows []survivedKreadsRow) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		if !strings.Contains(sql, "knowledge_reads") {
			return nil, fmt.Errorf("survived fake: unexpected sql %q", sql)
		}
		wanted := map[int]bool{}
		if ids, ok := args[0].([]int); ok {
			for _, id := range ids {
				wanted[id] = true
			}
		}
		perSession := map[int]map[string]int{}
		for _, r := range rows {
			if !wanted[r.id] {
				continue
			}
			if perSession[r.id] == nil {
				perSession[r.id] = map[string]int{}
			}
			perSession[r.id][r.session]++
		}
		var out [][]any
		for id, sessions := range perSession {
			n := 0
			for _, c := range sessions {
				if c >= 2 {
					n++
				}
			}
			if n > 0 {
				out = append(out, []any{int32(id), int64(n)})
			}
		}
		return out, nil
	}
}

type survivedPoolStore struct {
	pool *pgxpool.Pool
	wire.HistoryStubs
}

func (s *survivedPoolStore) Migrate(ctx context.Context) error { return nil }
func (s *survivedPoolStore) InsertBatch(ctx context.Context, r []map[string]any) ([]int, error) {
	return nil, nil
}
func (s *survivedPoolStore) ExistsHash(ctx context.Context, h string) (bool, error) {
	return false, nil
}
func (s *survivedPoolStore) ExistsProject(ctx context.Context, n string) (bool, error) {
	return false, nil
}
func (s *survivedPoolStore) Commit(ctx context.Context, m string) (string, error) {
	return "", nil
}
func (s *survivedPoolStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *survivedPoolStore) Close()                         { s.pool.Close() }
func (s *survivedPoolStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

func TestSurvivedCountsScenarios(t *testing.T) {
	ctx := context.Background()
	prefix := fmt.Sprintf("surv-%d-", time.Now().UnixNano())
	kreads := []survivedKreadsRow{
		{1, prefix + "s1", "read"}, {1, prefix + "s1", "hot"},
		{2, prefix + "s2", "read"}, {2, prefix + "s2", "hot"},
		{2, prefix + "s3", "read"}, {2, prefix + "s3", "read"},
		{3, prefix + "s4", "read"},
	}
	ids := []int{1, 2, 3, 4}

	var st wire.Store
	if dsn, real := seamRealDSN(); real {
		pool, err := db.Connect(ctx, dsn)
		if err != nil {
			t.Skipf("real db unavailable: %v", err)
		}
		for _, r := range kreads {
			if _, err := pool.Exec(ctx,
				`INSERT INTO knowledge_reads (knowledge_id, session, kind) VALUES ($1, $2, $3)`,
				r.id, r.session, r.kind); err != nil {
				t.Skipf("real seeding unavailable: %v", err)
			}
		}

		t.Cleanup(pool.Close)
		t.Cleanup(func() {
			if _, err := pool.Exec(ctx,
				`DELETE FROM knowledge_reads WHERE session LIKE $1`, prefix+"%"); err != nil {
				t.Errorf("knowledge_reads 시딩 정리 실패: %v", err)
			}
		})
		st = &survivedPoolStore{pool: pool}
	} else {
		st = &fakeStore{queryFn: survivedFakeEngine(kreads)}
	}

	got, err := SurvivedCounts(ctx, st, ids)
	if err != nil {
		t.Fatalf("SurvivedCounts 에러: %v", err)
	}
	if got[1] != 1 {
		t.Errorf("동일 세션 2회 = %d, want 1", got[1])
	}
	if got[2] != 2 {
		t.Errorf("상이 세션 2개 = %d, want 2", got[2])
	}
	if v, ok := got[3]; ok {
		t.Errorf("1회 회상 존재 = %d, want 부재", v)
	}
	if v, ok := got[4]; ok {
		t.Errorf("회상 없음 존재 = %d, want 부재", v)
	}
	if _, ok := got[1]; !ok {
		t.Errorf("survived id 1 부재 — map %v", got)
	}
}

func TestSurvivedCountsEmptyIDsNoop(t *testing.T) {
	st := &fakeStore{}
	got, err := SurvivedCounts(context.Background(), st, nil)
	if err != nil {
		t.Fatalf("에러: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("빈 ids 결과 = %v, want 빈 map", got)
	}
	if len(st.queries) != 0 {
		t.Errorf("빈 ids 쿼리 %d건, want 0", len(st.queries))
	}
}

func TestSurvivedCountsSQLContract(t *testing.T) {
	ids := []int{11, 12}
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		for _, want := range []string{"knowledge_reads", "GROUP BY knowledge_id, session", "HAVING count(*) >= 2"} {
			if !strings.Contains(sql, want) {
				t.Errorf("sql에 %q 부재: %q", want, sql)
			}
		}
		if got, ok := args[0].([]int); !ok || len(got) != 2 {
			t.Errorf("args[0] = %v, want ids []int", args[0])
		}
		return [][]any{{int32(11), int64(3)}}, nil
	}}
	got, err := SurvivedCounts(context.Background(), st, ids)
	if err != nil {
		t.Fatalf("에러: %v", err)
	}
	if got[11] != 3 {
		t.Errorf("map[11] = %d, want 3", got[11])
	}
}

func TestSurvivedCountsQueryError(t *testing.T) {
	st := &fakeStore{queryErr: fmt.Errorf("db down")}
	if _, err := SurvivedCounts(context.Background(), st, []int{1}); err == nil {
		t.Errorf("쿼리 실패 에러 전파 없음")
	}
}

func TestSnapshotSurvivedSessions(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")

	t.Run("survived≥1 id 수 스냅숏", func(t *testing.T) {
		kreads := []survivedKreadsRow{
			{1, "sn-s1", "read"}, {1, "sn-s1", "hot"},
			{2, "sn-s2", "read"}, {2, "sn-s2", "read"},
			{3, "sn-s3", "read"},
		}
		cap := &counterCaptureV{}
		st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "knowledge_reads") {
				return survivedFakeEngine(kreads)(sql, args)
			}
			cap.queries = append(cap.queries, capturedQueryV{sql, args})
			return nil, nil
		}}
		counts, err := SurvivedCounts(context.Background(), st, []int{1, 2, 3})
		if err != nil {
			t.Fatalf("SurvivedCounts 에러: %v", err)
		}
		SnapshotSurvivedSessions(context.Background(), st, counts)
		if got := cap.sum("daily:survived_sessions:" + today); got != 2 {
			t.Errorf("survived_sessions 증분=%d, want 2(id 1·2)", got)
		}
	})

	t.Run("전원 0이면 미발화", func(t *testing.T) {
		cap := &counterCaptureV{}
		st := &fakeStore{queryFn: cap.fn}
		SnapshotSurvivedSessions(context.Background(), st, map[int]int{1: 0, 2: 0})
		if got := cap.sum("daily:survived_sessions:"); got != 0 {
			t.Errorf("0 인원 증분=%d, want 0", got)
		}
		if len(cap.queries) != 0 {
			t.Errorf("n<=0인데 쿼리 %d건, want 0", len(cap.queries))
		}
	})

	t.Run("빈 map 미발화", func(t *testing.T) {
		cap := &counterCaptureV{}
		st := &fakeStore{queryFn: cap.fn}
		SnapshotSurvivedSessions(context.Background(), st, nil)
		if len(cap.queries) != 0 {
			t.Errorf("빈 map 쿼리 %d건, want 0", len(cap.queries))
		}
	})
}
