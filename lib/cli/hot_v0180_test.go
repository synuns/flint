package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestHotHeadlineTopics(t *testing.T) {
	withT := []string{"doltgres 포트 충돌", "CI 멱등 가드", "seed DSN 가드"}
	got := hotHeadline("normal", 5, 2, 3, withT)
	want := "[flint] active 5 · pending 2 · recent decisions: doltgres 포트 충돌 · CI 멱등 가드 · seed DSN 가드 — recall: flint read <query>"
	if got != want {
		t.Fatalf("헤드라인=%q\nwant=%q", got, want)
	}

	if got := hotHeadline("normal", 5, 2, 0, nil); got != "[flint] active 5 · pending 2 · recent decisions 0 — expand: flint hot --full" {
		t.Fatalf("빈 주제 폴백=%q", got)
	}

	if got := hotHeadline("minimal", 5, 2, 3, withT); got != "[flint] active 5 · pending 2" {
		t.Fatalf("minimal=%q", got)
	}
}

func TestHotTopicsFromRows(t *testing.T) {
	long := strings.Repeat("주", 20)
	rows := [][]any{
		{"project", "p1", "s", "2026-11-06"},
		{"decision", "최신 결정", "s", "2026-11-06"},
		{"decision", "최신 결정", "s2", "2026-11-05"},
		{"decision", long, "s", "2026-11-04"},
		{"decision", "네번째", "s", "2026-11-03"},
		{"pending", "p2", "s", "2026-11-02"},
	}
	got := hotTopics(rows, 3)
	if len(got) != 3 {
		t.Fatalf("주제 수=%d, want 3: %v", len(got), got)
	}
	if got[0] != "최신 결정" {
		t.Fatalf("첫 주제=%q, want 최신순", got[0])
	}
	if got[1] != string([]rune(long)[:16])+"…" {
		t.Fatalf("절산 주제=%q, want 16rune+…", got[1])
	}
	if got[2] != "네번째" {
		t.Fatalf("셋째 주제=%q", got[2])
	}
}

func TestHotHeadlineRunPath(t *testing.T) {
	hotRows := [][]any{
		{"decision", "검색 품질 트리거", "요약", "2026-11-05"},
		{"project", "p1", "active", "2026-11-04"},
	}
	var out bytes.Buffer
	f := &fakeStore{queryFn: hotProfileQueryFn("", hotRows)}
	if code := runHot(context.Background(), f, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot exit=%d", code)
	}
	want := "[flint] active 1 · pending 0 · recent decisions: 검색 품질 트리거 — recall: flint read <query>"
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("헤드라인=%q\nwant=%q", got, want)
	}
}
