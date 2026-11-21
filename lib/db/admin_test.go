// admin_test.go — updates·aggregations (approval·invalidation·merging·
// counts·consecutive unrecorded) slow tests.
package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/sqlfs"
	"github.com/synuns/flint/lib/store"
)

// pendingRec is a status=pending knowledge record (the pre-review-gate
// state).
func pendingRec(hash string) map[string]any {
	rec := knowledgeRec(hash)
	rec["status"] = "pending"
	return rec
}

// sessionAt is a session record with an explicit ts (for
// StaleSessionRuns ordering verification — Doltgres now() is the
// transaction time, so automatic values shuffle the order).
func sessionAt(agent string, ts time.Time) map[string]any {
	return map[string]any{"kind": "session", "agent": agent, "summary": "세션", "ts": ts}
}

// TestApproveIdempotent — 2 approves → the same state·the 2nd also
// without error.
func slowApproveIdempotent(t *testing.T, r *Repo) {
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{pendingRec("appr-1")})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	id := ids[0]
	if err := r.Approve(ctx, id); err != nil {
		t.Fatalf("first Approve: %v", err)
	}
	if err := r.Approve(ctx, id); err != nil {
		t.Fatalf("second Approve (want idempotent no-op): %v", err)
	}
	var status string
	if err := r.pool.QueryRow(ctx,
		`SELECT status FROM knowledge WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "active" {
		t.Fatalf("want status active after approve, got %q", status)
	}
}

// TestApproveMissing — a missing id is ErrNotFound.
func slowApproveMissing(t *testing.T, r *Repo) {
	if err := r.Approve(context.Background(), 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestInvalidateSetsInvalidAt — invalid_at NULL→NOT NULL.
func slowInvalidateSetsInvalidAt(t *testing.T, r *Repo) {
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("inv-1")})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if _, err := r.Invalidate(ctx, []int{ids[0]}, "중복 의심 정리"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	var invalidAt *time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT invalid_at FROM knowledge WHERE id = $1`, ids[0]).Scan(&invalidAt); err != nil {
		t.Fatalf("read invalid_at: %v", err)
	}
	if invalidAt == nil {
		t.Fatal("invalid_at still NULL after Invalidate")
	}
	// A missing id is ErrNotFound.
	if _, err := r.Invalidate(ctx, []int{99999}, "사유"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for missing id, got %v", err)
	}
}

