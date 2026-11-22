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
	// The registry now extends past 13 (schema v14) — this test pins the
	// v13-era terminus, so the walk stops at 13 (the fresh v13 assertion
	// would otherwise see the v14 ceiling).
	for _, gate := range SchemaGates() {
		if gate.Version > 13 {
			break
		}
		if err := gate.Ensure(ctx, r); err != nil {
			t.Fatalf("게이트 체인 v%d: %v", gate.Version, err)
		}
	}
	if v := readVersion(t, r); v != "13" {
		t.Fatalf("게이트 체인 후 schema_version = %q, want 13", v)
	}
	v13GateArtifacts(t, r, ctx)
}

// TestAuditStageTablesV14 — knowledge_links joins the audit staging list
// (audit data, not metering — links are audit-visible knowledge lineage
// metadata; the recall_events ruling precedent). Table-only ruling: the
// bigserial's knowledge_links_id_seq stays unstaged everywhere — the
// measured *_id_seq working-set residue is display-only·harmless (the
// auditStageTables sequence ruling).
func TestAuditStageTablesV14(t *testing.T) {
	found := false
	for _, cur := range auditStageTables {
		if cur == "knowledge_links" {
			found = true
			break
		}
	}
	if !found {
		t.Error("auditStageTables에 knowledge_links 부재 — v14 감사 테이블 미스테이징")
	}
	for _, cur := range telemetryStageTables {
		if cur == "knowledge_links" {
			t.Error("knowledge_links가 telemetry 스테이징에 포함 — 계측 테이블 아님")
		}
	}
	for _, cur := range telemetryStageSeqs {
		if cur == "knowledge_links_id_seq" {
			t.Error("knowledge_links_id_seq가 telemetry 스테이징에 포함 — 잔류-무해 룰링 위반")
		}
	}
}

// v14GateArtifacts — the schema v14 artifact assertions shared by the
// upgrade (v13→v14) and fresh-direct paths: the knowledge_links table
// (from_id·to_id·relation NOT NULL·created_at default now) with its
// (from_id)·(to_id) indexes and the knowledge (superseded_by) index; a
// functional link insert between 2 knowledge rows reads back, and
// relation NOT NULL is enforced (no FK — from/to existence is the write
// path's job, the existing knowledge convention).
func v14GateArtifacts(t *testing.T, r *Repo, ctx context.Context) {
	t.Helper()
	assertObject(t, r, ctx, "knowledge_links")
	assertObject(t, r, ctx, "idx_knowledge_links_from_id")
	assertObject(t, r, ctx, "idx_knowledge_links_to_id")
	assertObject(t, r, ctx, "idx_knowledge_superseded_by")
	// NOT NULL contract of the 3 payload columns.
	for _, col := range []string{"from_id", "to_id", "relation"} {
		var nullable string
		if err := r.pool.QueryRow(ctx,
			`SELECT is_nullable FROM information_schema.columns
			 WHERE table_name = 'knowledge_links' AND column_name = $1`, col).
			Scan(&nullable); err != nil {
			t.Fatalf("knowledge_links.%s 조회: %v", col, err)
		}
		if nullable != "NO" {
			t.Fatalf("knowledge_links.%s is_nullable=%q, want NO", col, nullable)
		}
	}
	// Functional spot — a link between 2 knowledge rows reads back.
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("v14-a"), knowledgeRec("v14-b"),
	})
	if err != nil {
		t.Fatalf("InsertBatch pre-v14 spot: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO knowledge_links (from_id, to_id, relation) VALUES ($1, $2, 'refines')`,
		ids[0], ids[1]); err != nil {
		t.Fatalf("knowledge_links 적립: %v", err)
	}
	var rel string
	var createdAt time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT relation, created_at FROM knowledge_links WHERE from_id = $1`, ids[0]).
		Scan(&rel, &createdAt); err != nil {
		t.Fatalf("knowledge_links 조회: %v", err)
	}
	if rel != "refines" || createdAt.IsZero() {
		t.Fatalf("링크 행 = (%q,%v), want (refines, created_at DEFAULT now)", rel, createdAt)
	}
	// relation NOT NULL enforced.
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO knowledge_links (from_id, to_id) VALUES ($1, $2)`, ids[0], ids[1]); err == nil {
		t.Fatal("relation NULL 적립 허용 — NOT NULL 계약 위반")
	}
}

