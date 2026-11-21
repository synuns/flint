// repo_transition_test.go — the repo unification of the knowledge
// state-transition UPDATEs (Invalidate·Supersede — rows-affected
// stewardship built in). The fake intercepts SQL·parameters via the
// transitionExecFn seam (SUPERLITE_REAL=1 keeps the real implementation
// — the real-path round trip is verified by the slow real-DB line).
package db

import (
	"context"
	"errors"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// transitionRecorder — the fake execution-seam recording (per-call
// SQL·parameters) plus scripted rows-affected.
type transitionRecorder struct {
	calls []transitionCall
	n     int64 // the rows-affected each call returns (default 1)
}

type transitionCall struct {
	sql  string
	args []any
}

// useFakeTransitionExec — swaps the transitionExecFn seam. With
// SUPERLITE_REAL=1, leaves the real implementation as-is (a test-seam
// check — in that case the caller goes to the slow line). v1.20.1: the
// supersede advisory-lock seam is swapped together (fake Supersede
// callers run against a nil pool — the lock is a real-DB concern).
// v1.20.2 (r88): the lock seam returns a conn-scoped exec — the fake
// exec records into the SAME recorder (Supersede's guarded UPDATE
// stays interceptable exactly as before).
func useFakeTransitionExec(t *testing.T) *transitionRecorder {
	t.Helper()
	if os.Getenv("SUPERLITE_REAL") == "1" {
		return nil
	}
	rec := &transitionRecorder{n: 1}
	prev := transitionExecFn
	transitionExecFn = func(ctx context.Context, r *Repo, query string, args ...any) (int64, error) {
		rec.calls = append(rec.calls, transitionCall{sql: query, args: args})
		return rec.n, nil
	}
	prevLock := supersedeLockFn
	supersedeLockFn = func(ctx context.Context, r *Repo) (supersedeExecFn, func(), error) {
		exec := func(ctx context.Context, query string, args ...any) (int64, error) {
			rec.calls = append(rec.calls, transitionCall{sql: query, args: args})
			return rec.n, nil
		}
		return exec, func() {}, nil
	}
	t.Cleanup(func() {
		transitionExecFn = prev
		supersedeLockFn = prevLock
	})
	return rec
}

// updateKnowledgeRe — the single-source assertion: detecting UPDATE
// knowledge literals in the ops·knowledge non-test sources.
var updateKnowledgeRe = regexp.MustCompile(`(?i)UPDATE\s+knowledge`)

// transitionSources — the sources of transition-delegation targets
// (maintain's 3 renewal paths·approve's renewal).
var transitionSources = []string{
	"../ops/maintain.go",
	"../knowledge/approve.go",
}

// assertNoUpdateKnowledgeLiterals — 0 residual UPDATE knowledge literals
// in the sources (everything via Repo —
// TestInvalidateSupersedeSingleSource).
func assertNoUpdateKnowledgeLiterals(t *testing.T) {
	t.Helper()
	for _, path := range transitionSources {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("소스 읽기 %s: %v", path, err)
		}
		if loc := updateKnowledgeRe.FindStringIndex(string(b)); loc != nil {
			t.Errorf("%s에 UPDATE knowledge 리터럴 잔여(오프셋 %d) — Repo 단일화 위반", path, loc[0])
		}
	}
}