// TestMergeMarksAndInvalidates — from invalidated + the merge marker on
// to's summary.
func slowMergeMarksAndInvalidates(t *testing.T, r *Repo) {
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("merge-from"),
		knowledgeRec("merge-to"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	from, to := ids[0], ids[1]
	if err := r.Merge(ctx, from, to); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	var invalidAt *time.Time
	var summary string
	if err := r.pool.QueryRow(ctx,
		`SELECT invalid_at, summary FROM knowledge WHERE id = $1`, from).
		Scan(&invalidAt, &summary); err != nil {
		t.Fatalf("read from: %v", err)
	}
	if invalidAt == nil {
		t.Fatal("from.invalid_at still NULL after Merge")
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT summary FROM knowledge WHERE id = $1`, to).Scan(&summary); err != nil {
		t.Fatalf("read to: %v", err)
	}
	if !containsMarker(summary, from) {
		t.Fatalf("to.summary lacks merge marker for from=%d: %q", from, summary)
	}
	// Missing ids are ErrNotFound.
	if err := r.Merge(ctx, 99999, to); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for missing from, got %v", err)
	}
	if err := r.Merge(ctx, from, 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for missing to, got %v", err)
	}
}

func containsMarker(summary string, fromID int) bool {
	return strings.Contains(summary, fmt.Sprintf("[merged:from=%d]", fromID))
}

// TestCounts — the pending count + the duplicate-suspect count (2+
// active rows sharing a topic).
func slowCounts(t *testing.T, r *Repo) {
	ctx := context.Background()
	dupA := knowledgeRec("cnt-dup-a")
	dupA["topic"] = "dup-topic"
	dupB := knowledgeRec("cnt-dup-b")
	dupB["topic"] = "dup-topic"
	if _, err := r.InsertBatch(ctx, []map[string]any{
		pendingRec("cnt-p1"),
		pendingRec("cnt-p2"),
		dupA,
		dupB,
		knowledgeRec("cnt-solo"), // unique topic — not a duplicate suspect
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	got, err := r.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if got.Pending != 2 {
		t.Errorf("Pending = %d, want 2", got.Pending)
	}
	if got.DupSuspect != 2 {
		t.Errorf("DupSuspect = %d, want 2 (dup-topic 2건)", got.DupSuspect)
	}
}

// TestStaleSessionRuns — the consecutive count of sessions without a
// knowledge write, from the most recent. The aggregation must stop at
// the first session with a record (not the total unrecorded count).
func slowStaleSessionRuns(t *testing.T, r *Repo) {
	ctx := context.Background()
	base := time.Now().Add(-10 * time.Hour)
	recs := []map[string]any{sessionAt("stale-a", base)} // oldest: has a record
	ids, err := r.InsertBatch(ctx, recs)
	if err != nil {
		t.Fatalf("InsertBatch base: %v", err)
	}
	kn := knowledgeRec("stale-k")
	kn["session_id"] = ids[0]
	if _, err := r.InsertBatch(ctx, []map[string]any{kn}); err != nil {
		t.Fatalf("InsertBatch knowledge: %v", err)
	}
	// The 3 subsequent sessions are unrecorded → consecutive 3.
	if _, err := r.InsertBatch(ctx, []map[string]any{
		sessionAt("stale-a", base.Add(1*time.Hour)),
		sessionAt("stale-a", base.Add(2*time.Hour)),
		sessionAt("stale-a", base.Add(3*time.Hour)),
	}); err != nil {
		t.Fatalf("InsertBatch stale: %v", err)
	}
	got, err := r.StaleSessionRuns(ctx, "stale-a")
	if err != nil {
		t.Fatalf("StaleSessionRuns: %v", err)
	}
	if got != 3 {
		t.Errorf("StaleSessionRuns = %d, want 3 (stops at recorded session)", got)
	}
	// 0 when the most recent session has a record.
	fresh := knowledgeRec("stale-k2")
	freshIDs, err := r.InsertBatch(ctx, []map[string]any{sessionAt("fresh-a", base.Add(4*time.Hour))})
	if err != nil {
		t.Fatalf("InsertBatch fresh: %v", err)
	}
	fresh["session_id"] = freshIDs[0]
	if _, err := r.InsertBatch(ctx, []map[string]any{fresh}); err != nil {
		t.Fatalf("InsertBatch fresh knowledge: %v", err)
	}
	got, err = r.StaleSessionRuns(ctx, "fresh-a")
	if err != nil {
		t.Fatalf("StaleSessionRuns fresh: %v", err)
	}
	if got != 0 {
		t.Errorf("StaleSessionRuns fresh = %d, want 0", got)
	}
}

// resetVersionToV1 establishes the v1-starting premise of the
// promotion-gate tests. It actually records '1' at call time — when the
// shared DB is already at a higher version (via other packages' —
// lib/cli·data's newRealStore chains), the gate's numeric-comparison
// short circuit (n>=N → no-op) means EnsureSchemaV2 never records '2',
// breaking the test's "meta='2' state" premise (the CI 39717583172
// occasion). In the string-equality-check era it rolled back even from
// higher versions and recorded '2', so the premise held by accident.
// After the test it rolls back to '1' again so other tests' newTestRepo
// are unaffected.
func resetVersionToV1(t *testing.T, r *Repo) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(),
		`UPDATE meta SET value = '1' WHERE key = 'schema_version'`); err != nil {
		t.Fatalf("reset schema_version to 1: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(),
			`UPDATE meta SET value = '1' WHERE key = 'schema_version'`)
	})
}

// TestMigrateAcceptsV2 — regression: with meta='2', Migrate passes with
// err=nil and does not roll the version back to '1'. The gate assumed
// only '1' and produced a nil wrap failure ("%!w(<nil>)").
func slowMigrateAcceptsV2(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	if err := r.EnsureSchemaV2(ctx); err != nil {
		t.Fatalf("EnsureSchemaV2: %v", err)
	}
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate under schema_version=2: %v", err)
	}
	var version string
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "2" {
		t.Fatalf("schema_version = %q, want 2 preserved by Migrate", version)
	}
}

// dropV3Indexes rolls back so the EnsureSchemaV3 tests leave no flint
// indexes behind (so other tests' newTestRepo are unaffected). Also
// cleans the v9 flint_knowledge_pending_idx — the helper's contract is
// resetting all gate-specific flint knowledge indexes.
func dropV3Indexes(t *testing.T, r *Repo) {
	t.Helper()
	t.Cleanup(func() {
		for _, name := range []string{
			"flint_knowledge_entity_idx",
			"flint_knowledge_domain_idx",
			"flint_knowledge_status_idx",
			"flint_knowledge_pending_idx",
		} {
			_, _ = r.pool.Exec(context.Background(), `DROP INDEX IF EXISTS `+name)
		}
	})
}

// flintIndexCount explicitly counts only the 3 v3 gate indexes
// (entity·domain·status). A bulk count of LIKE 'flint_knowledge_%'
// would also count schema v9's flint_knowledge_pending_idx and fail at
// 4 — it counts only the verification target (this gate's indexes).
func flintIndexCount(t *testing.T, r *Repo) int {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pg_indexes
		 WHERE tablename = 'knowledge' AND indexname IN (
		   'flint_knowledge_entity_idx',
		   'flint_knowledge_domain_idx',
		   'flint_knowledge_status_idx')`).
		Scan(&n); err != nil {
		t.Fatalf("count flint indexes: %v", err)
	}
	return n
}

// TestEnsureSchemaV3Idempotent — 2 calls → 3 indexes·version 3, the 2nd
// unchanged. Protocol: a DB that passed the v2 gate (the existing
// deployment path) also promotes.
func slowEnsureSchemaV3Idempotent(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	dropV3Indexes(t, r)
	ctx := context.Background()
	// The existing deployment path: a DB that passed v1 → the v2 gate.
	if err := r.EnsureSchemaV2(ctx); err != nil {
		t.Fatalf("EnsureSchemaV2: %v", err)
	}
	if err := r.EnsureSchemaV3(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV3: %v", err)
	}
	if n := flintIndexCount(t, r); n != 3 {
		t.Fatalf("flint indexes = %d, want 3 (entity·domain·status)", n)
	}
	for _, name := range []string{
		"flint_knowledge_entity_idx",
		"flint_knowledge_domain_idx",
		"flint_knowledge_status_idx",
	} {
		var exists bool
		if err := r.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_indexes
			 WHERE tablename = 'knowledge' AND indexname = $1)`, name).
			Scan(&exists); err != nil {
			t.Fatalf("check index %s: %v", name, err)
		}
		if !exists {
			t.Errorf("index %s missing after EnsureSchemaV3", name)
		}
	}
	var version string
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "3" {
		t.Fatalf("schema_version = %q, want 3", version)
	}
	// The 2nd call is an idempotent no-op — 3 indexes·version 3
	// unchanged.
	if err := r.EnsureSchemaV3(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV3 (want idempotent): %v", err)
	}
	if n := flintIndexCount(t, r); n != 3 {
		t.Fatalf("flint indexes after second call = %d, want 3", n)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("re-read schema_version: %v", err)
	}
	if version != "3" {
		t.Fatalf("schema_version after second call = %q, want 3", version)
	}
}

// TestEnsureSchemaV3FromV1 — a v1 DB that skipped v2 also promotes with
// the single v3 gate (the gate only updates the version; the indexes
// apply idempotently via IF NOT EXISTS).
func slowEnsureSchemaV3FromV1(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	dropV3Indexes(t, r)
	if err := r.EnsureSchemaV3(context.Background()); err != nil {
		t.Fatalf("EnsureSchemaV3 from v1: %v", err)
	}
	if n := flintIndexCount(t, r); n != 3 {
		t.Fatalf("flint indexes = %d, want 3", n)
	}
}

// TestEnsureSchemaV2 — a v1 DB's (fake DDL) lossless promotion: ALTER
// column additions + version 2 + existing data kept + idempotent.
func slowEnsureSchemaV2(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()
	// v1-era data — must survive the promotion.
	oldIDs, err := r.InsertBatch(ctx, []map[string]any{
		{"kind": "session", "agent": "v1", "summary": "구세션"},
	})
	if err != nil {
		t.Fatalf("InsertBatch v1 data: %v", err)
	}
	if err := r.EnsureSchemaV2(ctx); err != nil {
		t.Fatalf("EnsureSchemaV2: %v", err)
	}
	var version string
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "2" {
		t.Fatalf("schema_version = %q, want 2", version)
	}
	// Writable via the v2 columns (a behavior proof of their presence).
	if _, err := r.InsertBatch(ctx, []map[string]any{
		{"kind": "session", "agent": "v2", "summary": "신세션", "tasks": "v2 작업"},
		{"kind": "project", "name": "v2-proj", "note": "v2 메모"},
	}); err != nil {
		t.Fatalf("InsertBatch with v2 columns: %v", err)
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE id = $1`, oldIDs[0]).Scan(&n); err != nil {
		t.Fatalf("count old session: %v", err)
	}
	if n != 1 {
		t.Fatalf("v1 데이터 유실: old session count = %d, want 1", n)
	}
	// The rerun is an idempotent no-op.
	if err := r.EnsureSchemaV2(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV2 (want idempotent): %v", err)
	}
}