// TestV14GateUpgradeFromV13 — promotes a reproduced v13 DB to v14: the
// knowledge_links table + the 2 link indexes + the knowledge
// (superseded_by) index created·version 14·the rerun idempotent (the
// additive protocol — existing tables·data unchanged).
func TestV14GateUpgradeFromV13(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// Reproduce a faithful v13 DB — remove the v14 artifacts the fresh
	// schema.sql terminus already carries.
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS knowledge_links`,
		`DROP INDEX IF EXISTS idx_knowledge_superseded_by`,
	} {
		if _, err := r.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("v13 재현 %q: %v", stmt, err)
		}
	}
	setVersion(t, r, "13")

	if err := r.EnsureSchemaV14(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV14: %v", err)
	}
	if v := readVersion(t, r); v != "14" {
		t.Fatalf("schema_version = %q, want 14", v)
	}
	v14GateArtifacts(t, r, ctx)
	// The rerun is idempotent — version 14 kept·no error.
	if err := r.EnsureSchemaV14(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV14 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "14" {
		t.Fatalf("재호출 후 schema_version = %q, want 14", v)
	}
}

// TestV14FreshSchemaDirect — the fresh-install path: recreating with the
// real schema.sql + running the gate chain v2→LatestSchemaVersion in
// order reaches the latest terminus (knowledge_links + its indexes + the
// superseded_by index + the v15 unique triple seal + the v16 vault_files
// table + the v17 staleness/distill columns present without any gate
// DDL — the fresh-creation terminus contract).
func TestV14FreshSchemaDirect(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	saved := schemaSQLFn
	schemaSQLFn = sqlfs.SchemaSQL
	t.Cleanup(func() { schemaSQLFn = saved })
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP VIEW IF EXISTS active_knowledge, hot_view`,
		`DROP TABLE IF EXISTS read_events, session_starts, hot_events, knowledge_reads, recall_events, threshold_snapshots, weekly_rollups, knowledge_links, vault_files, tuning_predictions`,
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
	if v := readVersion(t, r); v != "18" {
		t.Fatalf("게이트 체인 후 schema_version = %q, want 18 (v1.23.0 상향)", v)
	}
	v14GateArtifacts(t, r, ctx)
	// v1.20.1: the fresh terminus carries the unique triple seal — a
	// duplicate (from,to,relation) INSERT is refused.
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("v1201-fresh-seal-a"), knowledgeRec("v1201-fresh-seal-b"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO knowledge_links (from_id, to_id, relation) VALUES ($1, $2, 'refines')`,
		ids[0], ids[1]); err != nil {
		t.Fatalf("fresh seal 링크 적립: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO knowledge_links (from_id, to_id, relation) VALUES ($1, $2, 'refines')`,
		ids[0], ids[1]); err == nil {
		t.Fatal("fresh seal 중복 triple INSERT 허용 — 유니크 봉인 부재")
	}
	// v1.21.0: the fresh terminus carries vault_files — the v16 gate's
	// artifacts (table·NOT NULL columns·defaults·UNIQUE path·(content_hash)
	// index) present without any gate DDL.
	v16GateArtifacts(t, r, ctx)
	// v1.22.0: the fresh terminus carries the v17 staleness/distill
	// columns without any gate DDL.
	v17GateArtifacts(t, r, ctx)
	// v1.23.0: the fresh terminus carries tuning_predictions + the 3
	// threshold_snapshots columns without any gate DDL.
	v18GateArtifacts(t, r, ctx)
}

// TestAuditStageTablesV16 — vault_files joins the audit staging list
// (audit data, not metering — the vault index is a recall-domain source
// snapshot, the recall_events ruling precedent). Table-only ruling: the
// bigserial's vault_files_id_seq stays unstaged everywhere (the
// auditStageTables sequence ruling — display-only·harmless residue).
func TestAuditStageTablesV16(t *testing.T) {
	found := false
	for _, cur := range auditStageTables {
		if cur == "vault_files" {
			found = true
			break
		}
	}
	if !found {
		t.Error("auditStageTables에 vault_files 부재 — v16 감사 테이블 미스테이징")
	}
	for _, cur := range telemetryStageTables {
		if cur == "vault_files" {
			t.Error("vault_files가 telemetry 스테이징에 포함 — 계측 테이블 아님")
		}
	}
	for _, cur := range telemetryStageSeqs {
		if cur == "vault_files_id_seq" {
			t.Error("vault_files_id_seq가 telemetry 스테이징에 포함 — 잔류-무해 룰링 위반")
		}
	}
}

