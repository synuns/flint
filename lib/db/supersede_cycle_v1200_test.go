// supersede_cycle_v1200_test.go — v1.20.0 T3 supersede cycle guard slow
// tests (real DB, synthetic demo rows only). The guard predicate is
// WouldCreateCycle / SupersedeCycleSQL: one round trip — a depth-capped
// WITH RECURSIVE walk of superseded_by starting at NEW; reaching OLD
// means the pending write would close a lineage loop. Guarded writers:
// Merge (maintain --merge) and SetSupersededRef (import lineage
// restoration); maintain --supersede runs the same predicate as a
// precheck (verified at the cli boundary in lib/cli).
package db

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// seedCycleRows — n active rows with unique content hashes; returns the
// inserted ids in insertion order.
func seedCycleRows(t *testing.T, r *Repo, prefix string, n int) []int {
	t.Helper()
	recs := make([]map[string]any, n)
	for i := range recs {
		recs[i] = knowledgeRec(fmt.Sprintf("%s-%03d", prefix, i))
	}
	ids, err := r.InsertBatch(context.Background(), recs)
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if len(ids) != n {
		t.Fatalf("InsertBatch ids = %d, want %d", len(ids), n)
	}
	return ids
}

// cycleRejected — common assertion: the error carries the cycle
// diagnostic (contract wording).
func cycleRejected(t *testing.T, where string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: no error, want cycle rejection", where)
	}
	if !strings.Contains(err.Error(), "would create a supersede cycle") {
		t.Fatalf("%s: message %q, want \"would create a supersede cycle\" diagnostic", where, err.Error())
	}
}