// TestAllGatesArtifacts — table-driven assertion that gates v2~v7 each
// actually create their promised artifacts (columns·indexes·tables).
// Previously only the newest gates (v6·v7) had their contents verified
// while v2~v5 were checked only for 'sequential passage' through the
// chain — a gate quietly turning into a no-op would not have been
// caught.
func slowAllGatesArtifacts(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()

	gates := []struct {
		name  string
		gate  func(context.Context) error
		check func(t *testing.T)
	}{
		{"v2", r.EnsureSchemaV2, func(t *testing.T) {
			assertColumn(t, r, ctx, "sessions", "tasks")
			assertColumn(t, r, ctx, "projects", "note")
		}},
		{"v3", r.EnsureSchemaV3, func(t *testing.T) {
			for _, idx := range []string{"flint_knowledge_entity_idx", "flint_knowledge_domain_idx", "flint_knowledge_status_idx"} {
				assertObject(t, r, ctx, idx)
			}
		}},
		{"v4", r.EnsureSchemaV4, func(t *testing.T) {
			assertColumn(t, r, ctx, "knowledge", "read_count")
			assertColumn(t, r, ctx, "knowledge", "last_read_at")
		}},
		{"v5", r.EnsureSchemaV5, func(t *testing.T) {
			assertColumn(t, r, ctx, "knowledge", "superseded_by")
		}},
		{"v6", r.EnsureSchemaV6, func(t *testing.T) {
			assertObject(t, r, ctx, "read_events")
		}},
		{"v7", r.EnsureSchemaV7, func(t *testing.T) {
			assertColumn(t, r, ctx, "read_events", "ids")
			assertObject(t, r, ctx, "session_starts")
			assertObject(t, r, ctx, "hot_events")
		}},
		{"v9", r.EnsureSchemaV9, func(t *testing.T) {
			for _, idx := range v9IndexNames {
				assertObject(t, r, ctx, idx)
			}
		}},
		{"v11", func(ctx context.Context) error {
			// Faithful v10-DB reproduction (after the R43 direct-run short
			// circuit) — v10 has no recall_events (the v12 gate creates it
			// first). Dropping any recall_events left on the fresh DB
			// measures the promotion-artifact (2-column add) path (the
			// fresh-DB direct path is TestV11GateFreshDirectToV12).
			if _, err := r.pool.Exec(ctx, `DROP TABLE IF EXISTS recall_events`); err != nil {
				return err
			}
			return r.EnsureSchemaV11(ctx)
		}, func(t *testing.T) {
			assertColumn(t, r, ctx, "knowledge", "recall_score")
			assertColumn(t, r, ctx, "knowledge", "last_recalled_at")
		}},
	}
	for _, g := range gates {
		if err := g.gate(ctx); err != nil {
			t.Fatalf("게이트 %s: %v", g.name, err)
		}
		g.check(t)
	}
}

