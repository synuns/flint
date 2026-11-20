// diff_store_test.go — DiffRows·DiffStat·DiffCommits (the Doltgres
// implementation of the wire history contract) slow real-DB regression.
// Seeds 4 synthetic demo commits (add→add→delete→modify) and measures
// the 3 diff_types·per-table summaries·commit ranges·ordering. The SQL
// is a lib/ops original port (byte-equivalent), so the commit
// composition also follows the original contract (added/removed via the
// shared COALESCE expression).
package db

import (
	"context"
	"testing"
)

// seedT5Commit — seeds one commit. With an empty sql, staging only
// (called after InsertBatch·pool.Exec); otherwise runs the SQL then
// commits. Returns the commit hash.
func seedT5Commit(t *testing.T, r *Repo, msg, sql string, args ...any) string {
	t.Helper()
	ctx := context.Background()
	if sql != "" {
		if _, err := r.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed exec(%s): %v", msg, err)
		}
	}
	h, err := r.Commit(ctx, msg)
	if err != nil {
		t.Fatalf("Commit(%s): %v", msg, err)
	}
	if h == "" {
		t.Fatalf("Commit(%s): 빈 해시(스테이징 0행)", msg)
	}
	return h
}

// TestDiffRowsSlowTypes — measures diff_types across 3 commit pairs.
// Since c1 can absorb newTestRepo's uncommitted cleanup DELETE, the
// added verification uses only the post-c1 range (c1→c2).
func TestDiffRowsSlowTypes(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	ids, err := r.InsertBatch(ctx, []map[string]any{
		knowledgeRec("v0180-t5-x"), // X — the modify target
	})
	if err != nil {
		t.Fatalf("InsertBatch(X): %v", err)
	}
	c1 := seedT5Commit(t, r, "flint v0180 t5 demo base-x", "")
	c2 := seedT5Commit(t, r, "flint v0180 t5 demo add-y",
		`INSERT INTO knowledge (topic, summary, tags, confidence, content_hash)
		 VALUES ('토픽-v0180-t5-y', '요약', ':a:', 0.9, 'v0180-t5-y')`)
	c3 := seedT5Commit(t, r, "flint v0180 t5 demo rm-y",
		`DELETE FROM knowledge WHERE content_hash = 'v0180-t5-y'`)
	c4 := seedT5Commit(t, r, "flint v0180 t5 demo mod-x",
		`UPDATE knowledge SET topic = '토픽-v0180-t5-x2' WHERE id = $1`, ids[0])

	cases := []struct {
		name      string
		from, to  string
		wantType  string
		wantTopic string
	}{
		{"added — c1→c2", c1, c2, "added", "토픽-v0180-t5-y"},
		{"removed — c2→c3", c2, c3, "removed", "토픽-v0180-t5-y"},
		{"modified — c3→c4", c3, c4, "modified", "토픽-v0180-t5-x2"},
	}
	for _, tc := range cases {
		rows, err := r.DiffRows(ctx, tc.from, tc.to)
		if err != nil {
			t.Fatalf("DiffRows(%s): %v", tc.name, err)
		}
		if len(rows) != 1 {
			t.Fatalf("DiffRows(%s) = %v, want 1행", tc.name, rows)
		}
		got := rows[0]
		if got.DiffType != tc.wantType || got.Topic != tc.wantTopic {
			t.Errorf("DiffRows(%s) = {%s %s}, want {%s %s}",
				tc.name, got.DiffType, got.Topic, tc.wantType, tc.wantTopic)
		}
		if got.ID == "" {
			t.Errorf("DiffRows(%s) ID 비어 있음: %+v", tc.name, got)
		}
	}
	// The identical commit range — no diff (0 rows).
	same, err := r.DiffRows(ctx, c4, c4)
	if err != nil {
		t.Fatalf("DiffRows(동일 해시): %v", err)
	}
	if len(same) != 0 {
		t.Fatalf("DiffRows(c4,c4) = %v, want 0행", same)
	}
}

