// survived_test.go — the survived signal contract (v1.14.0 T3): one
// survived point per (knowledge_id, session) pair with 2+ knowledge_reads
// rows, kind-agnostic (read·hot both count — recall exposure itself is
// the signal). Scenarios run against the in-memory fake engine by
// default; with SUPERLITE_REAL=1 + FLINT_TEST_DSN the same scenarios
// re-run against the real DB (slow, isolated scratch seeding under a
// unique session prefix, cleaned up after).
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

// survivedKreadsRow — one in-memory knowledge_reads row backing the fake
// engine (id + session is all the aggregation needs; kind is carried only
// to prove it does not matter).
type survivedKreadsRow struct {
	id      int
	session string
	kind    string
}

// survivedFakeEngine — computes the (knowledge_id, session) HAVING >=2
// aggregation over the in-memory rows for the id set in args[0],
// mirroring the production SQL (the fake answers with rows; the map
// building under test stays in SurvivedCounts).
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

// survivedPoolStore — the minimal real-DB wire.Store for the slow
// scenario (only Query works — the exportPoolStore convention).
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

// TestSurvivedCountsScenarios — the four contract cases against the fake
// engine (default) or the real DB (SUPERLITE_REAL=1 + FLINT_TEST_DSN,
// slow): same-session 2 reads → 1; two different sessions with 2 reads
// each → 2; a single read → absent (0); no reads at all → absent (0).
// The same-session pair mixes kind read+hot — kind must not matter.
func TestSurvivedCountsScenarios(t *testing.T) {
	ctx := context.Background()
	prefix := fmt.Sprintf("surv-%d-", time.Now().UnixNano())
	kreads := []survivedKreadsRow{
		{1, prefix + "s1", "read"}, {1, prefix + "s1", "hot"}, // same session, read+hot mix → 1 (kind-agnostic)
		{2, prefix + "s2", "read"}, {2, prefix + "s2", "hot"}, // two sessions × 2 reads → 2
		{2, prefix + "s3", "read"}, {2, prefix + "s3", "read"},
		{3, prefix + "s4", "read"}, // single read → 0
		// id 4: no knowledge_reads rows → 0
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
		// cleanup LIFO: delete the seeded rows first, then close the pool
		// (a deferred close would run before this and fail the delete).
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

// TestSurvivedCountsEmptyIDsNoop — empty ids issues no query and returns
// an empty map (no-op).
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

// TestSurvivedCountsSQLContract — the query shape: knowledge_reads,
// (knowledge_id, session) grouping with HAVING count >= 2, ids passed as
// the array arg.
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

// TestSurvivedCountsQueryError — a query failure propagates (the caller
// quietly ignores it — non-interference §13; missing signal degrades to
// no relief).
func TestSurvivedCountsQueryError(t *testing.T) {
	st := &fakeStore{queryErr: fmt.Errorf("db down")}
	if _, err := SurvivedCounts(context.Background(), st, []int{1}); err == nil {
		t.Errorf("쿼리 실패 에러 전파 없음")
	}
}