// assertColumn — asserts a table column exists (information_schema).
func assertColumn(t *testing.T, r *Repo, ctx context.Context, table, column string) {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name = $1 AND column_name = $2`, table, column).Scan(&n); err != nil {
		t.Fatalf("%s.%s 조회: %v", table, column, err)
	}
	if n != 1 {
		t.Errorf("게이트 산출물 %s.%s 부재(컬럼)", table, column)
	}
}

// assertObject — asserts a table·index exists (to_regclass).
func assertObject(t *testing.T, r *Repo, ctx context.Context, name string) {
	t.Helper()
	var exists bool
	if err := r.pool.QueryRow(ctx,
		`SELECT to_regclass('public.`+name+`') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("%s 존재 조회: %v", name, err)
	}
	if !exists {
		t.Errorf("게이트 산출물 %s 부재(테이블/인덱스)", name)
	}
}

// dropV11ScoreColumns — when the v11 gate tests reproduce a v10 DB,
// rolls back knowledge's 2 score columns (Doltgres lacks DROP COLUMN
// IF EXISTS — check columnExists then DROP. Inheriting the
// admin_v10_test.go convention).
func dropV11ScoreColumns(t *testing.T, r *Repo) {
	t.Helper()
	for _, col := range []string{"recall_score", "last_recalled_at"} {
		if columnExists(t, r, "knowledge", col) {
			if _, err := r.pool.Exec(context.Background(),
				`ALTER TABLE knowledge DROP COLUMN `+col); err != nil {
				t.Fatalf("v10 재현 knowledge.%s 삭제: %v", col, err)
			}
		}
	}
}

