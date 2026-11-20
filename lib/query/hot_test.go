package query

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestHotSQLQueriesView — HotSQL must query hot_view newest-first with the token-budget LIMIT baked in.
func TestHotSQLQueriesView(t *testing.T) {
	sql := HotSQL("")
	if !strings.Contains(sql, "FROM hot_view") {
		t.Errorf("HotSQL()이 hot_view를 조회하지 않음: %s", sql)
	}
	if !strings.Contains(strings.ToUpper(sql), "ORDER BY") {
		t.Errorf("HotSQL()에 ORDER BY 누락: %s", sql)
	}
	if !strings.Contains(strings.ToUpper(sql), "LIMIT") {
		t.Errorf("HotSQL()에 LIMIT(토큰 예산) 누락: %s", sql)
	}
	// the tie-break for ties (CI 39713526850): with planner-arbitrary order among tied ts,
	// the injection set and the increment set (hot.go hotKnowledgeIDsSQL) diverge —
	// hot_view's trailing id must create a total order.
	if !strings.Contains(sql, "ORDER BY ts DESC, id DESC LIMIT ") {
		t.Errorf("HotSQL()에 동률 tie-break(id DESC) 누락: %s", sql)
	}
}

// TestClampHotBoundaries — the 1999/2000/2001 boundaries: at or below the cap as-is; above, truncation + suffix.
func TestClampHotBoundaries(t *testing.T) {
	const suffix = "…[truncated]"
	mk := func(n int) string { return strings.Repeat("가", n) }

	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"1999 그대로", mk(1999), 2000, mk(1999)},
		{"2000 그대로", mk(2000), 2000, mk(2000)},
		{"2001 절단+접미", mk(2001), 2000, mk(2000) + suffix},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClampHot(c.in, c.max)
			if got != c.want {
				t.Errorf("ClampHot() 길이 = %d, want %d",
					utf8.RuneCountInString(got), utf8.RuneCountInString(c.want))
			}
		})
	}
}

// TestClampHotRuneSafe — truncation must happen on rune boundaries (preventing multibyte breakage).
func TestClampHotRuneSafe(t *testing.T) {
	got := ClampHot(strings.Repeat("한", 11), 5)
	want := strings.Repeat("한", 5) + "…[truncated]"
	if got != want {
		t.Errorf("ClampHot() = %q, want %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Errorf("ClampHot() 결과가 UTF-8 유효 문자열이 아님: %q", got)
	}
}
