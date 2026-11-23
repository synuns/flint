package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

const ExposureKey = wire.ExposureKey

var ExposureProfile = wire.ExposureProfile

func init() { Register("profile", runProfile) }

func runProfile(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {

	pres, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--apply", Value: true},
		},
		Usage:           i18n.T("usage.profile"),
		AllowPositional: true,
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	apply := ""

	type step struct {
		idx      int
		flag     string
		val      string
		hasValue bool
	}
	steps := make([]step, 0, len(pres.AllOccs())+len(pres.PosToks()))
	for _, o := range pres.AllOccs() {
		steps = append(steps, step{idx: o.Index, flag: o.Name, val: o.Value, hasValue: o.HasValue})
	}
	for _, p := range pres.PosToks() {
		steps = append(steps, step{idx: p.Index, val: p.Value, hasValue: true})
	}
	sort.Slice(steps, func(a, b int) bool { return steps[a].idx < steps[b].idx })
	for _, s := range steps {
		if s.flag == "--apply" {
			if !s.hasValue || (s.val != "normal" && s.val != "minimal") {
				fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("profile: --apply requires normal|minimal")))
				return 3
			}
			apply = s.val
			continue
		}

		if s.val != "normal" && s.val != "minimal" {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(i18n.T("err.unknown_flag")+" — "+i18n.T("usage.profile"), wire.MaskToken(s.val))))
			return 3
		}
		apply = s.val
	}
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if apply != "" {
		return runSetupExposure(ctx, st, apply, stdout)
	}

	var out reportOutput
	_ = collectEffect(ctx, st, 14, &out)
	th := writeThresholds(ctx, st)
	getKV, _ := metaKV(ctx, st)
	current := ExposureProfile(getKV)
	hint := profileHint(th, out.Effect)
	recommend, reason := "", ""
	switch {

	case strings.Contains(hint, "insufficient sample"):
		recommend, reason = "keep", "insufficient sample (observe first — read_events_n < zero_near_min_read)"
	case strings.Contains(hint, "keep normal"):
		recommend, reason = "normal", "recall-led majority (recall_led_pct >= 50)"
	default:
		recommend, reason = "minimal", "recall-led minority (recall_led_pct < 50) — a usage pattern suited to slimmer hot headlines and nudge suppression"
	}
	res := map[string]any{
		"status":    "ok",
		"current":   current,
		"recommend": recommend,
		"reason":    reason,
		"hint":      hint,

		"evidence": map[string]int{
			"read_events_n":  out.Effect.ReadEventsN,
			"recall_led_pct": out.Effect.RecallLedPct,
			"fallback_pct":   out.Effect.FallbackPct,
			"pair_pct":       out.Effect.PairPct,
		},
	}
	if (recommend == "normal" || recommend == "minimal") && recommend != current {
		res["next"] = "flint profile --apply " + recommend
	}
	b, err := json.Marshal(res)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
