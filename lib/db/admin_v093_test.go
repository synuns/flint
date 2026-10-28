// admin_v093_test.go — T3 DupGroups 슬로우 테스트(실DB).
package db

import (
	"context"
	"reflect"
	"testing"
)

// TestDupGroupsSameTopic — Review Focus(명명, slow·실DB): 동일 topic active
// 2건 이상 그룹 산출(ids 오름차순), 단독 topic·무효화 건은 제외.
// 산식은 Counts의 dup_suspect GROUP BY와 동일(invalid_at IS NULL AND
// status='active' GROUP BY topic HAVING count(*)>1).
func TestDupGroupsSameTopic(t *testing.T) {
	r := newTestRepo(t)
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
	// 무효화하면 그룹에서 빠진다(active만 대상).
	if err := r.Invalidate(ctx, ids[0], "테스트 무효화"); err != nil {
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
