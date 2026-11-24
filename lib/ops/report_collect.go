package ops

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

func CollectReport(ctx context.Context, st wire.Store, days int) (ReportOutput, error) {
	if err := st.Ping(ctx); err != nil {
		return ReportOutput{}, fmt.Errorf("report: %w", err)
	}

	th := knowledge.WriteThresholds(ctx, st)
	if max := th.DailyRetentionD; max > 0 && days > max {
		days = max
	}

	out := ReportOutput{Days: make([]ReportDay, 0, days)}
	if err := collectDaily(ctx, st, days, &out); err != nil {
		return ReportOutput{}, err
	}

	var g errgroup.Group
	var statErr, countErr, effectErr error
	g.Go(func() error { collectCuration(ctx, st, days, &out); return nil })
	g.Go(func() error { statErr = collectStats(ctx, st, &out); return nil })
	g.Go(func() error { countErr = collectCounts(ctx, st, days, th, &out); return nil })
	g.Go(func() error { effectErr = collectEffect(ctx, st, days, th, &out); return nil })
	g.Wait()
	for _, err := range []error{statErr, countErr, effectErr} {
		if err != nil {
			return ReportOutput{}, err
		}
	}

	out.DupPct = DupPct(out.Days)
	for _, d := range out.Days {
		out.DupHook += d.DupHook
		out.DupManual += d.DupManual
	}

	out.Diagnosis = Diagnose(out, th, knowledge.HotBudget(knowledge.MetaGetKV(ctx, st)))
	return out, nil
}
