package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func effectSequentialOracle(ctx context.Context, st wire.Store, days int, th store.Thresholds, out *ReportOutput) error {
	now := time.Now()
	windowStart := now.Add(-time.Duration(days) * 24 * time.Hour)
	pairWindow := time.Duration(th.PairWindowMin) * time.Minute
	chainWindow := time.Duration(th.ChainWindowH) * time.Hour

	deadCutoff := now.Add(-time.Duration(th.DeadDays) * 24 * time.Hour)
	exposed := map[int]bool{}
	if rows, err := wire.QueryOnce(ctx, st,
		`SELECT ids FROM hot_events WHERE ts >= $1`, []any{deadCutoff}); err == nil {
		for _, r := range rows {
			if len(r) > 0 {
				if s, ok := r[0].(string); ok {
					for _, id := range knowledge.ParseIDsCSV(s) {
						exposed[id] = true
					}
				}
			}
		}
	}
	rows, err := wire.QueryOnce(ctx, st, `
		SELECT count(*),
		       count(CASE WHEN read_count > 0 THEN 1 END)
		  FROM knowledge
		 WHERE status = 'active' AND invalid_at IS NULL`, nil)
	if err != nil {
		return err
	}
	var active, reused int
	if len(rows) > 0 {
		ReadCounters(rows[0], &active, &reused)
	}
	out.Effect.ReusePct = PctOf(reused, active)
	deadIDs, err := wire.QueryOnce(ctx, st, `
		/* dead */ SELECT id FROM knowledge
		 WHERE status = 'active' AND invalid_at IS NULL
		   AND read_count = 0 AND ingested_at < $1`, []any{deadCutoff})
	if err != nil {
		return err
	}
	dead := 0
	for _, r := range deadIDs {
		if len(r) == 0 {
			continue
		}
		if id, ok := render.CellToInt(r[0]); ok && !exposed[id] {
			dead++
		}
	}
	out.Effect.DeadPct = PctOf(dead, active)

	fetchStart := windowStart
	if min14 := now.Add(-14 * 24 * time.Hour); min14.Before(fetchStart) {
		fetchStart = min14
	}
	evs, err := queryReadEvents(ctx, st, fetchStart)
	if err != nil {
		return nil
	}
	inWin := make([]ReadEventRow, 0, len(evs))
	for _, e := range evs {
		if !e.Ts.Before(windowStart) {
			inWin = append(inWin, e)
		}
	}
	out.Effect.ReadEventsN = len(inWin)
	fallbacks := 0
	for _, e := range inWin {
		if e.Fallback {
			fallbacks++
		}
	}
	out.Effect.FallbackPct = PctOf(fallbacks, len(inWin))
	out.FallbackPct = out.Effect.FallbackPct
	q := qualityOf(inWin)
	out.Effect.ExactPct = PctOf(q.exact, len(inWin))
	out.Effect.Top1Pct = PctOf(q.top1, q.exact)
	out.Effect.MRR = math.Round(q.rrSum/math.Max(float64(q.exact), 1)*1000) / 1000
	cur, prev := TrendWindow(now, evs)
	out.Effect.Trend = cur.Diff(prev)

	collectZeroDecompose(out, inWin)
	collectCost(ctx, st, out, inWin)
	recalls := queryKnowledgeReads(ctx, st, windowStart.Add(-reportSessionLookback))
	collectRecallFate(ctx, st, out, recalls, now)
	collectHotReadChain(out, recalls, windowStart, pairWindow)

	var writes []WriteRow
	var werr error
	if len(inWin) > 0 || len(recalls) > 0 {
		writes, werr = queryWrites(ctx, st, windowStart)
	}

	var starts []time.Time
	var serr error
	hasSessions := false
	if len(inWin) > 0 || len(recalls) > 0 {
		starts, serr = queryTimes(ctx, st,
			`SELECT ts FROM session_starts WHERE ts >= $1 ORDER BY ts`,
			windowStart.Add(-reportSessionLookback))
		hasSessions = serr == nil && len(starts) > 0
	}

	if hasSessions {
		if len(recalls) > 0 {
			recallTimes := make([]time.Time, 0, len(recalls))
			for _, r := range recalls {
				recallTimes = append(recallTimes, r.Ts)
			}
			out.Effect.RecallLedPct = recallLedPctTimes(starts, recallTimes, writes, windowStart)
		} else {
			out.Effect.RecallLedPct = recallLedPct(starts, inWin, writes, windowStart)
		}
	}
	out.Effect.ProfileHint = ProfileHint(th, out.Effect)
	if len(inWin) == 0 {
		return nil
	}
	if werr != nil {
		return nil
	}

	writeTimes := make([]time.Time, 0, len(writes))
	sessWriteTimes := map[int][]time.Time{}
	for _, w := range writes {
		writeTimes = append(writeTimes, w.Ts)
		if hasSessions {
			if si := sessionIndexOf(starts, w.Ts); si >= 0 {
				sessWriteTimes[si] = append(sessWriteTimes[si], w.Ts)
			}
		}
	}
	pairs := 0
	for _, e := range inWin {
		cands := writeTimes
		if hasSessions {
			if si := sessionIndexOf(starts, e.Ts); si >= 0 {
				cands = sessWriteTimes[si]
			}
		}
		if TimeWindowHit(cands, e.Ts, pairWindow) {
			pairs++
		}
	}
	out.Effect.ReadWritePairs = pairs
	out.Effect.PairPct = PctOf(pairs, len(inWin))

	hasIDs := false
	for _, e := range inWin {
		if len(e.Ids) > 0 {
			hasIDs = true
			break
		}
	}
	if !hasIDs {
		return nil
	}
	topics := map[int]string{}
	if rows, err := wire.QueryOnce(ctx, st,
		`SELECT id, topic FROM knowledge`, nil); err == nil {
		for _, r := range rows {
			if len(r) < 2 {
				continue
			}
			if id, ok := render.CellToInt(r[0]); ok {
				topics[id] = fmt.Sprint(r[1])
			}
		}
	}
	out.Effect.ChainPct = ChainPct(inWin, writes, topics, chainWindow)
	return nil
}

