package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/query"
)

func TestHotHeadline(t *testing.T) {
	if got := hotHeadline("normal", 20, 7, 3, nil); got != "[flint] active 20 · pending 7 · recent decisions 3 — expand: flint hot --full" {
		t.Errorf("normal 헤드라인 = %q", got)
	}
	if got := hotHeadline("minimal", 20, 7, 3, []string{"x"}); got != "[flint] active 20 · pending 7" {
		t.Errorf("minimal 헤드라인 = %q", got)
	}
	if got := hotHeadline("other", 1, 2, 3, nil); !strings.Contains(got, "expand") {
		t.Errorf("미확인 프로파일 = normal 취급 아님: %q", got)
	}
}

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
		}
		return nil, nil
	}
}

func TestHotSummaryHeadlineByProfile(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{
		{"project", "p1", "active", "2026-10-23"},
		{"pending", "토픽", "요약", "2026-10-22"},
		{"pending", "토픽2", "요약2", "2026-10-21"},
		{"decision", "토픽3", "요약3", "2026-10-20"},
	}
	cases := []struct{ name, profile, want string }{
		{"미설정 normal", "", "[flint] active 1 · pending 2 · recent decisions: 토픽3 — recall: flint read <query>"},
		{"minimal", "minimal", "[flint] active 1 · pending 2"},
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

	var out2 bytes.Buffer
	f2 := &fakeStore{queryFn: hotProfileQueryFn("minimal", hotRows)}
	if code := runHot(ctx, f2, []string{"--session", "S1", "--full"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("runHot --session --full exit = %d, want 0", code)
	}
	if out2.String() != out.String() {
		t.Fatalf("--session 병용 시 --full 출력 상이: %q vs %q", out2.String(), out.String())
	}
}
