package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestReportZeroWindowed(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "daily:"):
			// 창 내(오늘) 행 + 창 밖(초과 보존분) 행 — 합산은 창 내만.
			return [][]any{
				{"daily:" + today, `{"read":10,"read_zero":4,"read_zero_near":3}`},
				{"daily:2001-01-01", `{"read":90,"read_zero":90,"read_zero_near":90}`},
			}, nil
		case strings.Contains(sql, "IN ('read_stats','hot_stats')"):
			return [][]any{{"read_stats", `{"calls":999,"zero":999}`}}, nil
		case strings.Contains(sql, "count"):
			return [][]any{{"1", "2", "3"}}, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := runReport(context.Background(), st, []string{"--days", "1"}, nil, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	s := out.String()
	for _, want := range []string{`"zero_pct":40`, `"zero_near_pct":30`} {
		if !strings.Contains(s, want) {
			t.Errorf("%s 누락(창 밖 2001-01-01 행·누적 999 오염 무시되어야): %s", want, s)
		}
	}
}
