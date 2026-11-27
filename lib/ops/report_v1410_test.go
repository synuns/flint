package ops

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCollectDailyParallelGolden(t *testing.T) {
	ctx := context.Background()
	const want = 5
	var mu sync.Mutex
	entered := 0
	joined := map[string]bool{}
	release := make(chan struct{})
	join := func(pat string) error {
		mu.Lock()
		if !joined[pat] {
			joined[pat] = true
			entered++
			if entered == want {
				close(release)
			}
		}
		mu.Unlock()
		select {
		case <-release:
			return nil
		case <-time.After(3 * time.Second):
			return errors.New("5-way barrier timeout — collectDaily가 병렬 errgroup에 편입되지 않음(선행 순차)")
		}
	}
	dailyCalls := 0
	slot := func(sql string) string {
		switch {
		case strings.Contains(sql, "LIKE 'daily:%'"):
			mu.Lock()
			dailyCalls++
			n := dailyCalls
			mu.Unlock()
			if n == 1 {
				return "daily"
			}
			if n == 2 {
				return "curation"
			}
		case strings.Contains(sql, "FROM knowledge_reads"):
			return "stats"
		case strings.Contains(sql, "status = 'pending'"):
			return "counts"
		case strings.Contains(sql, "read_count > 0"):
			return "effect"
		}
		return ""
	}
	barrier := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if pat := slot(sql); pat != "" {
			if err := join(pat); err != nil {
				return nil, err
			}
		}
		return dispatchReportQuery(sql)
	}}
	var par strings.Builder
	if code := RunReport(ctx, barrier, []string{"--days", "3"}, nil, &par); code != 0 {
		t.Fatalf("report exit=%d(collectDaily 병렬 미편입 — 배리어 미통과): %s", code, par.String())
	}

	clean := parallelReportFake(nil)
	var parClean strings.Builder
	if code := RunReport(ctx, clean, []string{"--days", "3"}, nil, &parClean); code != 0 {
		t.Fatalf("report exit=%d: %s", code, parClean.String())
	}
	seq, seqExit := reportSequentialJSON(ctx, clean, 3)
	if seqExit != 0 {
		t.Fatalf("순차 오라클 exit=%d", seqExit)
	}
	if parClean.String() != seq {
		t.Errorf("병렬 JSON과 순차 산물 불일치:\n par=%s\nseq=%s", parClean.String(), seq)
	}
}
