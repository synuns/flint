package knowledge

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/render"
)

func TestSlowWriteLinkTargetInt4OverflowRefusedV1201(t *testing.T) {
	st := t5ScratchStore(t)
	ctx := context.Background()
	var out strings.Builder
	code := RunWrite(ctx, st, []string{"knowledge"}, strings.NewReader(
		`{"topic":"v1201 int4 overflow","summary":"s","confidence":0.9,"relations":[{"to":2147483648,"relation":"refines"}]}`),
		&out)
	if code != 3 {
		t.Fatalf("exit=%d want 3: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "2147483648") {
		t.Fatalf("refusal must name the target id: %s", out.String())
	}
	rows, err := st.Query(ctx, `SELECT count(*) FROM knowledge`)
	if err != nil || len(rows) == 0 {
		t.Fatalf("knowledge count: %v", err)
	}
	if n, _ := render.CellToInt(rows[0][0]); n != 0 {
		t.Fatalf("knowledge rows=%d want 0 (refusal contaminates nothing)", n)
	}
	rows, err = st.Query(ctx, `SELECT count(*) FROM knowledge_links`)
	if err != nil || len(rows) == 0 {
		t.Fatalf("links count: %v", err)
	}
	if n, _ := render.CellToInt(rows[0][0]); n != 0 {
		t.Fatalf("knowledge_links rows=%d want 0", n)
	}
}

func linkTripleCount(t *testing.T, st *writeRealStore) int {
	t.Helper()
	rows, err := st.Query(context.Background(), `SELECT count(*) FROM knowledge_links`)
	if err != nil || len(rows) == 0 {
		t.Fatalf("links count: %v", err)
	}
	n, _ := render.CellToInt(rows[0][0])
	return n
}

func TestSlowWriteMixedBatchLinksAccruedV1201(t *testing.T) {
	st := t5ScratchStore(t)
	ctx := context.Background()

	base := `{"topic":"v1201 base %s","summary":"base %s","confidence":0.9}`
	var out strings.Builder
	if code := RunWrite(ctx, st, []string{"knowledge"}, strings.NewReader(
		fmt.Sprintf(base, "a", "a")+"\n"+fmt.Sprintf(base, "b", "b")+"\n"), &out); code != 0 {
		t.Fatalf("base 적립 exit=%d: %s", code, out.String())
	}
	aID := idOfTopic(t, st, "v1201 base a")
	bID := idOfTopic(t, st, "v1201 base b")

	out.Reset()
	code := RunWrite(ctx, st, []string{"knowledge"}, strings.NewReader(
		fmt.Sprintf(`{"topic":"v1201 base a","summary":"base a","confidence":0.9,"relations":[{"to":%d,"relation":"duplicates"}]}`, bID)+"\n"+
			fmt.Sprintf(`{"topic":"v1201 mixed fresh","summary":"fresh","confidence":0.9,"relations":[{"to":%d,"relation":"supports"}]}`, aID)+"\n"),
		&out)
	if code != 0 {
		t.Fatalf("혼합 배치 exit=%d: %s", code, out.String())
	}
	freshID := idOfTopic(t, st, "v1201 mixed fresh")
	if n := linkTripleCount(t, st); n != 2 {
		t.Fatalf("knowledge_links=%d행 want 2 (dup-side + fresh-side both accrued)", n)
	}
	for _, tr := range [][3]any{{bID, aID, "duplicates"}, {aID, freshID, "supports"}} {
		rows, err := st.Query(ctx,
			`SELECT count(*) FROM knowledge_links WHERE from_id=$1 AND to_id=$2 AND relation=$3`,
			tr[1], tr[0], tr[2])
		if err != nil || len(rows) == 0 {
			t.Fatalf("링크 조회: %v", err)
		}
		if n, _ := render.CellToInt(rows[0][0]); n != 1 {
			t.Fatalf("링크 (%v→%v %v) 부재 — 혼합 배치 적립 누락", tr[1], tr[0], tr[2])
		}
	}
}

func TestSlowPAMLinksRoundtripV1201(t *testing.T) {
	src := t5ScratchStore(t)
	ctx := context.Background()
	var out strings.Builder
	if code := RunWrite(ctx, src, []string{"knowledge"}, strings.NewReader(
		`{"topic":"v1201 rt alpha","summary":"rt alpha","confidence":0.9}`+"\n"+
			`{"topic":"v1201 rt beta","summary":"rt beta","confidence":0.9}`+"\n"), &out); code != 0 {
		t.Fatalf("수출측 적립 exit=%d: %s", code, out.String())
	}
	aID := idOfTopic(t, src, "v1201 rt alpha")
	bID := idOfTopic(t, src, "v1201 rt beta")
	if err := src.InsertKnowledgeLink(ctx, aID, bID, "refines"); err != nil {
		t.Fatalf("수출측 링크 적립: %v", err)
	}
	rows, err := ExportRows(ctx, src, ExportFilter{})
	if err != nil {
		t.Fatalf("ExportRows: %v", err)
	}
	links, err := ExportLinkRows(ctx, src)
	if err != nil {
		t.Fatalf("ExportLinkRows: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("수출 links=%d want 1", len(links))
	}
	doc, err := FormatPAM(rows, "flint-test", links...)
	if err != nil {
		t.Fatalf("FormatPAM: %v", err)
	}

	dst := t5ScratchStore(t)
	var inOut strings.Builder
	if code := RunImportMigrate(ctx, dst, []string{"--format", "pam"}, strings.NewReader(string(doc)), &inOut); code != 0 {
		t.Fatalf("수입 exit=%d: %s", code, inOut.String())
	}
	if !strings.Contains(inOut.String(), `"links":1`) {
		t.Fatalf("수입 요약에 links 누락: %s", inOut.String())
	}
	if n := linkTripleCount(t, dst); n != 1 {
		t.Fatalf("수입 knowledge_links=%d행 want 1 (round-trip row parity)", n)
	}
}