// TestV11GateAdditive — reproducing a v10 DB → the EnsureSchemaV11
// promotion additively adds the recall_score (int, default 0)·
// last_recalled_at (timestamptz NULL) columns, with existing rows
// intact·defaults (0·NULL)·the rerun idempotent. The reverse direction
// (an older binary reading a v11 DB) is covered by a spot of the
// current binary inserting·reading v11 values under the columns'
// additive-default premise (a behavior proof of the score-value path).
func slowV11GateAdditive(t *testing.T, r *Repo) {
	ctx := context.Background()
	dropV11ScoreColumns(t, r)
	// Faithful v10-DB reproduction (after the R43 direct-run short
	// circuit) — v10 has no recall_events (the v12 gate creates it
	// first). If it remains, the v11 gate's fresh-DB direct-run verdict
	// skips the 2-column add (that path is
	// TestV11GateFreshDirectToV12).
	if _, err := r.pool.Exec(ctx, `DROP TABLE IF EXISTS recall_events`); err != nil {
		t.Fatalf("v10 재현 recall_events 삭제: %v", err)
	}
	setVersion(t, r, "10")

	// Pre-promotion existing data — the intactness-proof baseline.
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v11-old")})
	if err != nil {
		t.Fatalf("InsertBatch pre-v11: %v", err)
	}

	if err := r.EnsureSchemaV11(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV11: %v", err)
	}
	if v := readVersion(t, r); v != "11" {
		t.Fatalf("schema_version = %q, want 11", v)
	}
	for _, col := range []string{"recall_score", "last_recalled_at"} {
		if !columnExists(t, r, "knowledge", col) {
			t.Fatalf("knowledge.%s 부재 — v11 additive 계약 위반", col)
		}
	}

	// Existing rows keep the defaults (0·NULL)·body intact.
	var score int
	var last *time.Time
	var topic string
	if err := r.pool.QueryRow(ctx,
		`SELECT recall_score, last_recalled_at, topic FROM knowledge WHERE id = $1`, ids[0]).
		Scan(&score, &last, &topic); err != nil {
		t.Fatalf("기존 knowledge 행 조회: %v", err)
	}
	if score != 0 || last != nil {
		t.Fatalf("기존 행 스코어 열 = (%d,%v), want (0,NULL — 컬럼 기본값)", score, last)
	}
	if topic == "" {
		t.Fatalf("기존 knowledge 행 유실 — 무손상 계약 위반")
	}

	// The v11 value insert·read spot (covering the reverse direction —
	// the current binary generating·reading scores).
	wantLast := time.Date(2026, 11, 15, 3, 0, 0, 0, time.UTC)
	rec := knowledgeRec("v11-score")
	rec["recall_score"] = 7
	rec["last_recalled_at"] = wantLast
	newIDs, err := r.InsertBatch(ctx, []map[string]any{rec})
	if err != nil {
		t.Fatalf("InsertBatch with v11 columns: %v", err)
	}
	var gotScore int
	var gotLast *time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT recall_score, last_recalled_at FROM knowledge WHERE id = $1`, newIDs[0]).
		Scan(&gotScore, &gotLast); err != nil {
		t.Fatalf("v11 행 조회: %v", err)
	}
	if gotScore != 7 {
		t.Fatalf("recall_score = %d, want 7", gotScore)
	}
	if gotLast == nil || !gotLast.Equal(wantLast) {
		t.Fatalf("last_recalled_at = %v, want %v", gotLast, wantLast)
	}

	// Idempotent — the rerun is a no-op (version 11 kept·no error).
	if err := r.EnsureSchemaV11(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV11 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "11" {
		t.Fatalf("재호출 후 schema_version = %q, want 11", v)
	}
}

// TestV11GateNumeric — a higher-version DB no-ops via numeric
// comparison (a downgrade-prevention regression — inheriting the
// TestV10GateNumeric convention).
func slowV11GateNumeric(t *testing.T, r *Repo) {
	setVersion(t, r, "99")
	if err := r.EnsureSchemaV11(context.Background()); err != nil {
		t.Fatalf("EnsureSchemaV11(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "99" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 99(수치 비교 no-op)", v)
	}
}

// TestTelemetryStageTablesV13 — the schema v13 metering tables
// (threshold_snapshots·weekly_rollups) must be explicit
// telemetryStageTables members (the no-global-dolt_add principle: an
// unstaged metering table never reaches the telemetry commit). The v13
// tables carry no serial id column, so telemetryStageSeqs stays at the
// existing 4 (Seqs would grow only with a serial-adopting table).
func TestTelemetryStageTablesV13(t *testing.T) {
	for _, tbl := range []string{"threshold_snapshots", "weekly_rollups"} {
		found := false
		for _, cur := range telemetryStageTables {
			if cur == tbl {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("telemetryStageTables에 %s 부재 — v13 계측 테이블 미스테이징(전역 dolt_add 금지 위반 소지)", tbl)
		}
	}
	if len(telemetryStageSeqs) != 4 {
		t.Errorf("telemetryStageSeqs = %d건, want 4(v13 테이블은 serial 부재 — Seqs 불변)", len(telemetryStageSeqs))
	}
}

// TestV13ThresholdKeys — the schema v13 threshold keys: defaults
// (90·90·336), meta override reflection, and the unset-keeps-0
// convention (the consumer fallback basis — the correction 2-key
// convention). 336h = 14 days, aligned with the events retention
// default (metering freshness lags beyond the retention window are the
// doctor warning target).
func TestV13ThresholdKeys(t *testing.T) {
	def := store.DefaultThresholds()
	if def.WeeklyRollupsRetentionD != 90 {
		t.Errorf("WeeklyRollupsRetentionD 기본값=%d, want 90(현행 *_retention_days 관례)", def.WeeklyRollupsRetentionD)
	}
	if def.ThresholdSnapshotsRetentionD != 90 {
		t.Errorf("ThresholdSnapshotsRetentionD 기본값=%d, want 90(현행 *_retention_days 관례)", def.ThresholdSnapshotsRetentionD)
	}
	if def.MeteringFreshnessWarnH != 336 {
		t.Errorf("MeteringFreshnessWarnH 기본값=%d, want 336(14일 — 이벤트 보존 기본과 정렬)", def.MeteringFreshnessWarnH)
	}
	th, err := store.ParseThresholds([][2]any{
		{"weekly_rollups_retention_days", "180"},
		{"threshold_snapshots_retention_days", "365"},
		{"metering_freshness_warn_h", "48"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.WeeklyRollupsRetentionD != 180 || th.ThresholdSnapshotsRetentionD != 365 || th.MeteringFreshnessWarnH != 48 {
		t.Errorf("오버라이드 반영 실패: rollup=%d snapshot=%d freshness=%d, want 180·365·48",
			th.WeeklyRollupsRetentionD, th.ThresholdSnapshotsRetentionD, th.MeteringFreshnessWarnH)
	}
	unset, err := store.ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.WeeklyRollupsRetentionD != 0 || unset.ThresholdSnapshotsRetentionD != 0 || unset.MeteringFreshnessWarnH != 0 {
		t.Errorf("unset 폴백: rollup=%d snapshot=%d freshness=%d, want 0·0·0",
			unset.WeeklyRollupsRetentionD, unset.ThresholdSnapshotsRetentionD, unset.MeteringFreshnessWarnH)
	}
}

// v13GateArtifacts — the schema v13 artifact assertions shared by the
// upgrade (v12→v13) and fresh-direct paths: the read_events.near_score
// column (double precision), the threshold_snapshots table + its
// (key, ts DESC) index, and the weekly_rollups table with the
// (week_start, metric) primary key enforced.
func v13GateArtifacts(t *testing.T, r *Repo, ctx context.Context) {
	t.Helper()
	// ① read_events.near_score — double precision (the scan-max score
	// storage; NOT NULL DEFAULT 0 — pre-v13 rows read 0).
	var dataType string
	if err := r.pool.QueryRow(ctx,
		`SELECT data_type FROM information_schema.columns
		 WHERE table_name = 'read_events' AND column_name = 'near_score'`).
		Scan(&dataType); err != nil {
		t.Fatalf("read_events.near_score 조회: %v", err)
	}
	if dataType != "double precision" {
		t.Fatalf("read_events.near_score 타입=%q, want double precision", dataType)
	}
	// ② threshold_snapshots + the (key, ts DESC) index.
	assertObject(t, r, ctx, "threshold_snapshots")
	var idx int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.statistics
		 WHERE table_name = 'threshold_snapshots' AND index_name = 'idx_threshold_snapshots_key_time'`).
		Scan(&idx); err != nil {
		t.Fatalf("threshold_snapshots 인덱스 조회: %v", err)
	}
	if idx < 1 {
		t.Fatalf("idx_threshold_snapshots_key_time 부재 — (key, ts DESC) 인덱스 계약 위반")
	}
	// ③ weekly_rollups — the composite PK enforced on duplicates.
	assertObject(t, r, ctx, "weekly_rollups")
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO weekly_rollups (week_start, metric, num, den)
		 VALUES (DATE '2026-11-16', 'zero_pct', 3, 9)`); err != nil {
		t.Fatalf("weekly_rollups 적립: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO weekly_rollups (week_start, metric, num, den)
		 VALUES (DATE '2026-11-16', 'zero_pct', 1, 1)`); err == nil {
		t.Fatal("weekly_rollups (week_start, metric) 중복 적립 허용 — PK 계약 위반")
	}
}

