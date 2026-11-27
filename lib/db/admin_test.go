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

func pendingRec(hash string) map[string]any {
	rec := knowledgeRec(hash)
	rec["status"] = "pending"
	return rec
}

func sessionAt(agent string, ts time.Time) map[string]any {
	return map[string]any{"kind": "session", "agent": agent, "summary": "세션", "ts": ts}
}

func slowApproveIdempotent(t *testing.T, r *Repo) {
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{pendingRec("appr-1")})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	id := ids[0]
	if err := r.Approve(ctx, id, ""); err != nil {
		t.Fatalf("first Approve: %v", err)
	}
	if err := r.Approve(ctx, id, ""); err != nil {
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

func slowApproveMissing(t *testing.T, r *Repo) {
	if err := r.Approve(context.Background(), 99999, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

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

	if _, err := r.Invalidate(ctx, []int{99999}, "사유"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for missing id, got %v", err)
	}
}

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
		knowledgeRec("cnt-solo"),
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

func slowStaleSessionRuns(t *testing.T, r *Repo) {
	ctx := context.Background()
	base := time.Now().Add(-10 * time.Hour)
	recs := []map[string]any{sessionAt("stale-a", base)}
	ids, err := r.InsertBatch(ctx, recs)
	if err != nil {
		t.Fatalf("InsertBatch base: %v", err)
	}
	kn := knowledgeRec("stale-k")
	kn["session_id"] = ids[0]
	if _, err := r.InsertBatch(ctx, []map[string]any{kn}); err != nil {
		t.Fatalf("InsertBatch knowledge: %v", err)
	}

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

func slowEnsureSchemaV3Idempotent(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	dropV3Indexes(t, r)
	ctx := context.Background()

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

func slowEnsureSchemaV2(t *testing.T, r *Repo) {
	resetVersionToV1(t, r)
	ctx := context.Background()

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

	if err := r.EnsureSchemaV2(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV2 (want idempotent): %v", err)
	}
}

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

func slowV11GateAdditive(t *testing.T, r *Repo) {
	ctx := context.Background()
	dropV11ScoreColumns(t, r)

	if _, err := r.pool.Exec(ctx, `DROP TABLE IF EXISTS recall_events`); err != nil {
		t.Fatalf("v10 재현 recall_events 삭제: %v", err)
	}
	setVersion(t, r, "10")

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

	if err := r.EnsureSchemaV11(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV11 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "11" {
		t.Fatalf("재호출 후 schema_version = %q, want 11", v)
	}
}

func slowV11GateNumeric(t *testing.T, r *Repo) {
	setVersion(t, r, "99")
	if err := r.EnsureSchemaV11(context.Background()); err != nil {
		t.Fatalf("EnsureSchemaV11(상위 버전 DB): %v", err)
	}
	if v := readVersion(t, r); v != "99" {
		t.Fatalf("게이트 하강 기록: schema_version=%q, want 99(수치 비교 no-op)", v)
	}
}

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

func v13GateArtifacts(t *testing.T, r *Repo, ctx context.Context) {
	t.Helper()

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

func TestV13GateUpgradeFromV12(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

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

	var nearScore float64
	if err := r.pool.QueryRow(ctx, `SELECT near_score FROM read_events`).
		Scan(&nearScore); err != nil {
		t.Fatalf("기존 read_events 행 near_score 조회: %v", err)
	}
	if nearScore != 0 {
		t.Fatalf("기존 행 near_score = %v, want 0(컬럼 기본값)", nearScore)
	}

	if err := r.EnsureSchemaV13(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV13 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "13" {
		t.Fatalf("재호출 후 schema_version = %q, want 13", v)
	}
}

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

func v14GateArtifacts(t *testing.T, r *Repo, ctx context.Context) {
	t.Helper()
	assertObject(t, r, ctx, "knowledge_links")
	assertObject(t, r, ctx, "idx_knowledge_links_from_id")
	assertObject(t, r, ctx, "idx_knowledge_links_to_id")
	assertObject(t, r, ctx, "idx_knowledge_superseded_by")

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

	if _, err := r.pool.Exec(ctx,
		`INSERT INTO knowledge_links (from_id, to_id) VALUES ($1, $2)`, ids[0], ids[1]); err == nil {
		t.Fatal("relation NULL 적립 허용 — NOT NULL 계약 위반")
	}
}

func TestV14GateUpgradeFromV13(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

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

	if err := r.EnsureSchemaV14(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV14 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "14" {
		t.Fatalf("재호출 후 schema_version = %q, want 14", v)
	}
}

func TestV14FreshSchemaDirect(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	saved := schemaSQLFn
	schemaSQLFn = sqlfs.SchemaSQL
	t.Cleanup(func() { schemaSQLFn = saved })
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP VIEW IF EXISTS active_knowledge, hot_view`,
		`DROP TABLE IF EXISTS read_events, session_starts, hot_events, knowledge_reads, recall_events, threshold_snapshots, weekly_rollups, knowledge_links, vault_files, tuning_predictions, echo_candidates`,
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
	if v := readVersion(t, r); v != "20" {
		t.Fatalf("게이트 체인 후 schema_version = %q, want 20 (v1.38.0 상향)", v)
	}
	v14GateArtifacts(t, r, ctx)

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

	v16GateArtifacts(t, r, ctx)

	v17GateArtifacts(t, r, ctx)

	v18GateArtifacts(t, r, ctx)

	v19GateArtifacts(t, r, ctx)
}

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

func v16GateArtifacts(t *testing.T, r *Repo, ctx context.Context) {
	t.Helper()
	assertObject(t, r, ctx, "vault_files")

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

	if _, err := r.pool.Exec(ctx,
		`INSERT INTO vault_files (path, title, mtime, size, content_hash)
		 VALUES ('demo/notes/first.md', 'Demo Dup', $1, 64, 'sha256-v16-b')`, mtime); err == nil {
		t.Fatal("vault_files 중복 path INSERT 허용 — UNIQUE 계약 위반")
	}
}

func v17GateArtifacts(t *testing.T, r *Repo, ctx context.Context) {
	t.Helper()

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

func TestV17GateUpgradeFromV16(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

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

	if err := r.EnsureSchemaV17(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV17 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "17" {
		t.Fatalf("재호출 후 schema_version = %q, want 17", v)
	}
}

func v18GateArtifacts(t *testing.T, r *Repo, ctx context.Context) {
	t.Helper()
	assertObject(t, r, ctx, "tuning_predictions")

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

func TestV18GateUpgradeFromV17(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

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

	if err := r.EnsureSchemaV18(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV18 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "18" {
		t.Fatalf("재호출 후 schema_version = %q, want 18", v)
	}
}

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

func v19GateArtifacts(t *testing.T, r *Repo, ctx context.Context) {
	t.Helper()
	assertObject(t, r, ctx, "echo_candidates")

	for _, idxName := range []string{"idx_echo_candidates_candidate_id", "idx_echo_candidates_ts"} {
		var idx int
		if err := r.pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_indexes
			 WHERE tablename = 'echo_candidates' AND indexname = $1`, idxName).
			Scan(&idx); err != nil {
			t.Fatalf("echo_candidates 인덱스 조회 %s: %v", idxName, err)
		}
		if idx < 1 {
			t.Fatalf("%s 부재 — v19 인덱스 계약 위반", idxName)
		}
	}

	for _, col := range []string{"ts", "candidate_id", "echo_id", "relation", "score"} {
		var nullable string
		if err := r.pool.QueryRow(ctx,
			`SELECT is_nullable FROM information_schema.columns
			 WHERE table_name = 'echo_candidates' AND column_name = $1`, col).
			Scan(&nullable); err != nil {
			t.Fatalf("echo_candidates.%s 조회: %v", col, err)
		}
		if nullable != "NO" {
			t.Fatalf("echo_candidates.%s is_nullable=%q, want NO", col, nullable)
		}
	}

	if _, err := r.pool.Exec(ctx,
		`INSERT INTO echo_candidates (candidate_id, echo_id, relation, score)
		 VALUES (11, 7, 'echo', 0.55)`); err != nil {
		t.Fatalf("echo_candidates 적립: %v", err)
	}
	var ts time.Time
	var relation string
	var score float64
	if err := r.pool.QueryRow(ctx,
		`SELECT ts, relation, score FROM echo_candidates
		 WHERE candidate_id = 11 AND echo_id = 7`).
		Scan(&ts, &relation, &score); err != nil {
		t.Fatalf("echo_candidates 조회: %v", err)
	}
	if ts.IsZero() || relation != "echo" || score != 0.55 {
		t.Fatalf("echo_candidates 행 = (%v,%q,%v), want DEFAULT ts·적립값 그대로", ts, relation, score)
	}

	for _, col := range []string{"kind", "source_path"} {
		var nullable string
		if err := r.pool.QueryRow(ctx,
			`SELECT is_nullable FROM information_schema.columns
			 WHERE table_name = 'knowledge' AND column_name = $1`, col).
			Scan(&nullable); err != nil {
			t.Fatalf("knowledge.%s 조회: %v", col, err)
		}
		if nullable != "YES" {
			t.Fatalf("knowledge.%s is_nullable=%q, want YES", col, nullable)
		}
	}
}

func TestV19GateUpgradeFromV18(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	if _, err := r.pool.Exec(ctx, `DROP TABLE IF EXISTS echo_candidates`); err != nil {
		t.Fatalf("v18 재현 echo_candidates 삭제: %v", err)
	}
	for _, col := range []string{"kind", "source_path"} {
		if columnExists(t, r, "knowledge", col) {
			if _, err := r.pool.Exec(ctx, `ALTER TABLE knowledge DROP COLUMN `+col); err != nil {
				t.Fatalf("v18 재현 knowledge.%s 삭제: %v", col, err)
			}
		}
	}
	setVersion(t, r, "18")

	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v19-pre")})
	if err != nil {
		t.Fatalf("v18 시대 knowledge 적립: %v", err)
	}

	if err := r.EnsureSchemaV19(ctx); err != nil {
		t.Fatalf("first EnsureSchemaV19: %v", err)
	}
	if v := readVersion(t, r); v != "19" {
		t.Fatalf("schema_version = %q, want 19", v)
	}
	v19GateArtifacts(t, r, ctx)

	var kind, sourcePath *string
	if err := r.pool.QueryRow(ctx,
		`SELECT kind, source_path FROM knowledge WHERE id = $1`, ids[0]).
		Scan(&kind, &sourcePath); err != nil {
		t.Fatalf("기존 knowledge 행 v19 열 조회: %v", err)
	}
	if kind != nil || sourcePath != nil {
		t.Fatalf("기존 knowledge 행 kind=%v source_path=%v, want NULL·NULL", kind, sourcePath)
	}

	if err := r.EnsureSchemaV19(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV19 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "19" {
		t.Fatalf("재호출 후 schema_version = %q, want 19", v)
	}
}

func TestAuditStageTablesV19(t *testing.T) {
	found := false
	for _, cur := range auditStageTables {
		if cur == "echo_candidates" {
			found = true
			break
		}
	}
	if !found {
		t.Error("auditStageTables에 echo_candidates 부재 — v19 감사 테이블 미스테이징")
	}
	for _, cur := range telemetryStageTables {
		if cur == "echo_candidates" {
			t.Error("echo_candidates가 telemetry 스테이징에 포함 — 감사 테이블 아님")
		}
	}
	for _, cur := range telemetryStageSeqs {
		if cur == "echo_candidates_id_seq" {
			t.Error("echo_candidates_id_seq가 telemetry 스테이징에 포함 — 잔류-무해 룰링 위반")
		}
	}
}

func TestV16GateUpgradeFromV15(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

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

	if err := r.EnsureSchemaV16(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV16 (want idempotent): %v", err)
	}
	if v := readVersion(t, r); v != "16" {
		t.Fatalf("재호출 후 schema_version = %q, want 16", v)
	}
}
