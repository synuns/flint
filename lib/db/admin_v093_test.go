package db

import (
	"context"
	"reflect"
	"testing"
)

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