// TestV13GateUpgradeFromV12 — promotes a reproduced v12 DB to v13: the
// near_score column added with DEFAULT 0 (pre-promotion rows read 0)·
// the 2 metering tables + index created·version 13·the rerun
// idempotent (the additive protocol — existing columns unchanged).
func TestV13GateUpgradeFromV12(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// Reproduce a faithful v12 DB — remove the v13 artifacts the fresh
	// schema.sql terminus already carries (DROP COLUMN has no IF EXISTS
	// on Doltgres — the dropV11ScoreColumns convention).
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS threshold_snapshots`,
		`DROP TABLE IF EXISTS weekly_rollups`,
	} {
		if _, err := r.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("v12 재현 %q: %v", stmt, err)
		}
	}
	if columnExists(t, r, "read_events", "near_score") {
		if _, err := r.pool.Exec(ctx, `ALTER TABLE read_events DROP COLUMN near_score`); err != nil {
			t.Fatalf("v12 재현 near_score 삭제: %v", err)
		}
	}
	setVersion(t, r, "12")
	// A pre-promotion read_events row — the DEFAULT 0 backfill target.
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO read_events (hits, zero, near, fallback) VALUES (0, true, false, false)`); err != nil {
		t.Fatalf("v12 시대 read_events 적립: %v", err)
	}

	if err := r.EnsureSchemaV13(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV13: %v", err)
	}
	if v := readVersion(t, r); v != "13" {
		t.Fatalf("schema_version = %q, want 13", v)
	}
	v13GateArtifacts(t, r, ctx)
	// The pre-promotion row keeps the default 0 (additive DEFAULT ALTER).
	var nearScore float64
	if err := r.pool.QueryRow(ctx, `SELECT near_score FROM read_events`).
		Scan(&nearScore); err != nil {
		t.Fatalf("기존 read_events 행 near_score 조회: %v", err)
	}
	if nearScore != 0 {
		t.Fatalf("기존 행 near_score = %v, want 0(컬럼 기본값)", nearScore)
	}
	// The rerun is idempotent — version 13 kept·no error·no duplicate PK
	// rejection of the rerun's CREATE IF NOT EXISTS path.
	if err := r.EnsureSchemaV13(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV13 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "13" {
		t.Fatalf("재호출 후 schema_version = %q, want 13", v)
	}
}