// TestInvalidateSupersedeSingleSource — the transition UPDATEs have the
// single Repo path — ① asserts Invalidate·Supersede's SQL co-writing
// (fake repo level) ② 0 UPDATE knowledge literals in the maintain·
// approve sources.
func TestInvalidateSupersedeSingleSource(t *testing.T) {
	assertNoUpdateKnowledgeLiterals(t)
	if os.Getenv("SUPERLITE_REAL") == "1" {
		t.Skip("REAL 모드 — SQL 캡처는 fake seam 전제(실경로는 DB 라인이 검증)")
	}
	rec := useFakeTransitionExec(t)
	r := &Repo{}
	ctx := context.Background()

	n, err := r.Invalidate(ctx, []int{1, 2}, "invalidate-stale")
	if err != nil || n != 1 {
		t.Fatalf("Invalidate = (%d, %v), want (1, nil)", n, err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("전이 실행 %d회, want 1", len(rec.calls))
	}
	if sqlText := rec.calls[0].sql; !strings.Contains(sqlText, "status = 'invalidated'") ||
		!strings.Contains(sqlText, "invalid_at = now()") ||
		!strings.Contains(sqlText, "WHERE id = ANY($1)") {
		t.Errorf("Invalidate SQL 병기 미충족: %q", sqlText)
	}
	if len(rec.calls[0].args) != 1 || !reflect.DeepEqual(rec.calls[0].args[0], []int{1, 2}) {
		t.Errorf("Invalidate 파라미터=%v, want [[1 2]]", rec.calls[0].args)
	}

	at := time.Now().Add(-24 * time.Hour)
	n, err = r.Supersede(ctx, 3, 7, at)
	if err != nil || n != 1 {
		t.Fatalf("Supersede = (%d, %v), want (1, nil)", n, err)
	}
	if len(rec.calls) != 2 {
		t.Fatalf("전이 실행 %d회, want 2", len(rec.calls))
	}
	if supSQL := rec.calls[1].sql; !strings.Contains(supSQL, "status = 'invalidated'") ||
		!strings.Contains(supSQL, "superseded_by = $2") ||
		!strings.Contains(supSQL, "invalid_at = $3") ||
		!strings.Contains(supSQL, "WHERE id = $1 AND status = 'active' AND invalid_at IS NULL") ||
		!strings.Contains(supSQL, "NOT EXISTS") {
		t.Errorf("Supersede SQL 병기 미충족: %q", supSQL)
	}
	if !reflect.DeepEqual(rec.calls[1].args, []any{3, 7, at, SupersedeCycleDepthLimit}) {
		t.Errorf("Supersede 파라미터=%v, want [3 7 %v %d]", rec.calls[1].args, at, SupersedeCycleDepthLimit)
	}

	// The 0-rows steward — no silent race success (ErrNotFound wrap:
	// inheriting the existing supersede steward contract).
	rec.n = 0
	if _, err := r.Invalidate(ctx, []int{9}, "경합"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Invalidate 0행 오류 = %v, want ErrNotFound 랩", err)
	}
	if _, err := r.Supersede(ctx, 3, 7, at); !errors.Is(err, ErrNotFound) {
		t.Errorf("Supersede 0행 오류 = %v, want ErrNotFound 랩", err)
	}
	// The race error message is English — the message passes through
	// directly to MCP rpcError.
	if _, err := r.Invalidate(ctx, []int{9}, "race"); !strings.Contains(err.Error(), "0 rows updated (race)") {
		t.Errorf("Invalidate 0행 메시지 = %v, want English race message", err)
	}
}

// TestSupersedeRecordsLineage — the lineage is the single
// superseded_by-recording model (the dual text-marker model abolished).
// Fake — the SQL records superseded_by and leaves no summary text
// marker. REAL — verifies superseded_by·invalid_at=NEW.valid_at
// recording on the real DB.
func TestSupersedeRecordsLineage(t *testing.T) {
	if os.Getenv("SUPERLITE_REAL") == "1" {
		testSupersedeRecordsLineageDB(t)
		return
	}
	rec := useFakeTransitionExec(t)
	at := time.Now().Add(-24 * time.Hour)
	if _, err := (&Repo{}).Supersede(context.Background(), 11, 12, at); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("전이 실행 %d회, want 1", len(rec.calls))
	}
	if !strings.Contains(rec.calls[0].sql, "superseded_by = $2") {
		t.Errorf("계보 미기록: %q", rec.calls[0].sql)
	}
	if strings.Contains(rec.calls[0].sql, "merged:from") ||
		strings.Contains(rec.calls[0].sql, "summary") {
		t.Errorf("텍스트 마커 이중 모델 잔여: %q", rec.calls[0].sql)
	}
}

