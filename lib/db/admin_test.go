// admin_test.go — T17 갱신·집계(승인·무효화·병합·카운트·연속 미기록) slow 테스트.
package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// pendingRec은 status=pending knowledge 레코드(검수 게이트 통과 전 상태).
func pendingRec(hash string) map[string]any {
	rec := knowledgeRec(hash)
	rec["status"] = "pending"
	return rec
}

// sessionAt은 ts를 명시한 세션 레코드(StaleSessionRuns 순서 검증용 —
// Doltgres now()가 트랜잭션 시각이라 자동값으론 순서가 섞인다).
func sessionAt(agent string, ts time.Time) map[string]any {
	return map[string]any{"kind": "session", "agent": agent, "summary": "세션", "ts": ts}
}

// TestApproveIdempotent — Review Focus(state): 2회 approve → 동일 상태·2번째도 에러 없음.
func TestApproveIdempotent(t *testing.T) {
	r := newTestRepo(t)
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

// TestApproveMissing — 없는 id는 ErrNotFound.
func TestApproveMissing(t *testing.T) {
	r := newTestRepo(t)
	if err := r.Approve(context.Background(), 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestInvalidateSetsInvalidAt — Review Focus 지정 케이스: invalid_at NULL→NOT NULL.
func TestInvalidateSetsInvalidAt(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("inv-1")})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := r.Invalidate(ctx, ids[0], "중복 의심 정리"); err != nil {
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
	// 없는 id는 ErrNotFound.
	if err := r.Invalidate(ctx, 99999, "사유"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for missing id, got %v", err)
	}
}

// TestMergeMarksAndInvalidates — from 무효화 + to summary에 병합 표시.
func TestMergeMarksAndInvalidates(t *testing.T) {
	r := newTestRepo(t)
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
	// 없는 id는 ErrNotFound.
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

// TestCounts — pending 수 + 중복 의심(같은 topic에 active 2건 이상) 수.
func TestCounts(t *testing.T) {
	r := newTestRepo(t)
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
		knowledgeRec("cnt-solo"), // 유일 topic — 중복 의심 아님
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

// TestStaleSessionRuns — 최근 세션부터 knowledge write 없는 연속 수.
// 기록 있는 세션을 만나면 집계가 멈춰야 한다(전체 미기록 수가 아님).
func TestStaleSessionRuns(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	base := time.Now().Add(-10 * time.Hour)
	recs := []map[string]any{sessionAt("stale-a", base)} // 가장 오래: 기록 있음
	ids, err := r.InsertBatch(ctx, recs)
	if err != nil {
		t.Fatalf("InsertBatch base: %v", err)
	}
	kn := knowledgeRec("stale-k")
	kn["session_id"] = ids[0]
	if _, err := r.InsertBatch(ctx, []map[string]any{kn}); err != nil {
		t.Fatalf("InsertBatch knowledge: %v", err)
	}
	// 이후 3세션은 미기록 → 연속 3.
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
	// 최근 세션에 기록이 있으면 0.
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

// resetVersionToV1은 EnsureSchemaV2 테스트가 meta='2'를 남기지 않게
// 되돌린다(다른 테스트의 newTestRepo가 영향받지 않도록).
func resetVersionToV1(t *testing.T, r *Repo) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(),
			`UPDATE meta SET value = '1' WHERE key = 'schema_version'`)
	})
}

// TestMigrateAcceptsV2 — 회귀(코디네이터 룰링): meta='2' 상태에서 Migrate는
// err=nil로 통과하고 버전을 '1'로 되돌리지 않는다. v0.1 게이트가 '1'만
// 상정해 nil wrap 실패("%!w(<nil>)")를 냈다.
func TestMigrateAcceptsV2(t *testing.T) {
	r := newTestRepo(t)
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

// dropV3Indexes는 EnsureSchemaV3 테스트가 flint 인덱스를 남기지 않게
// 되돌린다(다른 테스트의 newTestRepo가 영향받지 않도록).
func dropV3Indexes(t *testing.T, r *Repo) {
	t.Helper()
	t.Cleanup(func() {
		for _, name := range []string{
			"flint_knowledge_entity_idx",
			"flint_knowledge_domain_idx",
			"flint_knowledge_status_idx",
		} {
			_, _ = r.pool.Exec(context.Background(), `DROP INDEX IF EXISTS `+name)
		}
	})
}

// flintIndexCount는 knowledge 테이블의 flint 접두 인덱스 수를 센다.
func flintIndexCount(t *testing.T, r *Repo) int {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pg_indexes
		 WHERE tablename = 'knowledge' AND indexname LIKE 'flint_knowledge_%'`).
		Scan(&n); err != nil {
		t.Fatalf("count flint indexes: %v", err)
	}
	return n
}

// TestEnsureSchemaV3Idempotent — Review Focus(state): 2회 호출 → 인덱스 3행·
// 버전 3, 2회째는 무변화. protocol: v2 게이트를 경유한 DB(기존 배포 경로)도 승격.
func TestEnsureSchemaV3Idempotent(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	dropV3Indexes(t, r)
	ctx := context.Background()
	// 기존 배포 경로: v1 → v2 게이트 경유 DB.
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
	// 2회째 호출은 멱등 no-op — 인덱스 3행·버전 3 무변화.
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

// TestEnsureSchemaV3FromV1 — v2를 건너뛴 v1 DB도 v3 게이트 하나로 승격된다
// (게이트는 버전만 갱신하고 인덱스는 IF NOT EXISTS로 멱등 적용).
func TestEnsureSchemaV3FromV1(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	dropV3Indexes(t, r)
	if err := r.EnsureSchemaV3(context.Background()); err != nil {
		t.Fatalf("EnsureSchemaV3 from v1: %v", err)
	}
	if n := flintIndexCount(t, r); n != 3 {
		t.Fatalf("flint indexes = %d, want 3", n)
	}
}

// TestEnsureSchemaV2 — v1 DB(fake DDL) 무손실 승격: ALTER 컬럼 추가+버전 2+기존 데이터 유지+멱등.
func TestEnsureSchemaV2(t *testing.T) {
	r := newTestRepo(t)
	resetVersionToV1(t, r)
	ctx := context.Background()
	// v1 시절 데이터 — 승격 후에도 살아 있어야 한다.
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
	// v2 컬럼으로 쓰기 가능(컬럼 존재의 동작 증명).
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
	// 재호출은 멱등 no-op.
	if err := r.EnsureSchemaV2(ctx); err != nil {
		t.Fatalf("second EnsureSchemaV2 (want idempotent): %v", err)
	}
}