// v16GateArtifacts — the schema v16 artifact assertions shared by the
// upgrade (v15→v16) and fresh-direct paths: the vault_files table (all
// 8 declared columns NOT NULL — tags·summary·last_indexed_at via
// DEFAULT), the (content_hash) index, the path UNIQUE constraint
// enforced, and a functional pointer row reading back with the DEFAULTs
// surfacing (tags ”·summary ”·last_indexed_at now).
func v16GateArtifacts(t *testing.T, r *Repo, ctx context.Context) {
	t.Helper()
	assertObject(t, r, ctx, "vault_files")
	// the (content_hash) index — the duplicate-note detection axis.
	var idx int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_indexes
		 WHERE tablename = 'vault_files' AND indexname = 'idx_vault_files_content_hash'`).
		Scan(&idx); err != nil {
		t.Fatalf("vault_files 인덱스 조회: %v", err)
	}
	if idx < 1 {
		t.Fatalf("idx_vault_files_content_hash 부재 — (content_hash) 인덱스 계약 위반")
	}
	// every declared column is NOT NULL (the three DEFAULT columns carry
	// their values, not NULLability).
	for _, col := range []string{"path", "title", "tags", "mtime", "size", "content_hash", "summary", "last_indexed_at"} {
		var nullable string
		if err := r.pool.QueryRow(ctx,
			`SELECT is_nullable FROM information_schema.columns
			 WHERE table_name = 'vault_files' AND column_name = $1`, col).
			Scan(&nullable); err != nil {
			t.Fatalf("vault_files.%s 조회: %v", col, err)
		}
		if nullable != "NO" {
			t.Fatalf("vault_files.%s is_nullable=%q, want NO", col, nullable)
		}
	}
	// Functional spot — a synthetic demo pointer row with the DEFAULTs
	// surfacing (no personal vault paths — the synthetic-fixture rule).
	mtime := time.Date(2026, 11, 23, 1, 2, 3, 0, time.UTC)
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO vault_files (path, title, mtime, size, content_hash)
		 VALUES ('demo/notes/first.md', 'Demo First', $1, 128, 'sha256-v16-a')`, mtime); err != nil {
		t.Fatalf("vault_files 적립: %v", err)
	}
	var tags, summary, contentHash string
	var lastIndexed time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT tags, summary, content_hash, last_indexed_at FROM vault_files
		 WHERE path = 'demo/notes/first.md'`).
		Scan(&tags, &summary, &contentHash, &lastIndexed); err != nil {
		t.Fatalf("vault_files 조회: %v", err)
	}
	if tags != "" || summary != "" || contentHash != "sha256-v16-a" || lastIndexed.IsZero() {
		t.Fatalf("vault_files 행 = (%q,%q,%q,%v), want DEFAULT ''·''·now 계약",
			tags, summary, contentHash, lastIndexed)
	}
	// path UNIQUE enforced — the scan-diff UPSERT axis.
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO vault_files (path, title, mtime, size, content_hash)
		 VALUES ('demo/notes/first.md', 'Demo Dup', $1, 64, 'sha256-v16-b')`, mtime); err == nil {
		t.Fatal("vault_files 중복 path INSERT 허용 — UNIQUE 계약 위반")
	}
}

// v17GateArtifacts — the schema v17 artifact assertions shared by the
// upgrade (v16→v17) and fresh-direct paths: the 3 additive columns —
// vault_files.content_changed_at (timestamptz NULL), knowledge.distilled_at
// (timestamptz NULL), knowledge.conditions (text NOT NULL DEFAULT ”).
func v17GateArtifacts(t *testing.T, r *Repo, ctx context.Context) {
	t.Helper()
	// column presence + nullability (the two timestamp columns are NULL —
	// the staleness recheck queue·distill idempotence stamps; conditions
	// carries NOT NULL DEFAULT '' — the v11/v13 DEFAULT-ALTER precedent).
	for _, tc := range []struct{ table, col, wantNullable string }{
		{"vault_files", "content_changed_at", "YES"},
		{"knowledge", "distilled_at", "YES"},
		{"knowledge", "conditions", "NO"},
	} {
		var nullable string
		if err := r.pool.QueryRow(ctx,
			`SELECT is_nullable FROM information_schema.columns
			 WHERE table_name = $1 AND column_name = $2`, tc.table, tc.col).
			Scan(&nullable); err != nil {
			t.Fatalf("%s.%s 조회: %v", tc.table, tc.col, err)
		}
		if nullable != tc.wantNullable {
			t.Fatalf("%s.%s is_nullable=%q, want %q", tc.table, tc.col, nullable, tc.wantNullable)
		}
	}
}

