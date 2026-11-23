package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

const NudgePendingLine = "flint: distillation pending — flint nudge"

const NudgeReadHint = "run flint read <query> before working — check existing knowledge"

const NudgeRecallStartLine = "flint: last session only wrote, never recalled — start with flint read <query>"

const NudgeReasonRecall = "recall"

var ExposureProfileFn func(getKV func(string) (string, error)) string

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

type nudgeStdin struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	StopHookActive bool   `json:"stop_hook_active"`
}

func RunNudge(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {

	for _, a := range args {
		if a == "--clear" {
			_ = ClearMarker()
			return 0
		}
		if strings.HasPrefix(a, "-") {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(i18n.T("err.unknown_flag")+" — "+i18n.T("usage.nudge"), wire.MaskToken(a))))
			return 3
		}
	}

	distill, derr := ReadTypedMarker(MarkerKindDistill)
	recall, rerr := ReadTypedMarker(MarkerKindRecall)

	legacyRecall := false
	if distill != nil && distill.Reason == NudgeReasonRecall && recall == nil {
		recall, distill, legacyRecall = distill, nil, true
	}

	_ = derr
	_ = rerr
	if distill == nil && recall == nil {
		return 0
	}

	if NudgeProfile(ctx, st) == "minimal" {
		if recall != nil {
			_ = ClearTypedMarker(MarkerKindRecall)
		}
		if distill != nil {
			_ = ClearMarker()
		}
		return 0
	}

	var in nudgeStdin
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return 0
	}

	switch in.HookEventName {
	case "Stop":

		if distill == nil {
			return 0
		}

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

		knowledge.BumpMetaCounter(ctx, st, "fired_nudge_distill")
	case "SessionStart":

		if recall != nil {
			fmt.Fprintln(stdout, NudgeRecallStartLine)

			knowledge.BumpMetaCounter(ctx, st, "fired_nudge_recall")
			if legacyRecall {
				_ = ClearMarker()
			} else {
				_ = ClearTypedMarker(MarkerKindRecall)
			}
		}
		if distill != nil {

			line := NudgePendingLine + " · " + NudgeReadHint
			if n, ok := pendingCount(ctx, st); ok {
				if max := knowledge.WriteThresholds(ctx, st).PendingMax; n > max {
					line = fmt.Sprintf("flint: %d items pending distillation (limit %d) — flint maintain --dup-suspects · %s", n, max, NudgeReadHint)
				}
			}
			fmt.Fprintln(stdout, line)
		}
	}
	return 0
}

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
