package knowledge

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestImportPAMLinksReAccruedV1201(t *testing.T) {
	res := &PAMResult{
		Candidates: []ImportCandidate{
			{ID: "mem-a", Kind: "knowledge", Content: "alpha", Confidence: 0.8, Source: "import:pam:demo"},
			{ID: "mem-b", Kind: "knowledge", Content: "beta", Confidence: 0.8, Source: "import:pam:demo"},
		},
		Links: []PAMLinkPair{
			{From: "mem-a", To: "mem-b", Relation: "refines"},
			{From: "mem-a", To: "mem-missing", Relation: "supports"},
		},
	}
	st := &fakeStore{}
	oc, err := importPAM(context.Background(), st, res)
	if err != nil {
		t.Fatalf("importPAM: %v", err)
	}
	if oc.links != 1 {
		t.Fatalf("oc.links=%d want 1 (one applied, one skipped endpoint)", oc.links)
	}
	if len(st.linkInserts) != 1 || st.linkInserts[0] != [3]any{1, 2, "refines"} {
		t.Fatalf("linkInserts=%v want [[1 2 refines]]", st.linkInserts)
	}
	if got := importMigrateSummary(oc.imported, oc.duplicated, oc.superseded, oc.remapped, oc.skipped, oc.links); !strings.Contains(got, `"links":1`) {
		t.Fatalf("summary %q must carry links:1", got)
	}
}

func TestImportPAMCreatedAtPreservedV1201(t *testing.T) {
	want := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	res := &PAMResult{
		Candidates: []ImportCandidate{
			{ID: "mem-t", Kind: "knowledge", Content: "timed", Confidence: 0.8,
				Source: "import:pam:demo", CreatedAt: "2026-11-01T10:00:00Z"},
			{ID: "mem-bad", Kind: "knowledge", Content: "untimed", Confidence: 0.8,
				Source: "import:pam:demo", CreatedAt: "not-a-timestamp"},
		},
	}
	st := &fakeStore{}
	if _, err := importPAM(context.Background(), st, res); err != nil {
		t.Fatalf("importPAM: %v", err)
	}
	if len(st.inserted) != 2 {
		t.Fatalf("inserted=%d want 2", len(st.inserted))
	}
	got, ok := st.inserted[0]["ingested_at"].(time.Time)
	if !ok || !got.Equal(want) {
		t.Fatalf("ingested_at=%v want %v (PAM created_at preserved)", st.inserted[0]["ingested_at"], want)
	}
	if _, has := st.inserted[1]["ingested_at"]; has {
		t.Fatalf("garbage created_at must leave ingested_at absent, got %v", st.inserted[1]["ingested_at"])
	}
}

func TestImportJSONLLineageCycleRefusedV1201(t *testing.T) {
	sup := func(i int) *int { v := i; return &v }
	t0 := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	rows := []ExportRow{
		{ID: 900, Topic: "v1201 cyc a", Content: "a", Confidence: 0.8, Status: "active",
			CreatedAt: t0, UpdatedAt: t0, SupersededBy: sup(901)},
		{ID: 901, Topic: "v1201 cyc b", Content: "b", Confidence: 0.8, Status: "active",
			CreatedAt: t0, UpdatedAt: t0, SupersededBy: sup(902)},
		{ID: 902, Topic: "v1201 cyc c", Content: "c", Confidence: 0.8, Status: "active",
			CreatedAt: t0, UpdatedAt: t0, SupersededBy: sup(900)},
	}
	m := &migrateFake{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "WITH RECURSIVE") {
			return [][]any{{true, false}}, nil
		}
		return nil, nil
	}}}
	if _, err := importJSONL(context.Background(), m, rows); err == nil {
		t.Fatal("cycle-shaped lineage accepted")
	} else if !strings.Contains(err.Error(), "supersede cycle") {
		t.Fatalf("err=%v want supersede cycle diagnostic", err)
	}
	if len(m.superseded) != 0 {
		t.Fatalf("Supersede must not run on a cycle verdict: %v", m.superseded)
	}
}