// TestDiffStatSlowSummary — per-table change statistics for a
// commit-hash list. A knowledge commit yields knowledge-table
// data_change=true·schema_change=false.
func TestDiffStatSlowSummary(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v0180-t5-stat")}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	h := seedT5Commit(t, r, "flint v0180 t5 demo stat", "")

	stats, err := r.DiffStat(ctx, []string{h})
	if err != nil {
		t.Fatalf("DiffStat: %v", err)
	}
	found := false
	for _, s := range stats {
		if s.CommitHash != h {
			continue
		}
		found = true
		if s.Table == "knowledge" {
			if !s.DataChange {
				t.Errorf("DiffStat(knowledge).DataChange = false, want true")
			}
			if s.SchemaChange {
				t.Errorf("DiffStat(knowledge).SchemaChange = true, want false")
			}
		}
	}
	if !found {
		t.Fatalf("DiffStat(%s) = %v, want 해당 해시 행", h, stats)
	}
	// An empty list — 0 rows without an empty-array ANY($1) error.
	none, err := r.DiffStat(ctx, nil)
	if err != nil {
		t.Fatalf("DiffStat(nil): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("DiffStat(nil) = %v, want 0행", none)
	}
}

// TestDiffCommitsSlowRange — the from(exclusive)~to(inclusive) range·
// newest-first·data_change. LIMIT diffCommitsMax (20) inherits the
// original constant — instead of seeding 21 commits, the contract is
// verified via the boundary (identical hash = empty range)·out-of-range
// exclusion·DESC ordering.
func TestDiffCommitsSlowRange(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.InsertBatch(ctx, []map[string]any{knowledgeRec("v0180-t5-c0")}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	c1 := seedT5Commit(t, r, "flint v0180 t5 demo r1", "")
	c2 := seedT5Commit(t, r, "flint v0180 t5 demo r2",
		`INSERT INTO knowledge (topic, summary, tags, confidence, content_hash)
		 VALUES ('토픽-v0180-t5-c2', '요약', ':a:', 0.9, 'v0180-t5-c2')`)
	c3 := seedT5Commit(t, r, "flint v0180 t5 demo r3",
		`UPDATE knowledge SET summary = '요약2' WHERE content_hash = 'v0180-t5-c2'`)

	// c1(exclusive)~c3(inclusive) — c3, c2 DESC.
	cs, err := r.DiffCommits(ctx, c1, c3)
	if err != nil {
		t.Fatalf("DiffCommits: %v", err)
	}
	if len(cs) != 2 {
		t.Fatalf("DiffCommits(c1,c3) = %d행, want 2행: %+v", len(cs), cs)
	}
	if cs[0].Hash != c3 || cs[1].Hash != c2 {
		t.Fatalf("DiffCommits 순서 = [%s %s], want [%s %s] DESC", cs[0].Hash, cs[1].Hash, c3, c2)
	}
	if cs[0].Message != "flint v0180 t5 demo r3" {
		t.Errorf("DiffCommits[0].Message = %q, want 커밋 메시지", cs[0].Message)
	}
	if cs[0].Date.IsZero() {
		t.Errorf("DiffCommits[0].Date 제로값: %+v", cs[0])
	}
	if !cs[0].DataChange {
		t.Errorf("DiffCommits[0].DataChange = false, want true(지식 수정 커밋)")
	}
	if cs[0].SchemaChange {
		t.Errorf("DiffCommits[0].SchemaChange = true, want false")
	}

	// The identical hash — an empty range (commit_order > h AND <= h).
	same, err := r.DiffCommits(ctx, c3, c3)
	if err != nil {
		t.Fatalf("DiffCommits(동일 해시): %v", err)
	}
	if len(same) != 0 {
		t.Fatalf("DiffCommits(c3,c3) = %v, want 0행", same)
	}
}
