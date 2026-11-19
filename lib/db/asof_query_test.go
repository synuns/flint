// asof_query_test.go — QueryAsOf (the Doltgres implementation of the
// wire.AsOfQuery contract) slow real-DB regression. Measures the
// assembled AS OF query (count(*)·WHERE $N+ORDER BY tail clause)·the
// table whitelist·the hash charset·column identifier validation·and the
// view (active_knowledge) incompatibility path yielding the
// guidance-worded error without a base retry (original-equivalent).
package db

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

// t4Rec — a synthetic demo knowledge with a specified domain
// (knowledgeRec's default domain=main).
func t4Rec(hash, domain string) map[string]any {
	rec := knowledgeRec(hash)
	rec["domain"] = domain
	return rec
}

// TestRetryOnce — the one-read-retry contract (spec §7, the db-side
// copy equivalent to wire.QueryOnce). Only the first failure re-runs
// (both success·final failure capped at 2 attempts).
func TestRetryOnce(t *testing.T) {
	t.Run("첫 실패 후 재시도 성공", func(t *testing.T) {
		n := 0
		v, err := retryOnce(func() (string, error) {
			n++
			if n == 1 {
				return "", fmt.Errorf("transient")
			}
			return "ok", nil
		})
		if err != nil || v != "ok" || n != 2 {
			t.Fatalf("retryOnce = (%q, %v) attempts %d, want (\"ok\", nil) 2회", v, err, n)
		}
	})
	t.Run("연속 실패 — 1회만 재시도", func(t *testing.T) {
		n := 0
		_, err := retryOnce(func() (int, error) {
			n++
			return 0, fmt.Errorf("down")
		})
		if err == nil || n != 2 {
			t.Fatalf("retryOnce 연속 실패 = %v attempts %d, want 오류 2회", err, n)
		}
	})
	t.Run("첫 성공 — 재시도 없음", func(t *testing.T) {
		n := 0
		v, err := retryOnce(func() (int, error) {
			n++
			return 7, nil
		})
		if err != nil || v != 7 || n != 1 {
			t.Fatalf("retryOnce 첫 성공 = (%d, %v) attempts %d, want (7, nil) 1회", v, err, n)
		}
	})
}