// testSupersedeRecordsLineageDB — the slow real-DB line (a seam check —
// REAL mode).
func testSupersedeRecordsLineageDB(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	newAt := time.Now().Add(-2 * time.Hour).Truncate(time.Microsecond)
	newRec := knowledgeRec("super-new")
	newRec["valid_at"] = newAt
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("super-old"), newRec,
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	oldID, newID := ids[0], ids[1]
	if _, err := r.Supersede(ctx, oldID, newID, newAt); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	var status string
	var supBy *int
	var invalidAt *time.Time
	var summary string
	if err := r.pool.QueryRow(ctx,
		`SELECT status, superseded_by, invalid_at, summary FROM knowledge WHERE id = $1`, oldID).
		Scan(&status, &supBy, &invalidAt, &summary); err != nil {
		t.Fatalf("조회: %v", err)
	}
	if status != "invalidated" || supBy == nil || *supBy != newID {
		t.Fatalf("계보 미기록: status=%s superseded_by=%v", status, supBy)
	}
	if invalidAt == nil || !invalidAt.Equal(newAt) {
		t.Fatalf("invalid_at=%v, want NEW.valid_at %v", invalidAt, newAt)
	}
	if strings.Contains(summary, "merged:from") {
		t.Error("텍스트 마커 이중 모델 — summary에 merged 마커 잔여")
	}
	// Recalled (already invalidated) — the 0-rows steward.
	if _, err := r.Supersede(ctx, oldID, newID, newAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("0행 재시도 오류 = %v, want ErrNotFound 랩", err)
	}
}

// TestInvalidateGuardsProcessedRows — Invalidate's WHERE co-writes the
// invalid_at IS NULL guard — if a parallel supersede processed the same
// row between the dry-run listing and --commit, it is not re-updated
// (blocking the corruption of overwriting the lineage's
// invalid_at=NEW.valid_at with now()). Fake — the SQL co-write
// assertion (the same guard as the listing side's
// listExpiredPending·invalidate-stale). REAL — on the real DB, re-
// Invalidation of a superseded row yields 0 rows (ErrNotFound)·the
// lineage unchanged.
func TestInvalidateGuardsProcessedRows(t *testing.T) {
	if os.Getenv("SUPERLITE_REAL") == "1" {
		testInvalidateGuardsProcessedRowsDB(t)
		return
	}
	rec := useFakeTransitionExec(t)
	if _, err := (&Repo{}).Invalidate(context.Background(), []int{5}, "expire-pending"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("전이 실행 %d회, want 1", len(rec.calls))
	}
	if sqlText := rec.calls[0].sql; !strings.Contains(sqlText, "WHERE id = ANY($1) AND invalid_at IS NULL") {
		t.Errorf("Invalidate WHERE 가드 미병기: %q — 이미 처리된 행(invalid_at 기록됨) 재갱신 허용", sqlText)
	}
}

// testInvalidateGuardsProcessedRowsDB — the slow real-DB line (REAL
// mode): re-calling Invalidate on a row already processed by supersede —
// the 0-rows steward + lineage unchanged.
func testInvalidateGuardsProcessedRowsDB(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	newAt := time.Now().Add(-2 * time.Hour).Truncate(time.Microsecond)
	newRec := knowledgeRec("guard-new")
	newRec["valid_at"] = newAt
	ids, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("guard-old"), newRec})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	oldID, newID := ids[0], ids[1]
	if _, err := r.Supersede(ctx, oldID, newID, newAt); err != nil {
		t.Fatalf("Supersede: %v", err)
	}
	// Reproducing the race between listing (skipped) and confirmation —
	// the guard blocks it with 0 rows.
	if _, err := r.Invalidate(ctx, []int{oldID}, "경합 재시도"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("재 Invalidate 오류 = %v, want ErrNotFound 랩(0행 — 가드)", err)
	}
	// Lineage unchanged — superseded_by·invalid_at=NEW.valid_at kept (no
	// now() overwrite).
	var supBy *int
	var invalidAt *time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT superseded_by, invalid_at FROM knowledge WHERE id = $1`, oldID).
		Scan(&supBy, &invalidAt); err != nil {
		t.Fatalf("조회: %v", err)
	}
	if supBy == nil || *supBy != newID {
		t.Fatalf("계보 훼손: superseded_by=%v, want %d", supBy, newID)
	}
	if invalidAt == nil || !invalidAt.Equal(newAt) {
		t.Fatalf("invalid_at=%v, want NEW.valid_at %v (now() 덮어쓰기 금지)", invalidAt, newAt)
	}
}