// TestV17GateUpgradeFromV16 — promotes a reproduced v16 DB to v17: the 3
// additive columns added (the two timestamps NULL-able, conditions with
// DEFAULT ” so pre-promotion rows read the empty string)·version 17·the
// rerun idempotent (the additive protocol — existing tables·data unchanged).
func TestV17GateUpgradeFromV16(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// Reproduce a faithful v16 DB — remove the v17 artifacts the fresh
	// schema.sql terminus already carries (DROP COLUMN has no IF EXISTS
	// on Doltgres — the columnExists guard convention).
	if columnExists(t, r, "vault_files", "content_changed_at") {
		if _, err := r.pool.Exec(ctx, `ALTER TABLE vault_files DROP COLUMN content_changed_at`); err != nil {
			t.Fatalf("v16 재현 content_changed_at 삭제: %v", err)
		}
	}
	for _, col := range []string{"distilled_at", "conditions"} {
		if columnExists(t, r, "knowledge", col) {
			if _, err := r.pool.Exec(ctx, `ALTER TABLE knowledge DROP COLUMN `+col); err != nil {
				t.Fatalf("v16 재현 knowledge.%s 삭제: %v", col, err)
			}
		}
	}
	setVersion(t, r, "16")
	// Pre-promotion rows — the DEFAULT/NULL backfill targets (synthetic
	// demo fixtures only).
	mtime := time.Date(2026, 11, 23, 4, 5, 6, 0, time.UTC)
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO vault_files (path, title, mtime, size, content_hash)
		 VALUES ('demo/notes/stale-demo.md', 'Demo Stale', $1, 256, 'sha256-v17-a')`, mtime); err != nil {
		t.Fatalf("v16 시대 vault_files 적립: %v", err)
	}
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v17-pre")})
	if err != nil {
		t.Fatalf("v16 시대 knowledge 적립: %v", err)
	}

	if err := r.EnsureSchemaV17(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV17: %v", err)
	}
	if v := readVersion(t, r); v != "17" {
		t.Fatalf("schema_version = %q, want 17", v)
	}
	v17GateArtifacts(t, r, ctx)
	// The pre-promotion rows read the column defaults — content_changed_at
	// and distilled_at NULL (no change·no distill yet), conditions ''.
	var changedAt, distilledAt *time.Time
	var conditions string
	if err := r.pool.QueryRow(ctx,
		`SELECT content_changed_at FROM vault_files WHERE path = 'demo/notes/stale-demo.md'`).
		Scan(&changedAt); err != nil {
		t.Fatalf("기존 vault_files 행 content_changed_at 조회: %v", err)
	}
	if changedAt != nil {
		t.Fatalf("기존 vault_files 행 content_changed_at = %v, want NULL", changedAt)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT distilled_at, conditions FROM knowledge WHERE id = $1`, ids[0]).
		Scan(&distilledAt, &conditions); err != nil {
		t.Fatalf("기존 knowledge 행 v17 열 조회: %v", err)
	}
	if distilledAt != nil || conditions != "" {
		t.Fatalf("기존 knowledge 행 distilled_at=%v conditions=%q, want NULL·\"\"", distilledAt, conditions)
	}
	// The rerun is idempotent — version 17 kept·no error.
	if err := r.EnsureSchemaV17(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV17 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "17" {
		t.Fatalf("재호출 후 schema_version = %q, want 17", v)
	}
}

