package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

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

	if err := r.pool.QueryRow(ctx,
		`SELECT status FROM knowledge WHERE id = $1`, to).Scan(&status); err != nil {
		t.Fatalf("read to: %v", err)
	}
	if status != "active" {
		t.Fatalf("to.status = %q, want active", status)
	}
}

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

	var toStatus string
	if err := r.pool.QueryRow(ctx,
		`SELECT status FROM knowledge WHERE id = $1`, to).Scan(&toStatus); err != nil {
		t.Fatalf("read to: %v", err)
	}
	if toStatus != "active" {
		t.Fatalf("to.status = %q, want active", toStatus)
	}
}

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
