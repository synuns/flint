package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
)

func t5WiringScratch(t *testing.T) Store {
	t.Helper()
	dsn, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	base, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	t.Cleanup(base.Close)
	name := fmt.Sprintf("flint_t5wire_%d", time.Now().UnixNano()%1e9)
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	t.Cleanup(func() {
		var last error
		for i := 0; i < 3; i++ {
			if _, last = base.Exec(ctx, "DROP DATABASE IF EXISTS "+name); last == nil {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Logf("스크래치 DB 잔존(%q): %v", name, last)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	u.Path = "/" + name
	st, err := newRealStore(ctx, u.String())
	if err != nil {
		t.Fatalf("newRealStore(scratch): %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

func TestWriteRelationsRealStoreSlow(t *testing.T) {
	st := t5WiringScratch(t)
	ctx := context.Background()
	if code := t5RunWrite(t, st,
		`{"topic":"합성 배선 A","summary":"배선 회귀 합성 본문","confidence":0.9}`); code != 0 {
		t.Fatalf("A 적립 exit=%d", code)
	}
	idA := t5WireIDOf(t, st, "합성 배선 A")
	if code := t5RunWrite(t, st,
		fmt.Sprintf(`{"topic":"합성 배선 B","summary":"배선 회귀 합성 본문","confidence":0.9,"relations":[{"to":%d,"relation":"refines"}]}`, idA)); code != 0 {
		t.Fatalf("B 적립(relations) exit=%d idA=%d", code, idA)
	}
	rows, err := st.Query(ctx, `SELECT from_id, to_id, relation FROM knowledge_links`)
	if err != nil {
		t.Fatalf("links 조회 실패: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("프로덕션 링크 적립 수=%d, want 1 (rows=%v)", len(rows), rows)
	}
}

func TestExportPAMLinksWiringRealSlow(t *testing.T) {
	st := t5WiringScratch(t)

	body1 := t5RunExport(t, st, "--format", "pam")
	if rels := t5PAMRelations(t, body1); len(rels) != 0 {
		t.Fatalf("links 없는 문서 relations=%v, want empty", rels)
	}

	t5RunWrite(t, st, `{"topic":"합성 배선 수출 A","summary":"수출 회귀 합성 본문","confidence":0.9}`)
	idA := t5WireIDOf(t, st, "합성 배선 수출 A")
	t5RunWrite(t, st,
		fmt.Sprintf(`{"topic":"합성 배선 수출 B","summary":"수출 회귀 합성 본문","confidence":0.9,"relations":[{"to":%d,"relation":"supports"}]}`, idA))
	body2 := t5RunExport(t, st, "--format", "pam")
	rels := t5PAMRelations(t, body2)
	if len(rels) != 1 {
		t.Fatalf("links 있는 문서 relations=%v, want 1", rels)
	}
	r := rels[0]
	if r.Type != "supports" || r.From == "" || r.To == "" || r.From == r.To {
		t.Fatalf("수출 relation=%+v, want supports from≠to", r)
	}
}

func t5RunWrite(t *testing.T, st Store, stdin string) int {
	t.Helper()
	var out strings.Builder
	return knowledge.RunWrite(context.Background(), st, []string{"knowledge"},
		strings.NewReader(stdin), &out)
}

func t5RunExport(t *testing.T, st Store, args ...string) string {
	t.Helper()
	saved := exportStderr
	exportStderr = io.Discard
	t.Cleanup(func() { exportStderr = saved })
	var buf strings.Builder
	if code := runExport(context.Background(), st, args, nil, &buf); code != 0 {
		t.Fatalf("export %v exit=%d body=%s", args, code, buf.String())
	}
	return buf.String()
}

type t5PAMRelation struct {
	Type string `json:"type"`
	From string `json:"from"`
	To   string `json:"to"`
}

func t5PAMRelations(t *testing.T, body string) []t5PAMRelation {
	t.Helper()
	var doc struct {
		Relations []t5PAMRelation `json:"relations"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("문서 파싱 실패: %v (body=%d bytes)", err, len(body))
	}
	return doc.Relations
}

func t5WireIDOf(t *testing.T, st Store, topic string) int {
	t.Helper()
	rows, err := st.Query(context.Background(), `SELECT id FROM knowledge WHERE topic = $1`, topic)
	if err != nil || len(rows) == 0 {
		t.Fatalf("행 조회 실패 topic=%s err=%v rows=%d", topic, err, len(rows))
	}
	n, _ := render.CellToInt(rows[0][0])
	return n
}

func TestExportMDChainsWiringRealSlow(t *testing.T) {
	st := t5WiringScratch(t)
	ctx := context.Background()
	t5RunWrite(t, st, `{"topic":"합성 체인 A","summary":"체인 회귀 합성 본문","confidence":0.9}`)
	idA := t5WireIDOf(t, st, "합성 체인 A")

	body1 := t5RunExport(t, st, "--format", "md")
	if strings.Contains(body1, "→ #") || strings.Contains(body1, "(depth ") {
		t.Fatalf("체인 없는 md에 표기 존재: %s", body1)
	}

	t5RunWrite(t, st, `{"topic":"합성 체인 B","summary":"체인 회귀 합성 본문","confidence":0.9}`)
	idB := t5WireIDOf(t, st, "합성 체인 B")
	if _, err := st.Query(ctx, `UPDATE knowledge SET superseded_by = $1 WHERE id = $2`, idB, idA); err != nil {
		t.Fatalf("superseded_by 설정 실패: %v", err)
	}
	body2 := t5RunExport(t, st, "--format", "md")
	wantArrow := fmt.Sprintf("→ #%d", idB)
	if !strings.Contains(body2, wantArrow) {
		t.Fatalf("체인 표기 부재(%q): %s", wantArrow, body2)
	}
	if !strings.Contains(body2, "(depth 1)") {
		t.Fatalf("depth 표기 부재: %s", body2)
	}
}
