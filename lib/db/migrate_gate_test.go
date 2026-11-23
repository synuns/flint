package db

import (
	"context"
	"testing"
)

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

	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(신규 DB meta 생성): %v", err)
	}

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

	calls = 0
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(최신): %v", err)
	}
	if calls != 0 {
		t.Fatalf("최신 뷰인데 재적용: calls=%d", calls)
	}

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

func TestCommitNothingStagedSucceeds(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("f-t50-1-commit")}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if _, err := r.Commit(ctx, "f-t50-1 probe"); err != nil {
		t.Fatalf("첫 Commit: %v", err)
	}

	h, err := r.Commit(ctx, "f-t50-1 probe again")
	if err != nil {
		t.Fatalf("빈 커밋: %v, want nil", err)
	}
	if h != "" {
		t.Fatalf("빈 커밋 hash=%q, want \"\"", h)
	}
}

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