// v18GateArtifacts — the schema v18 artifact assertions shared by the
// upgrade (v17→v18) and fresh-direct paths: the tuning_predictions table
// (all 9 non-id columns NOT NULL — ts via DEFAULT now(), not NULLability)
// with its (ts) index, and the 3 nullable threshold_snapshots columns
// (origin·trigger·rationale — the snapshot.go "deferred to v1.23.0"
// delivery).
func v18GateArtifacts(t *testing.T, r *Repo, ctx context.Context) {
	t.Helper()
	assertObject(t, r, ctx, "tuning_predictions")
	// the (ts) index — the shadow-round time-window scan axis.
	var idx int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_indexes
		 WHERE tablename = 'tuning_predictions' AND indexname = 'idx_tuning_predictions_ts'`).
		Scan(&idx); err != nil {
		t.Fatalf("tuning_predictions 인덱스 조회: %v", err)
	}
	if idx < 1 {
		t.Fatalf("idx_tuning_predictions_ts 부재 — (ts) 인덱스 계약 위반")
	}
	// every declared column except the bigserial id PK is NOT NULL.
	for _, col := range []string{"ts", "trigger", "key", "cur", "new",
		"pred_zero_near_pct", "pred_reclass_n", "sample_n", "rationale"} {
		var nullable string
		if err := r.pool.QueryRow(ctx,
			`SELECT is_nullable FROM information_schema.columns
			 WHERE table_name = 'tuning_predictions' AND column_name = $1`, col).
			Scan(&nullable); err != nil {
			t.Fatalf("tuning_predictions.%s 조회: %v", col, err)
		}
		if nullable != "NO" {
			t.Fatalf("tuning_predictions.%s is_nullable=%q, want NO", col, nullable)
		}
	}
	// Functional spot — a synthetic demo prediction row reading back with
	// the DEFAULT ts surfacing (the shadow circuit's own rows are numbers
	// and a composed rationale only — no personal data).
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO tuning_predictions
		 (trigger, key, cur, new, pred_zero_near_pct, pred_reclass_n, sample_n, rationale)
		 VALUES ('auto_zero_near_high', 'suggest_min_score', '0.15', '0.12',
		         4.2, 3, 40, 'demo shadow prediction rationale')`); err != nil {
		t.Fatalf("tuning_predictions 적립: %v", err)
	}
	var ts time.Time
	var trigger, key, cur, newVal, rationale string
	var predPct float64
	var reclassN, sampleN int
	if err := r.pool.QueryRow(ctx,
		`SELECT ts, trigger, key, cur, new, pred_zero_near_pct, pred_reclass_n, sample_n, rationale
		 FROM tuning_predictions WHERE key = 'suggest_min_score'`).
		Scan(&ts, &trigger, &key, &cur, &newVal, &predPct, &reclassN, &sampleN, &rationale); err != nil {
		t.Fatalf("tuning_predictions 조회: %v", err)
	}
	if ts.IsZero() || trigger != "auto_zero_near_high" || key != "suggest_min_score" ||
		cur != "0.15" || newVal != "0.12" || predPct != 4.2 || reclassN != 3 || sampleN != 40 ||
		rationale != "demo shadow prediction rationale" {
		t.Fatalf("tuning_predictions 행 = (%v,%q,%q,%q,%q,%v,%d,%d,%q), want DEFAULT ts·적립값 그대로",
			ts, trigger, key, cur, newVal, predPct, reclassN, sampleN, rationale)
	}
	// the 3 v18 threshold_snapshots columns are NULL-able (v1.23.0 has 0
	// applies — no origin=auto rows yet; schema·stamp paths only).
	for _, col := range []string{"origin", "trigger", "rationale"} {
		var nullable string
		if err := r.pool.QueryRow(ctx,
			`SELECT is_nullable FROM information_schema.columns
			 WHERE table_name = 'threshold_snapshots' AND column_name = $1`, col).
			Scan(&nullable); err != nil {
			t.Fatalf("threshold_snapshots.%s 조회: %v", col, err)
		}
		if nullable != "YES" {
			t.Fatalf("threshold_snapshots.%s is_nullable=%q, want YES", col, nullable)
		}
	}
}

