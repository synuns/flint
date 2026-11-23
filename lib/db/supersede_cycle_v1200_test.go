package db

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

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

func cycleRejected(t *testing.T, where string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: no error, want cycle rejection", where)
	}
	if !strings.Contains(err.Error(), "would create a supersede cycle") {
		t.Fatalf("%s: message %q, want \"would create a supersede cycle\" diagnostic", where, err.Error())
	}
}

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

func slowCyclePredicateWalks(t *testing.T, r *Repo) {
	ctx := context.Background()
	ids := seedCycleRows(t, r, "cyc-pred", 4)
	a, b, c, d := ids[0], ids[1], ids[2], ids[3]

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

func slowCyclePredicateDepthCap(t *testing.T, r *Repo) {
	ctx := context.Background()

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

	if _, err := r.SetSupersededRef(ctx, deep[69], deep[0]); err == nil {
		t.Fatal("cap70 closing write: want conservative refusal, got nil")
	}
	if _, sup := lineageOf(t, r, deep[69]); sup != nil {
		t.Fatalf("cap70 closing write: superseded_by=%v, want unset (refused)", sup)
	}
}

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

func TestSlowSupersedeCyclePredicate(t *testing.T) {
	r := newTestRepo(t)
	t.Run("WalkVerdicts", func(t *testing.T) { slowCyclePredicateWalks(t, r) })
	t.Run("DepthCap", func(t *testing.T) { slowCyclePredicateDepthCap(t, r) })
}

func TestSlowMergeCycleGuardSlow(t *testing.T) {
	slowMergeCycleGuard(t, newTestRepo(t))
}

func TestSlowSetSupersededRefCycleGuardSlow(t *testing.T) {
	slowSetSupersededRefCycleGuard(t, newTestRepo(t))
}
