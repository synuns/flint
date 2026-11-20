// brief_test.go — reflecting the decay-adjusted effective recall score
// in brief selection ranking. The same contract as hot — effective DESC
// stable sort (ties·zero scores inherit the existing RankOrder),
// masking·neutralization·byte cap unchanged.
package knowledge

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/query"
)

// briefRankedStore — a fake returning rows for the BuildReadStatus (the
// ranking-column-merged form) query (meta unset — default decay
// coefficient 1·default budget 3,000).
func briefRankedStore(rows [][]any) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return rows, nil
		}
		return nil, nil
	}}
}

// TestBriefRanksByEffectiveScore — the brief body row order is the
// decay-adjusted effective score, descending.
func TestBriefRanksByEffectiveScore(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	rows := [][]any{
		// readColumns 9 columns + recall_score·last_recalled_at
		{id(5), "저점", "요약5", ":a:", nil, "main", 0.9, "active", int32(1), int32(2), nil},
		{id(3), "고점미회상", "요약3", ":b:", nil, "main", 0.9, "active", int32(1), int32(8), nil},
		{id(7), "고점감쇠", "요약7", ":c:", nil, "main", 0.9, "active", int32(1), int32(10), now.Add(-10 * 24 * time.Hour)}, // 10-10=0
	}
	var out bytes.Buffer
	if code := RunBrief(ctx, briefRankedStore(rows), nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit = %d: %s", code, out.String())
	}
	wantOrder := []string{"고점미회상", "저점", "고점감쇠"} // 8 > 2 > 0
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	for i, want := range wantOrder {
		if i+1 >= len(lines) || !strings.Contains(lines[i+1], want) { // +1: skip the header line
			t.Errorf("본문 행 %d = %v, want %q 포함(유효 점수 내림 8·2·0)", i, lines, want)
		}
	}
}

// TestBriefZeroScoresKeepExistingOrder — missing ranking columns (9
// columns)·an all-zero score set: the stable sort inherits the existing
// order and the output is byte-identical to the header+the FormatRows
// base format.
func TestBriefZeroScoresKeepExistingOrder(t *testing.T) {
	ctx := context.Background()
	base := [][]any{
		{id(3), "토픽A", "요약A", ":a:", nil, "main", 0.9, "active", int32(1)},
		{id(7), "토픽B", "요약B", ":b:", nil, "main", 0.8, "active", int32(0)},
	}
	withZero := [][]any{
		{id(3), "토픽A", "요약A", ":a:", nil, "main", 0.9, "active", int32(1), int32(0), nil},
		{id(7), "토픽B", "요약B", ":b:", nil, "main", 0.8, "active", int32(0), int32(0), nil},
	}
	for _, tc := range []struct {
		name string
		rows [][]any
	}{
		{"랭킹 열 부재(9열)", base},
		{"전원 0점", withZero},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := RunBrief(ctx, briefRankedStore(tc.rows), nil, strings.NewReader(""), &out); code != 0 {
				t.Fatalf("RunBrief exit = %d: %s", code, out.String())
			}
			want := briefHeader(len(tc.rows)) + query.FormatRows(tc.rows, "topic,summary,tags", false)
			if out.String() != want {
				t.Errorf("출력 = %q, want %q(기존 순서·포맷 불변)", out.String(), want)
			}
		})
	}
}

// TestBriefUnrecalledKeepsScore — a cnt cell present but last_at NULL (a
// fake shape — in the real-DB derived aggregate, cnt>0 always comes with
// last_at) keeps cnt as-is with no decay penalty (the guard branch of
// the EventEffectiveScore T1 contract). A real-DB never-recalled row
// (0 events) has cnt NULL → zero score (the same semantics as hot).
func TestBriefUnrecalledKeepsScore(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	rows := [][]any{
		{id(1), "회상있음", "요약", ":a:", nil, "main", 0.9, "active", int32(1), int32(6), now.Add(-3 * 24 * time.Hour)}, // 6-3=3
		{id(2), "미회상", "요약", ":b:", nil, "main", 0.9, "active", int32(1), int32(6), nil},                           // 6 kept
	}
	var out bytes.Buffer
	if code := RunBrief(ctx, briefRankedStore(rows), nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit = %d: %s", code, out.String())
	}
	idxUn := strings.Index(out.String(), "미회상")
	idxRec := strings.Index(out.String(), "회상있음")
	if idxUn < 0 || idxRec < 0 || idxUn > idxRec {
		t.Errorf("순서 = 미회상@%d 회상있음@%d, want 미회상 선행(6 > 3): %q", idxUn, idxRec, out.String())
	}
}

