// admin_v093_test.go — DupGroups slow tests (real DB).
package db

import (
	"context"
	"reflect"
	"testing"
)

// TestDupGroupsSameTopic — produces groups of 2+ active rows sharing a
// topic (ids ascending); solo topics·invalidated rows excluded. The
// formula is the same GROUP BY as Counts' dup_suspect (invalid_at IS
// NULL AND status='active' GROUP BY topic HAVING count(*)>1).
func slowDupGroupsSameTopic(t *testing.T, r *Repo) {
	ctx := context.Background()
	a := knowledgeRec("dup-a")
	a["topic"] = "rv-e"
	b := knowledgeRec("dup-b")
	b["topic"] = "rv-e"
	c := knowledgeRec("dup-c")
	c["topic"] = "solo"
	ids, err := r.InsertBatch(ctx, []map[string]any{a, b, c})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	groups, err := r.DupGroups(ctx, 10)
	if err != nil {
		t.Fatalf("DupGroups: %v", err)
	}
	want := []DupGroup{{Topic: "rv-e", IDs: []int{ids[0], ids[1]}}}
	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("DupGroups = %+v, want %+v", groups, want)
	}
	// Invalidating drops it from the group (active-only targets).
	if _, err := r.Invalidate(ctx, []int{ids[0]}, "테스트 무효화"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	groups, err = r.DupGroups(ctx, 10)
	if err != nil {
		t.Fatalf("DupGroups after invalidate: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("무효화 후 그룹 = %+v, want 빈", groups)
	}
}
