package db

import (
	"context"
	"testing"
)

// TestMigrateGatesViewReapply — when the views are current (hash match
// + all present), Migrate skips view reapplication (regression).
// DROP+CREATE-ing the views on every command rewrote·staged
// dolt_schemas, causing working-set merge conflicts ("schema conflict
// for dolt_schemas") in parallel writes on fresh DBs. Whether
// reapplication occurred is verified by the reapplyViewsFn call count.
func TestMigrateGatesViewReapply(t *testing.T) {
	unlock := sharedDBLock(t)
	t.Cleanup(unlock)
	useFakeSQL(t)

	calls := 0
	saved := reapplyViewsFn
	reapplyViewsFn = func(r *Repo, ctx context.Context) error {
		calls++
		return saved(r, ctx)
	}
	t.Cleanup(func() { reapplyViewsFn = saved })

	ctx := context.Background()
	r, err := NewRepo(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	t.Cleanup(r.Close)
	// A freshly created shared DB has no meta table yet (NewRepo does not
	// migrate) — the DELETE below would hit 42P01 on a new/reshuffled
	// order. Migrate once so the setup is order-independent; the hash
	// DELETE right after invalidates whatever it recorded, keeping the
	// test body unchanged.
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(신규 DB meta 생성): %v", err)
	}

	// Setup: invalidate views_hash → Migrate reapplies then records the
	// hash.
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM meta WHERE key IN ('views_hash','views_sig')`); err != nil {
		t.Fatalf("해시 초기화: %v", err)
	}
	t.Cleanup(func() {
		r.pool.Exec(context.Background(), `DELETE FROM meta WHERE key IN ('views_hash','views_sig')`)
	})
	calls = 0
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(무효 해시): %v", err)
	}
	if calls == 0 {
		t.Fatalf("해시 무효 시에는 재적용해야 함: calls=%d", calls)
	}
	if !viewExists(ctx, t, r, "active_knowledge") {
		t.Fatalf("재적용 후 뷰가 존재해야 함")
	}

	// ① Rerun in the current state — reapplication skipped (blocking
	// needless dolt_schemas rewrites).
	calls = 0
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(최신): %v", err)
	}
	if calls != 0 {
		t.Fatalf("최신 뷰인데 재적용: calls=%d", calls)
	}

	// ② Hash mismatch (simulating a view-SQL update deployment) —
	// reapplied.
	if _, err := r.pool.Exec(ctx,
		`UPDATE meta SET value = 'stale' WHERE key = 'views_hash'`); err != nil {
		t.Fatalf("해시 오염: %v", err)
	}
	calls = 0
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(stale 해시): %v", err)
	}
	if calls == 0 {
		t.Fatalf("해시 불일치 시에는 재적용해야 함")
	}

	// ③ Missing views (the existence gate) — reapplied.
	if _, err := r.pool.Exec(ctx,
		`DROP VIEW IF EXISTS active_knowledge, hot_view`); err != nil {
		t.Fatalf("뷰 삭제: %v", err)
	}
	calls = 0
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(뷰 결측): %v", err)
	}
	if calls == 0 {
		t.Fatalf("뷰 결측 시에는 재적용해야 함")
	}
	if !viewExists(ctx, t, r, "active_knowledge") {
		t.Fatalf("재적용 후 뷰가 존재해야 함")
	}
}

// TestCommitNothingStagedSucceeds — in parallel writes, when another
// writer's dolt_add('-A') stages·commits even this session's changes,
// this session's dolt_commit raises "nothing to commit" (XX000)
// (measured: the residual exit-1 cause of 8 concurrent writes on a
// fresh DB). A commit with nothing staged means the data is already
// persisted, so it must be treated as success with nil, hash="".
func TestCommitNothingStagedSucceeds(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("f-t50-1-commit")}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if _, err := r.Commit(ctx, "f-t50-1 probe"); err != nil {
		t.Fatalf("첫 Commit: %v", err)
	}
	// A Commit with nothing newly staged — previously
	// ErrRetriesExhausted after retries (exit 1); now immediate nil +
	// hash="".
	h, err := r.Commit(ctx, "f-t50-1 probe again")
	if err != nil {
		t.Fatalf("빈 커밋: %v, want nil", err)
	}
	if h != "" {
		t.Fatalf("빈 커밋 hash=%q, want \"\"", h)
	}
}

// viewExists — whether the view exists in the public schema.
func viewExists(ctx context.Context, t *testing.T, r *Repo, name string) bool {
	t.Helper()
	var ok bool
	if err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.relkind = 'v' AND n.nspname = 'public' AND c.relname = $1
		)`, name).Scan(&ok); err != nil {
		t.Fatalf("뷰 존재 확인(%s): %v", name, err)
	}
	return ok
}
