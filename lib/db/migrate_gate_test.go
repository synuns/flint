package db

import (
	"context"
	"testing"
)

// TestMigrateGatesViewReapply — 뷰가 최신(해시 일치 + 전부 존재)이면
// Migrate가 뷰 재적용을 스킵한다(F-T50-1 회귀). 매 커맨드마다 뷰를
// DROP+CREATE하면 dolt_schemas가 재작성·스테이징되어 신규 DB의 병렬
// write에서 working-set merge 충돌("schema conflict for dolt_schemas")이
// 발생했다. 재적용 발생 여부는 reapplyViewsFn 호출 수로 검증한다.
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
	r, err := NewRepo(ctx, testDSN())
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	t.Cleanup(r.Close)

	// 준비: views_hash 무효 → Migrate는 재적용 후 해시를 기록한다.
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

	// ① 최신 상태 재실행 — 재적용 스킵(불필요한 dolt_schemas 재작성 차단).
	calls = 0
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(최신): %v", err)
	}
	if calls != 0 {
		t.Fatalf("최신 뷰인데 재적용: calls=%d", calls)
	}

	// ② 해시 불일치(뷰 SQL 갱신 배포 시뮬레이션) — 재적용.
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

	// ③ 뷰 결측(존재 게이트) — 재적용.
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

// TestCommitNothingStagedSucceeds — 병렬 write에서 다른 writer의
// dolt_add('-A')가 이 세션의 변경까지 스테이징·커밋하면, 이 세션의
// dolt_commit는 "nothing to commit"(XX000) 오류를 낸다(F-T50-1 실측:
// 신규 DB 8동시 write의 잔여 exit 1 원인). 스테이징된 게 없는 커밋은
// 데이터가 이미 영속된 것이므로 nil, hash=""로 성공 처리해야 한다.
func TestCommitNothingStagedSucceeds(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("f-t50-1-commit")}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if _, err := r.Commit(ctx, "f-t50-1 probe"); err != nil {
		t.Fatalf("첫 Commit: %v", err)
	}
	// 새로 스테이징된 게 없는 상태의 Commit — 이전에는 재시도 끝
	// ErrRetriesExhausted(exit 1), 이제는 즉시 nil + hash="".
	h, err := r.Commit(ctx, "f-t50-1 probe again")
	if err != nil {
		t.Fatalf("빈 커밋: %v, want nil", err)
	}
	if h != "" {
		t.Fatalf("빈 커밋 hash=%q, want \"\"", h)
	}
}

// viewExists — public 스키마에 해당 뷰가 있는지.
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
