package ops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/db"
)

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

func TestStaleOverflowRealSlow(t *testing.T) {
	ctx := context.Background()
	scratchDSN := testengine.Scratch(t)
	repo, err := db.NewRepo(ctx, scratchDSN)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	pool, err := db.Connect(ctx, scratchDSN)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	ms := &slowMaintStore{reportSlowStore: &reportSlowStore{repo: repo, pool: pool}, repo: repo}
	defer ms.Close()
	if err := ms.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

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

	items, _, err = listStale(ctx, ms, 30, 20)
	if err != nil {
		t.Fatalf("listStale(30): %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("30일 창 — stale %d건, want 2(시드 정상성)", len(items))
	}

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
