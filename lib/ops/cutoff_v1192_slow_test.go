// cutoff_v1192_slow_test.go — slow (FLINT_TEST_DSN): the v1.19.2
// overflow-sweep regression on the real Doltgres path — a 200000-day
// stale window (the stale_days override shape the R75 audit measured)
// lists 0 stale rows and invalidates 0 rows on a DB whose rows WOULD be
// stale at any normal window (the pre-fix wrapped cutoff swept every
// active row).
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
)

// slowMaintStore — the real-DB MaintStore: the shared real-DB
// wire.Store (reportSlowStore) plus the Repo-delegated maintenance
// methods (the single-write-path principle — writes_through_repo_test).
type slowMaintStore struct {
	*reportSlowStore
	repo *db.Repo
}

func (s *slowMaintStore) Counts(ctx context.Context) (db.Counts, error) {
	return s.repo.Counts(ctx)
}
func (s *slowMaintStore) DupGroups(ctx context.Context, limit int) ([]db.DupGroup, error) {
	return s.repo.DupGroups(ctx, limit)
}
func (s *slowMaintStore) Invalidate(ctx context.Context, ids []int, reason string) (int64, error) {
	return s.repo.Invalidate(ctx, ids, reason)
}
func (s *slowMaintStore) Supersede(ctx context.Context, oldID, newID int, at time.Time) (int64, error) {
	return s.repo.Supersede(ctx, oldID, newID, at)
}
func (s *slowMaintStore) PruneInvalidated(ctx context.Context, before time.Time) (int64, error) {
	return s.repo.PruneInvalidated(ctx, before)
}
func (s *slowMaintStore) PruneRecallEvents(ctx context.Context, before time.Time) (int64, error) {
	return s.repo.PruneRecallEvents(ctx, before)
}
func (s *slowMaintStore) Merge(ctx context.Context, fromID, toID int) error {
	return s.repo.Merge(ctx, fromID, toID)
}

// TestStaleOverflowRealSlow — 200000-day window on the real path:
// listing 0 and invalidating 0 (synthetic demo rows only).
func TestStaleOverflowRealSlow(t *testing.T) {
	if os.Getenv("FLINT_TEST_DSN") == "" {
		t.Skip("FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	dsn := os.Getenv("FLINT_TEST_DSN")
	base, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	defer base.Close()
	name := fmt.Sprintf("flint_t3cutoff_%d", time.Now().UnixNano()%1e9)
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	t.Cleanup(func() { _, _ = base.Exec(ctx, "DROP DATABASE IF EXISTS "+name) })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	u.Path = "/" + name
	repo, err := db.NewRepo(ctx, u.String())
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	pool, err := db.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	ms := &slowMaintStore{reportSlowStore: &reportSlowStore{repo: repo, pool: pool}, repo: repo}
	defer ms.Close()
	if err := ms.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// 2 synthetic demo rows, 200 days old (stale at any normal window).
	ids, err := ms.InsertBatch(ctx, []map[string]any{
		staleSeedRec("t3-cutoff-a", "t3-ca", "active", ":adr:"),
		staleSeedRec("t3-cutoff-b", "t3-cb", "active", ":adr:"),
	})
	if err != nil {
		t.Fatalf("시드: %v", err)
	}
	for _, id := range ids {
		if _, err := ms.Query(ctx,
			`UPDATE knowledge SET ingested_at = now() - interval '200 days' WHERE id = $1`, id); err != nil {
			t.Fatalf("시드 경과일 적용 id=%d: %v", id, err)
		}
	}

	// The overflow window: 0 stale (the clamped cutoff stays ~292 years
	// in the past; the wrapped arithmetic put it in the future).
	items, days, err := listStale(ctx, ms, 200000, 20)
	if err != nil {
		t.Fatalf("listStale(200000): %v", err)
	}
	if days != 200000 {
		t.Fatalf("days = %d, want 200000", days)
	}
	if len(items) != 0 {
		t.Fatalf("200000일 창 — stale %d건, want 0(래프 시 전면 스윕)", len(items))
	}
	// Contrast: a normal window lists them (the 0 is the clamp, not the seed).
	items, _, err = listStale(ctx, ms, 30, 20)
	if err != nil {
		t.Fatalf("listStale(30): %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("30일 창 — stale %d건, want 2(시드 정상성)", len(items))
	}

	// The application path: --invalidate-stale at the overflow window
	// invalidates 0 rows (the dry-run targets and the confirmed targets match).
	var out strings.Builder
	if code := maintainInvalidateStale(ctx, ms, &out, 200000, 20, true); code != 0 {
		t.Fatalf("invalidate-stale exit = %d: %s", code, out.String())
	}
	var res struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &res); err != nil {
		t.Fatalf("JSON 파싱: %v — %s", err, out.String())
	}
	if res.Count != 0 {
		t.Fatalf("무효화 = %d건, want 0(래프 시 활성 지식 전면 무효화)", res.Count)
	}
	rowsInv, err := ms.Query(ctx, `SELECT count(*) FROM knowledge WHERE invalid_at IS NOT NULL`)
	if err != nil {
		t.Fatalf("invalid_at 카운트 조회: %v", err)
	}
	invalidated := 0
	if len(rowsInv) > 0 && len(rowsInv[0]) > 0 {
		if n, ok := rowsInv[0][0].(int64); ok {
			invalidated = int(n)
		}
	}
	if invalidated != 0 {
		t.Fatalf("invalid_at 설정 행 = %d건, want 0", invalidated)
	}
}
