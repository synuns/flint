// brief_test.go — brief 선정 순위에 감쇠 후 유효 회상 점수
// 반영. hot과 동일 계약 — effective DESC 안정 정렬(동점·0점은 기존
// RankOrder 순서 승계), 마스킹·중립화·바이트 상한 불변.
package knowledge

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/query"
)

// briefRankedStore — BuildReadStatus(랭킹 열 병합형) 질의에 rows를
// 반환하는 fake(meta는 미설정 — 기본 감쇠 계수 1·기본 예산 3,000).
func briefRankedStore(rows [][]any) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return rows, nil
		}
		return nil, nil
	}}
}

// TestBriefRanksByEffectiveScore — 브리프 본문 행 순서가 감쇠 후 유효 점수 내림.
func TestBriefRanksByEffectiveScore(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	rows := [][]any{
		// readColumns 9열 + recall_score·last_recalled_at
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
		if i+1 >= len(lines) || !strings.Contains(lines[i+1], want) { // +1: 헤더 줄 제외
			t.Errorf("본문 행 %d = %v, want %q 포함(유효 점수 내림 8·2·0)", i, lines, want)
		}
	}
}

// TestBriefZeroScoresKeepExistingOrder — 랭킹 열 부재(9열)·전원 0점은 안정 정렬이 기존 순서를
// 승계하고 출력은 헤더+FormatRows 기본 포맷과 바이트 동일.
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

// TestBriefUnrecalledKeepsScore — last_recalled_at NULL은 감쇠 불이익 없이 score 그대로.
func TestBriefUnrecalledKeepsScore(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	rows := [][]any{
		{id(1), "회상있음", "요약", ":a:", nil, "main", 0.9, "active", int32(1), int32(6), now.Add(-3 * 24 * time.Hour)}, // 6-3=3
		{id(2), "미회상", "요약", ":b:", nil, "main", 0.9, "active", int32(1), int32(6), nil},                           // 6 유지
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

// TestBriefRankedSQLShape — BuildReadStatus 조립 SQL 치환 계약: SELECT 말미에
// recall_score·last_recalled_at 병합, WHERE·ORDER BY·LIMIT·placeholder
// 번호는 원본 승계. 치환점 미발견 형태는 원본 반환.
func TestBriefRankedSQLShape(t *testing.T) {
	f := query.Filters{Status: "active", Limit: 5}
	base, args := query.BuildReadStatus("q", f)
	got := briefRankedSQL(base)
	for _, frag := range []string{
		"read_count, recall_score, last_recalled_at FROM knowledge",
		"status = $1",
		"invalid_at IS NULL",
	} {
		if !strings.Contains(got, frag) {
			t.Errorf("ranked SQL에 %q 누락: %s", frag, got)
		}
	}
	if !strings.HasSuffix(got, base[strings.LastIndex(base, " ORDER BY"):]) {
		t.Errorf("ORDER BY·LIMIT 절 미승계: %s", got)
	}
	if strings.Count(got, "$") != strings.Count(base, "$") {
		t.Errorf("placeholder 수 변화 = %q (원본 %q)", got, base)
	}
	if len(args) != 4 { // status·q·랭킹 정합값·limit — 치환은 인자 불변
		t.Errorf("args = %v, want 4개(치환은 인자 불변)", args)
	}
	if got := briefRankedSQL("SELECT x FROM other"); got != "SELECT x FROM other" {
		t.Errorf("치환점 미발견 시 원본 반환 위반: %s", got)
	}
}

// id — fixture 헬퍼(fake 셀 int32 관례).
func id(n int) any { return int32(n) }
