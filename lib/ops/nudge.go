// nudge.go — flint nudge: nudges the pending-distillation marker left by PostCompact at the
// Stop hook and reminds via a plain notice at SessionStart. Silent exit 0 on every failure
// (non-interference). Infinite-nudge defense = the stop_hook_active first gate + the one-shot marker (consumed via --clear).
// Exposure-profile branching — normal is 1 short line; minimal, with no output,
// auto-concludes the marker (compact has already accrued pending — no re-accrual, ruling).
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

// NudgePendingLine — the short directive of the Stop nudge's additionalContext and
// the 1-line SessionStart plain notice (the same wording).
const NudgePendingLine = "flint: distillation pending — flint nudge"

// NudgeReadHint — the read-promotion hint merged into the SessionStart notice.
const NudgeReadHint = "run flint read <query> before working — check existing knowledge"

// NudgeRecallStartLine — the 1-line SessionStart notice for the missing-recall marker
// (recall-hint.json). When done leaves a marker for the state of 0 recalls today with
// writes at the threshold, the next session start shows it once and auto-consumes it.
// The separate file (recall-hint.json) — the pending-distillation marker (pending-distill,
// durable) has a different lifecycle; sharing a file meant they overwrote each other.
const NudgeRecallStartLine = "flint: last session only wrote, never recalled — start with flint read <query>"

// NudgeReasonRecall — the Reason value of done's missing-recall marker (diagnostic display).
const NudgeReasonRecall = "recall"

// ExposureProfileFn — the exposure-profile determination seam. The real implementation (cli
// profile.go) is injected by the cli init, and the cli tests' var swap propagates via the
// ops_alias_2 wrapper reading it at call time (composePsFn).
var ExposureProfileFn func(getKV func(string) (string, error)) string

// NudgeProfile — st's exposure profile. st nil, lookup failure, or unset means normal
// (a profile lookup failure is normal).
func NudgeProfile(ctx context.Context, st wire.Store) string {
	if st == nil || ExposureProfileFn == nil {
		return "normal"
	}
	g := knowledge.MetaGetKV(ctx, st)
	p := ExposureProfileFn(func(k string) (string, error) { return g(k, "") })
	if p == "" {
		return "normal"
	}
	return p
}

// nudgeStdin — the fields of the Claude Code hook stdin that nudge looks at.
type nudgeStdin struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	StopHookActive bool   `json:"stop_hook_active"`
}

// RunNudge — the nudge command. Always returns 0 (non-interference).
// The distillation marker (pending-distill, durable) and the recall marker
// (recall-hint, one-shot) are consumed in parallel from independent files — Stop nudges only
// the distillation one (closing the defect of mistaking the recall marker for distillation,
// producing a bogus nudge and wiping the recall notice via --clear); SessionStart announces both (the recall one is shown once and auto-consumed).
func RunNudge(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// --clear: consume the marker and exit with no output (errors silent too). The manual path is unchanged (R3).
	// an unknown `-`-prefixed argument is invalid-arg exit 3 — abolishing the old silent
	// ignore (exit 0) (no silent success). Hooks are invoked with no arguments, so the normal
	// hook path (§13 non-interference) is unchanged.
	for _, a := range args {
		if a == "--clear" {
			_ = ClearMarker()
			return 0
		}
		if strings.HasPrefix(a, "-") {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("unknown flag: %s — usage: flint nudge [--clear]", a)))
			return 3
		}
	}

	distill, derr := ReadTypedMarker(MarkerKindDistill)
	recall, rerr := ReadTypedMarker(MarkerKindRecall)
	// a legacy deployment recorded recall into pending-distill.json —
	// residue identified by Reason and treated as recall. Consumption deletes from the file
	// it was recorded in (the distillation file).
	legacyRecall := false
	if distill != nil && distill.Reason == NudgeReasonRecall && recall == nil {
		recall, distill, legacyRecall = distill, nil, true
	}
	// corruption (derr, rerr) is ignored if any marker was read; proceed.
	_ = derr
	_ = rerr
	if distill == nil && recall == nil {
		return 0 // both absent or corrupt — silent either way
	}

	// minimal: zero agent exposure — no output, only the marker auto-conclusion. A conclusion
	// failure is silently skipped (the marker stays). It precedes stdin parsing (including auto-concluding on empty stdin).
	if NudgeProfile(ctx, st) == "minimal" {
		if recall != nil {
			_ = ClearTypedMarker(MarkerKindRecall)
		}
		if distill != nil {
			_ = ClearMarker()
		}
		return 0
	}

	// a stdin parse failure is silently skipped (non-interference).
	var in nudgeStdin
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return 0
	}

	switch in.HookEventName {
	case "Stop":
		// with only the recall marker, Stop is silent — the next SessionStart consumes it.
		if distill == nil {
			return 0
		}
		// if Stop has already been blocked once (stop_hook_active), do not re-nudge.
		if in.StopHookActive {
			return 0
		}
		b, jerr := json.Marshal(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":     "Stop",
				"additionalContext": NudgePendingLine,
			},
		})
		if jerr != nil {
			return 0
		}
		fmt.Fprintln(stdout, string(b))
		// fired counter — the Stop nudge (distill) actually firing once.
		knowledge.BumpMetaCounter(ctx, st, "fired_nudge_distill")
	case "SessionStart":
		// missing recall: show the dedicated wording once, then auto-consume — without a separate
		// action (--clear); once the next session responds with a read, it is done (one-shot notice semantics).
		if recall != nil {
			fmt.Fprintln(stdout, NudgeRecallStartLine)
			// fired counter — the missing-recall notice actually firing once.
			knowledge.BumpMetaCounter(ctx, st, "fired_nudge_recall")
			if legacyRecall {
				_ = ClearMarker() // legacy: consume from the file it was recorded in (pending-distill)
			} else {
				_ = ClearTypedMarker(MarkerKindRecall)
			}
		}
		if distill != nil {
			// merge the read hint onto the same line after the existing notice (keeping the
			// 1-plain-line contract). Budget visibility (the Letta in-band convention): attach the
			// pending count/cap as data (only when over the PendingMax threshold — the default wording is unchanged).
			line := NudgePendingLine + " · " + NudgeReadHint
			if n, ok := pendingCount(ctx, st); ok {
				if max := knowledge.WriteThresholds(ctx, st).PendingMax; n > max { // the excess contract (aligned with maintain and sessionhook)
					line = fmt.Sprintf("flint: %d items pending distillation (limit %d) — flint maintain --dup-suspects · %s", n, max, NudgeReadHint)
				}
			}
			fmt.Fprintln(stdout, line)
		}
	}
	return 0
}

// pendingCount — the pending knowledge count (a lookup failure or absence is ok=false —
// silent, §13). The lookup itself delegates to the single countPending implementation.
func pendingCount(ctx context.Context, st wire.Store) (int, bool) {
	if st == nil {
		return 0, false
	}
	n, err := knowledge.CountPending(ctx, st)
	if err != nil {
		return 0, false
	}
	return n, true
}
