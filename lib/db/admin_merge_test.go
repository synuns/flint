// admin_merge_test.go — Merge's lineage superseded_by recording slow
// tests. The merge transition follows the same columns·update shape as
// Supersede (the single superseded_by model) — a regression of the
// lineage-loss defect that left only a summary marker.
package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestMergeRecordsSupersededBy — after merging, the from row's
// superseded_by==into. (Contract: Merge records superseded_by in the
// same shape as Supersede, alongside the marker-suffix convention.)
func slowMergeRecordsSupersededBy(t *testing.T, r *Repo) {
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("merge-sup-from"),
		knowledgeRec("merge-sup-to"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	from, to := ids[0], ids[1]
	if err := r.Merge(ctx, from, to); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	var supersededBy *int
	var status string
	if err := r.pool.QueryRow(ctx,
		`SELECT superseded_by, status FROM knowledge WHERE id = $1`, from).
		Scan(&supersededBy, &status); err != nil {
		t.Fatalf("read from: %v", err)
	}
	if supersededBy == nil || *supersededBy != to {
		t.Fatalf("from.superseded_by = %v, want %d", supersededBy, to)
	}
	if status != "invalidated" {
		t.Fatalf("from.status = %q, want invalidated", status)
	}
	// to stays active — the lineage receiver unchanged.
	if err := r.pool.QueryRow(ctx,
		`SELECT status FROM knowledge WHERE id = $1`, to).Scan(&status); err != nil {
		t.Fatalf("read to: %v", err)
	}
	if status != "active" {
		t.Fatalf("to.status = %q, want active", status)
	}
}

// TestMergeTwiceRejected — re-merging an already-invalidated from is
// refused + the marker stays single. The re-merge is refused by
// Supersede's 0-rows steward convention (an ErrNotFound-wrapped error),
// and the refusal wording keeps the already-processed (invalidated)
// case.
func slowMergeTwiceRejected(t *testing.T, r *Repo) {
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("merge-twice-from"),
		knowledgeRec("merge-twice-to"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	from, to := ids[0], ids[1]
	if err := r.Merge(ctx, from, to); err != nil {
		t.Fatalf("first Merge: %v", err)
	}
	if err := r.Merge(ctx, from, to); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound on re-merge of invalidated from, got %v", err)
	} else if !strings.Contains(err.Error(), "from already processed — re-merge refused") {
		t.Fatalf("re-merge of invalidated from: message %q, want \"from already processed — re-merge refused\" 안내 유지",
			err.Error())
	}
	var summary string
	if err := r.pool.QueryRow(ctx,
		`SELECT summary FROM knowledge WHERE id = $1`, to).Scan(&summary); err != nil {
		t.Fatalf("read to: %v", err)
	}
	marker := fmt.Sprintf(" [merged:from=%d]", from)
	if n := strings.Count(summary, marker); n != 1 {
		t.Fatalf("marker count = %d, want 1 (summary %q)", n, summary)
	}
	// The lineage also keeps the first merge's value.
	var supersededBy *int
	if err := r.pool.QueryRow(ctx,
		`SELECT superseded_by FROM knowledge WHERE id = $1`, from).
		Scan(&supersededBy); err != nil {
		t.Fatalf("read from: %v", err)
	}
	if supersededBy == nil || *supersededBy != to {
		t.Fatalf("from.superseded_by = %v, want %d", supersededBy, to)
	}
}

// TestMergePendingRejected — merging a non-active (pending) from is
// refused. The 0-rows steward distinguishes the status in its guidance:
// statuses other than invalidated get the "not active (cannot merge) —
// status: <value>" wording.
func slowMergePendingRejected(t *testing.T, r *Repo) {
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("merge-pending-from"),
		knowledgeRec("merge-pending-to"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	from, to := ids[0], ids[1]
	// Force from to pending — reproducing the case failing the
	// active-update condition.
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET status = 'pending' WHERE id = $1`, from); err != nil {
		t.Fatalf("force pending: %v", err)
	}
	err = r.Merge(ctx, from, to)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound on merge of pending from, got %v", err)
	}
	if !strings.Contains(err.Error(), "active") {
		t.Fatalf("merge of pending from: message %q, want \"active\" 안내", err.Error())
	}
	if !strings.Contains(err.Error(), "status: pending") {
		t.Fatalf("merge of pending from: message %q, want \"status: pending\" 구분 안내",
			err.Error())
	}
	// to unchanged — no marker attached on the refusal path.
	var toStatus string
	if err := r.pool.QueryRow(ctx,
		`SELECT status FROM knowledge WHERE id = $1`, to).Scan(&toStatus); err != nil {
		t.Fatalf("read to: %v", err)
	}
	if toStatus != "active" {
		t.Fatalf("to.status = %q, want active", toStatus)
	}
}

// TestMergeActiveInvalidAtRejected — a from with status='active' +
// invalid_at set (a corrupted state) is also refused by the 0-rows
// steward. The wording must name the invalid_at cause instead of the
// self-contradictory "not active — status: active".
func slowMergeActiveInvalidAtRejected(t *testing.T, r *Repo) {
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("merge-actinv-from"),
		knowledgeRec("merge-actinv-to"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	from, to := ids[0], ids[1]
	// Force the corrupted state — status is active yet invalid_at is
	// set.
	if _, err := r.pool.Exec(ctx,
		`UPDATE knowledge SET invalid_at = now() WHERE id = $1`, from); err != nil {
		t.Fatalf("force invalid_at: %v", err)
	}
	err = r.Merge(ctx, from, to)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound on merge of active+invalid_at from, got %v", err)
	}
	if !strings.Contains(err.Error(), "invalid_at") {
		t.Fatalf("active+invalid_at: message %q, want invalid_at 원인 명시", err.Error())
	}
	if strings.Contains(err.Error(), "not active") {
		t.Fatalf("active+invalid_at: message %q — status active에 자기모순 문구", err.Error())
	}
}