// TestBriefRankedSQLShape — the BuildReadStatus assembled-SQL
// substitution contract: the recall_events derived aggregate join
// (re.cnt·re.last_at) merges at the SELECT tail; WHERE·ORDER BY·LIMIT·
// placeholder numbering inherit the original (v1.9.0 T4 — the v11 direct
// columns dropped). A shape without the pivot returns the original.
func TestBriefRankedSQLShape(t *testing.T) {
	f := query.Filters{Status: "active", Limit: 5}
	base, args := query.BuildReadStatus("q", f)
	got := briefRankedSQL(base)
	for _, frag := range []string{
		"read_count, re.cnt, re.last_at FROM knowledge",
		"LEFT JOIN (SELECT knowledge_id, count(*)::int AS cnt, max(recalled_at) AS last_at FROM recall_events GROUP BY knowledge_id) re ON re.knowledge_id = knowledge.id",
		"status = $1",
		"invalid_at IS NULL",
	} {
		if !strings.Contains(got, frag) {
			t.Errorf("ranked SQL에 %q 누락: %s", frag, got)
		}
	}
	// Defense against residual v11 direct-column references.
	if strings.Contains(got, "recall_score") || strings.Contains(got, "last_recalled_at") {
		t.Errorf("v11 직접 열 참조 잔존: %s", got)
	}
	if !strings.HasSuffix(got, base[strings.LastIndex(base, " ORDER BY"):]) {
		t.Errorf("ORDER BY·LIMIT 절 미승계: %s", got)
	}
	if strings.Count(got, "$") != strings.Count(base, "$") {
		t.Errorf("placeholder 수 변화 = %q (원본 %q)", got, base)
	}
	if len(args) != 4 { // status·q·the rank-match value·limit — the substitution leaves args unchanged
		t.Errorf("args = %v, want 4개(치환은 인자 불변)", args)
	}
	if got := briefRankedSQL("SELECT x FROM other"); got != "SELECT x FROM other" {
		t.Errorf("치환점 미발견 시 원본 반환 위반: %s", got)
	}
}

// TestBriefVolImmutableExempt — the v1.13.0 T2 ranking switch on the brief
// path: the tags column is already exposed by BuildReadStatus (readColumns
// idx 3 — no SQL change), so a decay-exhausted row tagged :vol:immutable:
// keeps its count (10) and tops the body, while vol:conditional decays
// verbatim (0). Without the exemption the fresh-recalled row (8) would lead.
func TestBriefVolImmutableExempt(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	rows := [][]any{
		{id(9), "면제고점", "요약9", ":adr::vol:immutable:", nil, "main", 0.9, "active", int32(1), int32(10), now.Add(-10 * 24 * time.Hour)}, // exempt → 10
		{id(4), "조건부감쇠", "요약4", ":vol:conditional:", nil, "main", 0.9, "active", int32(1), int32(10), now.Add(-10 * 24 * time.Hour)},   // 10-10=0
		{id(3), "고점미회상", "요약3", ":b:", nil, "main", 0.9, "active", int32(1), int32(8), nil},                                            // 8
	}
	var out bytes.Buffer
	if code := RunBrief(ctx, briefRankedStore(rows), nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit = %d: %s", code, out.String())
	}
	wantOrder := []string{"면제고점", "고점미회상", "조건부감쇠"} // 10 > 8 > 0
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	for i, want := range wantOrder {
		if i+1 >= len(lines) || !strings.Contains(lines[i+1], want) { // +1: skip the header line
			t.Errorf("본문 행 %d = %v, want %q 포함(면제 10·8·감쇠 0 순)", i, lines, want)
		}
	}
}

// id — a fixture helper (the fake-cell int32 convention).
func id(n int) any { return int32(n) }