func effectSeqReportJSON(ctx context.Context, st wire.Store, days int) (string, int) {
	th := knowledge.WriteThresholds(ctx, st)
	if max := th.DailyRetentionD; max > 0 && days > max {
		days = max
	}
	out := ReportOutput{Days: make([]ReportDay, 0, days)}
	if err := collectDaily(ctx, st, days, &out); err != nil {
		return wire.ErrLine(err), store.MapExit(err)
	}
	collectCuration(ctx, st, days, &out)
	if err := collectStats(ctx, st, &out); err != nil {
		return wire.ErrLine(err), store.MapExit(err)
	}
	if err := collectCounts(ctx, st, days, th, &out); err != nil {
		return wire.ErrLine(err), store.MapExit(err)
	}
	if err := effectSequentialOracle(ctx, st, days, th, &out); err != nil {
		return wire.ErrLine(err), store.MapExit(err)
	}
	out.DupPct = DupPct(out.Days)
	for _, d := range out.Days {
		out.DupHook += d.DupHook
		out.DupManual += d.DupManual
	}
	out.Diagnosis = Diagnose(out, th, knowledge.HotBudget(knowledge.MetaGetKV(ctx, st)))
	b, err := json.Marshal(out)
	if err != nil {
		return wire.ErrLine(err), 1
	}
	return string(b) + "\n", 0
}

func TestEffectParallelJSONByteParity(t *testing.T) {
	ctx := context.Background()
	f := parallelReportFake(nil)
	var par strings.Builder
	if code := RunReport(ctx, f, []string{"--days", "3"}, nil, &par); code != 0 {
		t.Fatalf("report exit=%d: %s", code, par.String())
	}
	seq, seqExit := effectSeqReportJSON(ctx, f, 3)
	if seqExit != 0 {
		t.Fatalf("순차 오라클 exit=%d", seqExit)
	}
	if par.String() != seq {
		t.Errorf("병렬 JSON과 순차 오라클 불일치:\n par=%s\nseq=%s", par.String(), seq)
	}
}

func TestEffectParallelConcurrent(t *testing.T) {
	const want = 5
	var mu sync.Mutex
	joined := map[string]bool{}
	entered := 0
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
			return errors.New("concurrency barrier timeout — collectEffect 1상 SELECT 5종이 동시에 겹치지 않음(순차 실행)")
		}
	}
	slot := func(sql string) string {
		switch {
		case strings.Contains(sql, "FROM hot_events"):
			return "exposed"
		case strings.Contains(sql, "read_count > 0"):
			return "count"
		case strings.Contains(sql, "/* dead */"):
			return "dead"
		case strings.Contains(sql, "fallback, ids"):
			return "readEvents"
		case strings.Contains(sql, "FROM knowledge_reads"):
			return "kreads"
		}
		return ""
	}
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if pat := slot(sql); pat != "" {
			if err := join(pat); err != nil {
				return nil, err
			}
		}
		return dispatchReportQuery(sql)
	}}
	var out strings.Builder
	if code := RunReport(context.Background(), f, []string{"--days", "3"}, nil, &out); code != 0 {
		t.Fatalf("report exit=%d(배리어 미통과 — 1상 SELECT가 병렬로 겹치지 않음): %s", code, out.String())
	}
}

func TestEffectParallelErrorPriority(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		errs map[string]error
	}{
		{
			name: "복수 실패 — count가 dead보다 우선",
			errs: map[string]error{
				"read_count > 0": errors.New("count query failed"),
				"/* dead */":     errors.New("dead query failed"),
			},
		},
		{
			name: "deadIDs 단독 실패 — 전파",
			errs: map[string]error{"/* dead */": errors.New("dead query failed")},
		},
		{
			name: "readEvents 실패 — effect 전체 강등(exit 0)",
			errs: map[string]error{"fallback, ids": errors.New("read events query failed")},
		},
		{
			name: "writes 실패 — 페어링 강등(exit 0)",
			errs: map[string]error{"ingested_at >=": errors.New("writes query failed")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var par strings.Builder
			code := RunReport(ctx, parallelReportFake(tc.errs), []string{"--days", "3"}, nil, &par)
			seq, seqExit := effectSeqReportJSON(ctx, parallelReportFake(tc.errs), 3)
			if code != seqExit {
				t.Errorf("exit=%d, want %d(순차 오라클) — out=%s", code, seqExit, par.String())
			}
			if strings.TrimSpace(par.String()) != strings.TrimSpace(seq) {
				t.Errorf("출력 불일치:\n par=%s\nseq=%s", par.String(), seq)
			}
		})
	}
}