// TestV13FreshSchemaDirect — the fresh-install path: recreating with the
// real schema.sql + running the gate chain v2→v13 in order reaches the
// v13 terminus (near_score·threshold_snapshots·weekly_rollups present
// without any gate DDL — the fresh-creation terminus contract).
func TestV13FreshSchemaDirect(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	saved := schemaSQLFn
	schemaSQLFn = sqlfs.SchemaSQL
	t.Cleanup(func() { schemaSQLFn = saved })
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP VIEW IF EXISTS active_knowledge, hot_view`,
		`DROP TABLE IF EXISTS read_events, session_starts, hot_events, knowledge_reads, recall_events, threshold_snapshots, weekly_rollups`,
		`DELETE FROM meta WHERE key = 'schema_version'`,
	} {
		if _, err := r.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("신규 DB 재현 %q: %v", stmt, err)
		}
	}
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(신규 생성): %v", err)
	}
	for _, gate := range SchemaGates() {
		if err := gate.Ensure(ctx, r); err != nil {
			t.Fatalf("게이트 체인 v%d: %v", gate.Version, err)
		}
	}
	if v := readVersion(t, r); v != "13" {
		t.Fatalf("게이트 체인 후 schema_version = %q, want 13", v)
	}
	v13GateArtifacts(t, r, ctx)
}
