// profile.go — exposure profile: reads meta exposure_profile.
// The profile branches only hot and nudge behavior (other commands and exit contracts unchanged).
// Profile determination — from recommendation to switching. profile_hint
// (report) was guidance only; this completes it into an actionable circuit. The determination rules share
// a single source with profileHint (sample floor zero_near_min_read and recall_led_pct
// 50 thresholds — documented in METRICS §3). Supporting metrics are exposed alongside so users can verify
// the determination (observability transparency — the GSM principle).
// The reading helpers (ExposureKey, ExposureProfile) moved to lib/wire/profile.go
// — this file owns the determination command runProfile and cli compatibility aliases.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// Aliases — re-exports of the wire-moved pieces (the setup.go write path, the ops_alias_2 seam,
// profile_v0110_test compatibility).
const ExposureKey = wire.ExposureKey

var ExposureProfile = wire.ExposureProfile

func init() { Register("profile", runProfile) }

// runProfile — `flint profile [--apply normal|minimal]`.
// No args: current profile + supporting metrics + recommendation. --apply: commits the switch
// (reuses setup --exposure — single source for the write path).
// Invalid --apply values and unknown flags are invalid-arg exit 3 (consistent with
// setup --exposure invalid-value exit 3).
func runProfile(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// moved the manual loop to the wire.ParseArgs spec table. Positional values
	// normal|minimal stay allowed (other positional tokens remain unknown
	// flags as before). When --apply and a positional value are both given, args order wins (inheriting
	// the existing overwrite-on-encounter semantics — reproduced in the merge test).
	pres, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--apply", Value: true},
		},
		Usage:           "usage: flint profile [--apply normal|minimal]",
		AllowPositional: true,
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	apply := ""
	// merges positional tokens and flag occurrences by args index (preserving error and overwrite order).
	type step struct {
		idx      int
		flag     string // "" = positional token
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
		// positional allowed (value only, without --apply) — other positional tokens are unknown flags.
		if s.val != "normal" && s.val != "minimal" {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("unknown flag: %s — usage: flint profile [--apply normal|minimal]", s.val)))
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
	// determination: reuses the report effect computation (window fixed at 14 days — the standard determination window).
	// computation failure demotes to a no-evidence determination (§13 — reporting invariant).
	var out reportOutput
	_ = collectEffect(ctx, st, 14, &out)
	th := writeThresholds(ctx, st)
	getKV, _ := metaKV(ctx, st)
	current := ExposureProfile(getKV)
	hint := profileHint(th, out.Effect)
	recommend, reason := "", ""
	switch {
	// determined via the ops.ProfileHint (English) marker — standard wording,
	// "insufficient sample"·"keep normal"·"low recall lead"(lib/ops
	// single source in report.go). low recall lead is the minimal default branch.
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
		// supporting metrics — only the signals used for the determination (report is the source for full metrics).
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
