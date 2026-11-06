// hot_v0180_test.go — v0.18.0 R2: normal 프로파일 헤드라인에 최근 결정
// 주제 노출 + 회상 힌트. 주입이 카운트만 주면 다음 세션이 "뭘 검색할지"
// 알 수 없어 read가 굶는다(관측: read:write 불균형) — 주제명이 검색
// 큐리로 쓰이게 한다. minimal 프로파일은 불변(비간섭 원칙).
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestHotHeadlineTopics — normal 헤드라인: 주제 K개 · 회상 힌트.
// 주제 없으면 기존 카운트+펼치기 문구 폴백(결정 0건 세션).
func TestHotHeadlineTopics(t *testing.T) {
	withT := []string{"doltgres 포트 충돌", "CI 멱등 가드", "seed DSN 가드"}
	got := hotHeadline("normal", 5, 2, 3, withT)
	want := "[flint] 활성 5 · 대기 2 · 최근 결정: doltgres 포트 충돌 · CI 멱등 가드 · seed DSN 가드 — 회상: flint read <쿼리>"
	if got != want {
		t.Fatalf("헤드라인=%q\nwant=%q", got, want)
	}
	// 주제 없음 → 기존 문구(펼치기 안내) 폴백.
	if got := hotHeadline("normal", 5, 2, 0, nil); got != "[flint] 활성 5 · 대기 2 · 최근 결정 0 — 펼치기: flint hot --full" {
		t.Fatalf("빈 주제 폴백=%q", got)
	}
	// minimal 불변.
	if got := hotHeadline("minimal", 5, 2, 3, withT); got != "[flint] 활성 5 · 대기 2" {
		t.Fatalf("minimal=%q", got)
	}
}

// TestHotTopicsFromRows — hot_view 행(최신순)에서 decision 주제 상위 K.
// 주제 절단(16 rune + …)·동일 주제 중복 제거·K 초과 분기.
func TestHotTopicsFromRows(t *testing.T) {
	long := strings.Repeat("주", 20) // 20 rune → 16+…
	rows := [][]any{
		{"project", "p1", "s", "2026-11-06"},
		{"decision", "최신 결정", "s", "2026-11-06"},
		{"decision", "최신 결정", "s2", "2026-11-05"}, // 동일 주제 중복 제거
		{"decision", long, "s", "2026-11-04"},
		{"decision", "네번째", "s", "2026-11-03"}, // K=3 초과 → 탈락
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

// TestHotHeadlineRunPath — runHot 전체 경로: normal 프로파일에서 주제가
// 실제 헤드라인에 반영된다(hot_v0110 hotProfileQueryFn 재사용).
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
	want := "[flint] 활성 1 · 대기 0 · 최근 결정: 검색 품질 트리거 — 회상: flint read <쿼리>"
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("헤드라인=%q\nwant=%q", got, want)
	}
}