// lineageOf — (status, superseded_by) of a row, for post-rejection
// state assertions.
func lineageOf(t *testing.T, r *Repo, id int) (string, *int) {
	t.Helper()
	var status string
	var sup *int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT status, superseded_by FROM knowledge WHERE id = $1`, id).
		Scan(&status, &sup); err != nil {
		t.Fatalf("read lineage id=%d: %v", id, err)
	}
	return status, sup
}

// slowCyclePredicateWalks — WouldCreateCycle unit family: a 1-hop and a
// 3-hop reverse edge are cycles; an unrelated pair and a legitimate
// chain extension are not; OLD==NEW is a 1-hop cycle.
func slowCyclePredicateWalks(t *testing.T, r *Repo) {
	ctx := context.Background()
	ids := seedCycleRows(t, r, "cyc-pred", 4)
	a, b, c, d := ids[0], ids[1], ids[2], ids[3]
	// Build the chain A→B→C with SetSupersededRef (ref-only writes —
	// each step's NEW has no outgoing ref yet, so no guard trips).
	for _, step := range [][2]int{{a, b}, {b, c}} {
		if _, err := r.SetSupersededRef(ctx, step[0], step[1]); err != nil {
			t.Fatalf("chain %d→%d: %v", step[0], step[1], err)
		}
	}
	for _, tc := range []struct {
		where        string
		oldID, newID int
		want         bool
	}{
		{"1-hop reverse (B→A)", b, a, true},
		{"3-hop reverse (C→A)", c, a, true},
		{"chain extension (D→C)", d, c, false},
		{"unrelated pair (D→A)", d, a, false},
		{"self (A→A)", a, a, true},
	} {
		got, err := r.WouldCreateCycle(ctx, tc.oldID, tc.newID)
		if err != nil {
			t.Fatalf("%s: %v", tc.where, err)
		}
		if got != tc.want {
			t.Errorf("%s: WouldCreateCycle=%v, want %v", tc.where, got, tc.want)
		}
	}
}

// slowCyclePredicateDepthCap — the recursive walk stops at the depth
// cap (double safety against runaway recursion): a closing edge whose
// loop path stays within the cap is still detected; since v1.20.1
// (r87) one beyond the cap is a CONSERVATIVE REFUSAL — the walk's
// evidence window ended before the chain did, and the old "safe
// no-cycle" verdict (an absence claim beyond the evidence) is
// abolished.
func slowCyclePredicateDepthCap(t *testing.T, r *Repo) {
	ctx := context.Background()
	// Within cap: chain of 64 rows — OLD=#64 is reached at walk depth
	// 64, so the closing loop is detected.
	ids := seedCycleRows(t, r, "cyc-cap64", 64)
	for i := 0; i+1 < len(ids); i++ {
		if _, err := r.SetSupersededRef(ctx, ids[i], ids[i+1]); err != nil {
			t.Fatalf("cap64 chain step %d: %v", i, err)
		}
	}
	got, err := r.WouldCreateCycle(ctx, ids[63], ids[0])
	if err != nil || got != true {
		t.Fatalf("cap64 closing edge: (%v, %v), want (true, nil)", got, err)
	}

	// Beyond cap: 70 rows — the walk from NEW=#1 tops out before
	// reaching OLD=#70: the conservative depth-cap refusal (an error,
	// not a no-cycle verdict).
	deep := seedCycleRows(t, r, "cyc-cap70", 70)
	for i := 0; i+1 < len(deep); i++ {
		if _, err := r.SetSupersededRef(ctx, deep[i], deep[i+1]); err != nil {
			t.Fatalf("cap70 chain step %d: %v", i, err)
		}
	}
	got, err = r.WouldCreateCycle(ctx, deep[69], deep[0])
	if err == nil || !strings.Contains(err.Error(), "depth cap") {
		t.Fatalf("cap70 closing edge: (%v, %v), want the conservative depth-cap refusal", got, err)
	}
	// The beyond-cap write is refused too — the SetSupersededRef guard
	// propagates the refusal; no loop planting through the truncation.
	if _, err := r.SetSupersededRef(ctx, deep[69], deep[0]); err == nil {
		t.Fatal("cap70 closing write: want conservative refusal, got nil")
	}
	if _, sup := lineageOf(t, r, deep[69]); sup != nil {
		t.Fatalf("cap70 closing write: superseded_by=%v, want unset (refused)", sup)
	}
}

// slowMergeCycleGuard — Merge writes superseded_by too (from→to), so
// the reverse merge after X→Y is rejected with the same diagnostic and
// Y's row is untouched (active, no lineage ref).
func slowMergeCycleGuard(t *testing.T, r *Repo) {
	ctx := context.Background()
	ids := seedCycleRows(t, r, "cyc-merge", 2)
	x, y := ids[0], ids[1]
	if err := r.Merge(ctx, x, y); err != nil {
		t.Fatalf("merge x→y: %v", err)
	}
	cycleRejected(t, "reverse merge y→x", r.Merge(ctx, y, x))
	status, sup := lineageOf(t, r, y)
	if status != "active" || sup != nil {
		t.Fatalf("after rejection y: status=%q superseded_by=%v, want active/nil", status, sup)
	}
}

// slowSetSupersededRefCycleGuard — the import lineage-restore path
// guards the same shape: filling P.superseded_by then Q.superseded_by=P
// is rejected; a self reference is a 1-hop cycle.
func slowSetSupersededRefCycleGuard(t *testing.T, r *Repo) {
	ctx := context.Background()
	ids := seedCycleRows(t, r, "cyc-ref", 3)
	p, q, s := ids[0], ids[1], ids[2]
	if _, err := r.SetSupersededRef(ctx, p, q); err != nil {
		t.Fatalf("ref p→q: %v", err)
	}
	_, err := r.SetSupersededRef(ctx, q, p)
	cycleRejected(t, "ref q→p", err)
	_, err = r.SetSupersededRef(ctx, s, s)
	cycleRejected(t, "self ref", err)
}

// TestSlowSupersedeCyclePredicate — the WouldCreateCycle family:
// walk verdicts, self, depth cap.
func TestSlowSupersedeCyclePredicate(t *testing.T) {
	r := newTestRepo(t)
	t.Run("WalkVerdicts", func(t *testing.T) { slowCyclePredicateWalks(t, r) })
	t.Run("DepthCap", func(t *testing.T) { slowCyclePredicateDepthCap(t, r) })
}

// TestSlowMergeCycleGuardSlow — the Merge (maintain --merge) family.
func TestSlowMergeCycleGuardSlow(t *testing.T) {
	slowMergeCycleGuard(t, newTestRepo(t))
}

// TestSlowSetSupersededRefCycleGuardSlow — the import lineage-restore family.
func TestSlowSetSupersededRefCycleGuardSlow(t *testing.T) {
	slowSetSupersededRefCycleGuard(t, newTestRepo(t))
}