// TestV18GateUpgradeFromV17 — promotes a reproduced v17 DB to v18: the
// tuning_predictions table + its (ts) index created·the 3
// threshold_snapshots columns added (NULL-able — pre-promotion rows read
// NULL)·version 18·the rerun idempotent (the additive protocol — existing
// tables·data unchanged).
func TestV18GateUpgradeFromV17(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// Reproduce a faithful v17 DB — remove the v18 artifacts the fresh
	// schema.sql terminus already carries (DROP COLUMN has no IF EXISTS
	// on Doltgres — the columnExists guard convention).
	if _, err := r.pool.Exec(ctx, `DROP TABLE IF EXISTS tuning_predictions`); err != nil {
		t.Fatalf("v17 재현 tuning_predictions 삭제: %v", err)
	}
	for _, col := range []string{"origin", "trigger", "rationale"} {
		if columnExists(t, r, "threshold_snapshots", col) {
			if _, err := r.pool.Exec(ctx,
				`ALTER TABLE threshold_snapshots DROP COLUMN `+col); err != nil {
				t.Fatalf("v17 재현 threshold_snapshots.%s 삭제: %v", col, err)
			}
		}
	}
	setVersion(t, r, "17")
	// Pre-promotion snapshot row — the NULL backfill target (synthetic
	// demo fixture only).
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO threshold_snapshots (key, old_value, new_value)
		 VALUES ('suggest_min_score', '0.2', '0.15')`); err != nil {
		t.Fatalf("v17 시대 threshold_snapshots 적립: %v", err)
	}

	if err := r.EnsureSchemaV18(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV18: %v", err)
	}
	if v := readVersion(t, r); v != "18" {
		t.Fatalf("schema_version = %q, want 18", v)
	}
	v18GateArtifacts(t, r, ctx)
	// The pre-promotion snapshot row reads NULL on the 3 new columns —
	// the v1.23.0 additive premise (0 applies — no origin=auto stamps).
	var origin, trigger, rationale *string
	if err := r.pool.QueryRow(ctx,
		`SELECT origin, trigger, rationale FROM threshold_snapshots
		 WHERE key = 'suggest_min_score'`).
		Scan(&origin, &trigger, &rationale); err != nil {
		t.Fatalf("기존 threshold_snapshots 행 v18 열 조회: %v", err)
	}
	if origin != nil || trigger != nil || rationale != nil {
		t.Fatalf("기존 threshold_snapshots 행 origin=%v trigger=%v rationale=%v, want NULL×3",
			origin, trigger, rationale)
	}
	// The rerun is idempotent — version 18 kept·no error.
	if err := r.EnsureSchemaV18(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV18 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "18" {
		t.Fatalf("재호출 후 schema_version = %q, want 18", v)
	}
}

// TestAuditStageTablesV18 — tuning_predictions joins the audit staging
// list (shadow predictions are audit data — the recall_events ruling
// precedent: they must propagate across devices via push/pull). Table-only
// ruling: the bigserial's tuning_predictions_id_seq stays unstaged
// everywhere (the auditStageTables sequence ruling — display-only·
// harmless residue).
func TestAuditStageTablesV18(t *testing.T) {
	found := false
	for _, cur := range auditStageTables {
		if cur == "tuning_predictions" {
			found = true
			break
		}
	}
	if !found {
		t.Error("auditStageTables에 tuning_predictions 부재 — v18 감사 테이블 미스테이징")
	}
	for _, cur := range telemetryStageTables {
		if cur == "tuning_predictions" {
			t.Error("tuning_predictions가 telemetry 스테이징에 포함 — 감사 테이블 아님")
		}
	}
	for _, cur := range telemetryStageSeqs {
		if cur == "tuning_predictions_id_seq" {
			t.Error("tuning_predictions_id_seq가 telemetry 스테이징에 포함 — 잔류-무해 룰링 위반")
		}
	}
}

// TestV16GateUpgradeFromV15 — promotes a reproduced v15 DB to v16: the
// vault_files table + its (content_hash) index created·version 16·the
// rerun idempotent (the additive protocol — existing tables·data
// unchanged).
func TestV16GateUpgradeFromV15(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// Reproduce a faithful v15 DB — remove the v16 artifacts the fresh
	// schema.sql terminus already carries (dropping the table removes its
	// indexes too).
	if _, err := r.pool.Exec(ctx, `DROP TABLE IF EXISTS vault_files`); err != nil {
		t.Fatalf("v15 재현 vault_files 삭제: %v", err)
	}
	setVersion(t, r, "15")

	if err := r.EnsureSchemaV16(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV16: %v", err)
	}
	if v := readVersion(t, r); v != "16" {
		t.Fatalf("schema_version = %q, want 16", v)
	}
	v16GateArtifacts(t, r, ctx)
	// The rerun is idempotent — version 16 kept·no error.
	if err := r.EnsureSchemaV16(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV16 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "16" {
		t.Fatalf("재호출 후 schema_version = %q, want 16", v)
	}
}
