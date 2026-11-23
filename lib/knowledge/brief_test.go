package knowledge

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/query"
)

func briefRankedStore(rows [][]any) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return rows, nil
		}
		return nil, nil
	}}
}

func TestBriefRanksByEffectiveScore(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	rows := [][]any{

		{id(5), "저점", "요약5", ":a:", nil, "main", 0.9, "active", int32(1), int32(2), nil},
		{id(3), "고점미회상", "요약3", ":b:", nil, "main", 0.9, "active", int32(1), int32(8), nil},
		{id(7), "고점감쇠", "요약7", ":c:", nil, "main", 0.9, "active", int32(1), int32(10), now.Add(-10 * 24 * time.Hour)},
	}
	var out bytes.Buffer
	if code := RunBrief(ctx, briefRankedStore(rows), nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit = %d: %s", code, out.String())
	}
	wantOrder := []string{"고점미회상", "저점", "고점감쇠"}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	for i, want := range wantOrder {
		if i+1 >= len(lines) || !strings.Contains(lines[i+1], want) {
			t.Errorf("본문 행 %d = %v, want %q 포함(유효 점수 내림 8·2·0)", i, lines, want)
		}
	}
}

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

func TestBriefUnrecalledKeepsScore(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	rows := [][]any{
		{id(1), "회상있음", "요약", ":a:", nil, "main", 0.9, "active", int32(1), int32(6), now.Add(-3 * 24 * time.Hour)},
		{id(2), "미회상", "요약", ":b:", nil, "main", 0.9, "active", int32(1), int32(6), nil},
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

	if strings.Contains(got, "recall_score") || strings.Contains(got, "last_recalled_at") {
		t.Errorf("v11 직접 열 참조 잔존: %s", got)
	}
	if !strings.HasSuffix(got, base[strings.LastIndex(base, " ORDER BY"):]) {
		t.Errorf("ORDER BY·LIMIT 절 미승계: %s", got)
	}
	if strings.Count(got, "$") != strings.Count(base, "$") {
		t.Errorf("placeholder 수 변화 = %q (원본 %q)", got, base)
	}
	if len(args) != 4 {
		t.Errorf("args = %v, want 4개(치환은 인자 불변)", args)
	}
	if got := briefRankedSQL("SELECT x FROM other"); got != "SELECT x FROM other" {
		t.Errorf("치환점 미발견 시 원본 반환 위반: %s", got)
	}
}

func TestBriefVolImmutableExempt(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	rows := [][]any{
		{id(9), "면제고점", "요약9", ":adr::vol:immutable:", nil, "main", 0.9, "active", int32(1), int32(10), now.Add(-10 * 24 * time.Hour)},
		{id(4), "조건부감쇠", "요약4", ":vol:conditional:", nil, "main", 0.9, "active", int32(1), int32(10), now.Add(-10 * 24 * time.Hour)},
		{id(3), "고점미회상", "요약3", ":b:", nil, "main", 0.9, "active", int32(1), int32(8), nil},
	}
	var out bytes.Buffer
	if code := RunBrief(ctx, briefRankedStore(rows), nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit = %d: %s", code, out.String())
	}
	wantOrder := []string{"면제고점", "고점미회상", "조건부감쇠"}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	for i, want := range wantOrder {
		if i+1 >= len(lines) || !strings.Contains(lines[i+1], want) {
			t.Errorf("본문 행 %d = %v, want %q 포함(면제 10·8·감쇠 0 순)", i, lines, want)
		}
	}
}

func id(n int) any { return int32(n) }
