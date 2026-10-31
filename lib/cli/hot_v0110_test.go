// hot_v0110_test.go — R2 hot 요약 헤드라인·--full 플래그(v0.11.0).
// 기본 경로는 프로파일별 헤드라인 1줄, --full은 기존 전체 출력(바이트 불변).
package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"flint/lib/query"
)

func TestHotHeadline(t *testing.T) {
	if got := hotHeadline("normal", 20, 7, 3); got != "[flint] 활성 20 · 대기 7 · 최근 결정 3 — 펼치기: flint hot --full" {
		t.Errorf("normal 헤드라인 = %q", got)
	}
	if got := hotHeadline("minimal", 20, 7, 3); got != "[flint] 활성 20 · 대기 7" {
		t.Errorf("minimal 헤드라인 = %q", got)
	}
	if got := hotHeadline("other", 1, 2, 3); !strings.Contains(got, "펼치기") {
		t.Errorf("미확인 프로파일 = normal 취급 아님: %q", got)
	}
}

// hotProfileQueryFn — hot_view 본질의는 hotRows, meta exposure_profile은
// profile(빈 문자열이면 미설정=0행), 동반 질의는 id [3 7].
func hotProfileQueryFn(profile string, hotRows [][]any) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM hot_view"):
			return hotRows, nil
		case strings.Contains(sql, "FROM meta") && len(args) > 0 && fmt.Sprint(args[0]) == "exposure_profile":
			if profile == "" {
				return nil, nil
			}
			return [][]any{{profile}}, nil
		case strings.Contains(sql, "FROM projects p"):
			return [][]any{{int32(3)}, {int32(7)}}, nil
		}
		return nil, nil
	}
}

// TestHotSummaryHeadlineByProfile — Review Focus: 프로파일 미설정=normal
// 헤드라인(펼치기 안내 포함), minimal은 카운트 2개만. 카운트는 전체
// 출력과 동일 데이터 소스(hot_view 조회 행)에서 추출.
func TestHotSummaryHeadlineByProfile(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{
		{"project", "p1", "active", "2026-10-23"},
		{"pending", "토픽", "요약", "2026-10-22"},
		{"pending", "토픽2", "요약2", "2026-10-21"},
		{"decision", "토픽3", "요약3", "2026-10-20"},
	}
	cases := []struct{ name, profile, want string }{
		{"미설정 normal", "", "[flint] 활성 1 · 대기 2 · 최근 결정 1 — 펼치기: flint hot --full"},
		{"minimal", "minimal", "[flint] 활성 1 · 대기 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			f := &fakeStore{queryFn: hotProfileQueryFn(tc.profile, hotRows)}
			if code := runHot(ctx, f, nil, strings.NewReader(""), &out); code != 0 {
				t.Fatalf("runHot exit = %d, want 0", code)
			}
			if got := strings.TrimSpace(out.String()); got != tc.want {
				t.Fatalf("헤드라인 = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHotFullUnchanged — Review Focus: --full은 프로파일 무관 기존 전체
// 출력 경로. 신규 단언은 플래그 파싱 라우팅만(전체 포맷 회귀는 기존 hot
// 테스트가 담보).
func TestHotFullUnchanged(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{
		{"pending", "토픽", "요약", "2026-10-22"},
		{"decision", "토픽2", "요약2", "2026-10-21"},
	}
	var out bytes.Buffer
	f := &fakeStore{queryFn: hotProfileQueryFn("minimal", hotRows)}
	if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit = %d, want 0", code)
	}
	if want := query.FormatRows(hotRows, hotFields, false); out.String() != want {
		t.Fatalf("--full 출력 = %q, want %q(기존 전체 출력)", out.String(), want)
	}
	// --session과 --full 병용도 동일 라우팅.
	var out2 bytes.Buffer
	f2 := &fakeStore{queryFn: hotProfileQueryFn("minimal", hotRows)}
	if code := runHot(ctx, f2, []string{"--session", "S1", "--full"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("runHot --session --full exit = %d, want 0", code)
	}
	if out2.String() != out.String() {
		t.Fatalf("--session 병용 시 --full 출력 상이: %q vs %q", out2.String(), out.String())
	}
}