// TestQueryAsOfSlowAssembly — the assembly·validation path. Seed: 2
// main + 1 other rows, commit, then query at that point in time.
func TestQueryAsOfSlowAssembly(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{
		t4Rec("v0180-t4-a", "main"),
		t4Rec("v0180-t4-b", "main"),
		t4Rec("v0180-t4-o", "other"),
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	// Seed the same way as the T8 (status --as-of) real path — the status
	// column defaults to 'active', so switch to pending then commit.
	if _, err := r.pool.Exec(ctx, `UPDATE knowledge SET status = 'pending'`); err != nil {
		t.Fatalf("seed status pending: %v", err)
	}
	h, err := r.Commit(ctx, "flint v0180 t4 demo seed")
	if err != nil || h == "" {
		t.Fatalf("Commit: %v (hash %q)", err, h)
	}

	// count(*) — a scalar 1 row.
	got, err := r.QueryAsOf(ctx, wire.AsOfQuery{Hash: h, Table: "knowledge", Columns: []string{"count(*)"}})
	if err != nil {
		t.Fatalf("QueryAsOf(count): %v", err)
	}
	if len(got) != 1 || fmt.Sprint(got[0][0]) != "3" {
		t.Fatalf("QueryAsOf(count) = %v, want [[3]]", got)
	}

	// WHERE $1 + ORDER BY tail clause + LIMIT literal — Args positional
	// binding.
	got, err = r.QueryAsOf(ctx, wire.AsOfQuery{
		Hash:    h,
		Table:   "knowledge",
		Columns: []string{"topic"},
		Where:   "WHERE domain = $1 ORDER BY topic ASC",
		Args:    []any{"main"},
		Limit:   2,
	})
	if err != nil {
		t.Fatalf("QueryAsOf(where): %v", err)
	}
	if len(got) != 2 || fmt.Sprint(got[0][0]) != "토픽-v0180-t4-a" || fmt.Sprint(got[1][0]) != "토픽-v0180-t4-b" {
		t.Fatalf("QueryAsOf(where+order) = %v, want main 2건 topic ASC", got)
	}

	// The conditions-only Where form — reproducing the status (T8) path.
	// Conditions without a clause keyword must get the " WHERE " prefix
	// from the assembler (without it, "... AS OF 'h' status = ..." is a
	// 42601 syntax error — CI run 44225427183).
	got, err = r.QueryAsOf(ctx, wire.AsOfQuery{Hash: h, Table: "knowledge",
		Columns: []string{"count(*)"}, Where: "status = 'pending' AND invalid_at IS NULL"})
	if err != nil {
		t.Fatalf("QueryAsOf(조건-only Where): %v", err)
	}
	if len(got) != 1 || fmt.Sprint(got[0][0]) != "3" {
		t.Fatalf("QueryAsOf(조건-only Where) = %v, want [[3]] (시드 3건 전부 pending)", got)
	}

	// The view path — active_knowledge AS OF queried normally (all rows
	// active).
	got, err = r.QueryAsOf(ctx, wire.AsOfQuery{Hash: h, Table: "active_knowledge", Columns: []string{"count(*)"}})
	if err != nil {
		t.Fatalf("QueryAsOf(view count): %v", err)
	}
	if len(got) != 1 || fmt.Sprint(got[0][0]) != "3" {
		t.Fatalf("QueryAsOf(view count) = %v, want [[3]]", got)
	}

	// A table outside the whitelist — rejected (sessions is a real table
	// but not permitted).
	if _, err := r.QueryAsOf(ctx, wire.AsOfQuery{Hash: h, Table: "sessions", Columns: []string{"count(*)"}}); err == nil ||
		!strings.Contains(err.Error(), "sessions") {
		t.Fatalf("QueryAsOf(화이트리스트 외) 오류 없음/메시지 부재: %v", err)
	}

	// Invalid hashes — uppercase·short prefixes rejected + the original
	// error wording preserved (read.go).
	for _, bad := range []string{"ABC12345", "abc", "v0180;--"} {
		_, err := r.QueryAsOf(ctx, wire.AsOfQuery{Hash: bad, Table: "knowledge", Columns: []string{"count(*)"}})
		if err == nil {
			t.Fatalf("QueryAsOf(해시 %q) 오류 없음", bad)
		}
		if !strings.Contains(err.Error(), "as-of hash charset violation") {
			t.Fatalf("QueryAsOf(해시 %q) 오류 문구 = %q, want 원문 'as-of hash charset violation'", bad, err)
		}
	}

	// Invalid column identifiers — function·alias character sets outside
	// the allowed set rejected.
	for _, bad := range []string{"topic; DELETE FROM knowledge", "Topic", "topic--x"} {
		if _, err := r.QueryAsOf(ctx, wire.AsOfQuery{Hash: h, Table: "knowledge", Columns: []string{bad}}); err == nil {
			t.Fatalf("QueryAsOf(컬럼 %q) 오류 없음", bad)
		}
	}
}

// TestQueryAsOfSlowViewFallback — the view-incompatibility guidance
// error (no fallback, original-equivalent). After the seed commit (hA),
// add a column to the base and redefine the view to reference it (no
// commit needed — views always use the current definition). Rows at hA
// lack the column, so the view AS OF query fails with "column ... does
// not exist", and the result must be the immediate guidance-wrapped
// error without a base (knowledge) retry (the base lacks the
// invalid_at filter, so a successful retry would expose invalid
// knowledge).
func TestQueryAsOfSlowViewFallback(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{t4Rec("v0180-t4-fb", "main")}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	hA, err := r.Commit(ctx, "flint v0180 t4 demo fallback base")
	if err != nil || hA == "" {
		t.Fatalf("Commit: %v (hash %q)", err, hA)
	}
	const extra = "v0180t4extra"
	dropExtra := func() { _, _ = r.pool.Exec(ctx, "ALTER TABLE knowledge DROP COLUMN "+extra) }
	// Recover a residual column from a prior run (an absent-DROP error is
	// ignored — Doltgres lacks DROP COLUMN IF EXISTS) then add it for
	// idempotent seeding.
	dropExtra()
	if _, err := r.pool.Exec(ctx, "ALTER TABLE knowledge ADD COLUMN "+extra+" text"); err != nil {
		t.Fatalf("alter add column: %v", err)
	}
	t.Cleanup(func() { dropExtra() })
	// Explicit restoration of the original view — restores this test's
	// experimental active_knowledge definition back to the deployed one.
	// The subsequent test's newTestRepo→Migrate views_sig-mismatch
	// reapplication effectively restores it (self-healing), but that
	// implicit dependency is removed in favor of an explicit restore —
	// reusing the same reapplication path as Migrate (reapplyViews:
	// simple-protocol DROP+CREATE). Registered later than dropExtra so
	// LIFO runs it first — the restored definition does not reference the
	// extra column, so it is independent of the column-drop order.
	t.Cleanup(func() {
		if err := reapplyViewsFn(r, ctx); err != nil {
			t.Errorf("active_knowledge 원본 복원 실패: %v", err)
		}
	})
	// The view-redefinition helper — Doltgres appears to cache the
	// validation results of similar queries after a view AS OF error
	// fires (measured), so each step resets via redefinition.
	redefineView := func() {
		t.Helper()
		if _, err := r.pool.Exec(ctx,
			"CREATE OR REPLACE VIEW active_knowledge AS SELECT id, topic, "+extra+" FROM knowledge WHERE invalid_at IS NULL"); err != nil {
			t.Fatalf("view redefine: %v", err)
		}
	}
	// Common to the view-query steps — from a fresh definition, the hA
	// point-in-time view query must always yield the guidance error
	// (reproducibility checked by the ①·③ sandwich; ② is the path that
	// in the fallback era returned rows via a base retry — the core of
	// the regression assertion).
	viewQ := wire.AsOfQuery{Hash: hA, Table: "active_knowledge", Columns: []string{"id", "topic"}}

	// ① The guidance-wrapped error (original preserved).
	redefineView()
	if _, err := r.QueryAsOf(ctx, viewQ); err == nil || !strings.Contains(err.Error(),
		"try an AS OF query on the base table (knowledge)") {
		t.Fatalf("QueryAsOf(뷰 비호환) = %v, want 안내 문구 랩 오류", err)
	}

	// ② The regression — the fallback fully removed: even with a view
	// error, it must not return rows via a base (knowledge) retry
	// (invalid-knowledge exposure). It must be the guidance-wrapped
	// error.
	redefineView()
	if rows, err := r.QueryAsOf(ctx, viewQ); err == nil || !strings.Contains(err.Error(),
		"try an AS OF query on the base table (knowledge)") {
		t.Fatalf("QueryAsOf(폴백 제거 회귀) = (%v, %v), want 안내 문구 랩 오류(base 재시도 금지)", rows, err)
	}

	// ③ Reproducibility — from a fresh definition again the wrapped error
	// (the sandwich's lower slice).
	redefineView()
	if _, err := r.QueryAsOf(ctx, viewQ); err == nil || !strings.Contains(err.Error(),
		"try an AS OF query on the base table (knowledge)") {
		t.Fatalf("QueryAsOf(재현) = %v, want 안내 문구 랩 오류", err)
	}
}

// TestActiveKnowledgeRestoredAfterViewFallback — self-containedly
// verifies the path (an explicit-restoration regression) by which the
// view experiments (the TestQueryAsOfSlowViewFallback family) restore
// active_knowledge to the deployed definition. In the era without the
// restoration cleanup, the view stayed experimental and this assertion
// failed. Historically the shared DB's residual view state was probed
// over a raw connection, but the newTestRepo switch to scratch DBs
// means ViewFallback pollution no longer reaches the shared DB — so the
// pollution is planted directly (simple-protocol DROP+CREATE —
// replacing even the server catalog, reproducing the actual experiment
// path) and the view freshness (the views_hash·views_sig double gate)
// is checked after restoration (reapplyViews). It does not go through
// the Migrate right after newTestRepo — because the self-healing
// reapplication would mask a missing restoration (inheriting the
// original contract).
func TestActiveKnowledgeRestoredAfterViewFallback(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// Pollution — replace with an experimental definition (single id
	// column). The gate must catch it for the premise to hold.
	plantStaleView(t, r.dsn)
	if r.viewsCurrent(ctx, viewsSQLFn()) {
		t.Fatal("실험 정의 오염이 views 게이트에 잡히지 않음 — 검증 전제 위반")
	}
	// Explicit restoration — the same path as the ViewFallback cleanup
	// (reapplyViews: simple-protocol DROP+CREATE; meta already holds the
	// deployed digest, so no rewrite needed).
	if err := reapplyViewsFn(r, ctx); err != nil {
		t.Fatalf("active_knowledge 원본 복원 실패: %v", err)
	}
	if !r.viewsCurrent(ctx, viewsSQLFn()) {
		t.Fatal("active_knowledge가 배포 정의가 아니다 — ViewFallback 테스트 원본 복원 누락")
	}
}
