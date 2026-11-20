// compact.go — flint compact: the PostCompact hook entry. Accrues the hook
// JSON's compaction summary automatically as a session row (fixed --source
// hook) and leaves a distillation-pending marker. On any failure: exit 0 and
// an empty stdout line (§13 non-interference).
package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/synuns/flint/lib/wire"
)

// compactMaxBytes — the stdin hook JSON cap. Matches the update metadata 8MiB
// convention (lib/cli/update.go) — non-interference defense in the same
// direction as the mcp 4MB line cap. Oversized input is a silent no-op, not
// an error (§13).
const compactMaxBytes = 8 << 20 // 8MiB

// compactHook — the JSON schema the PostCompact hook passes on stdin.
type compactHook struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	CompactSummary string `json:"compact_summary"`
}

// RunCompact — parse the stdin hook JSON → if the summary is non-empty,
// accrue via RunWrite (session, --source hook) → on success + non-empty
// session_id, WriteMarker. Parse failure, empty summary, or nil st are
// no-ops; the return is always 0 and nothing is written to stdout (sole
// exception: unknown `-`-prefixed args get the invalid-arg exit 3 — hooks
// invoke with no args, so the normal hook path is unchanged).
func RunCompact(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// Unknown `-`-prefixed args get the invalid-arg exit 3 (no silent
	// success). Hooks are invoked with no args, so the normal hook path
	// (§13 non-interference) is unchanged.
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("unknown flag: %s — usage: flint compact (hook entry — no flags)", a)))
			return 3
		}
	}
	if st == nil {
		return 0
	}
	// Read at most cap+1 bytes instead of an unbounded io.ReadAll. Anything
	// over is a no-op exit 0 before parsing (non-interference — aligned with
	// the update 8MiB and mcp 4MB caps).
	b, err := io.ReadAll(io.LimitReader(stdin, compactMaxBytes+1))
	if err != nil {
		return 0
	}
	if int64(len(b)) > compactMaxBytes {
		return 0
	}
	var h compactHook
	if err := json.Unmarshal(b, &h); err != nil {
		return 0
	}
	if strings.TrimSpace(h.CompactSummary) == "" {
		return 0
	}
	// Accrual payload: the single summary normalized into a session row.
	// RunWrite (readPayloads) accepts only line-delimited JSON objects
	// (JSONL), so serialize as one object, not an array (an array would fail
	// parsing and no-op).
	payload, err := json.Marshal(map[string]any{
		"summary": h.CompactSummary,
		"agent":   "claude",
		"ts":      time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return 0
	}
	code := RunWrite(ctx, st, []string{"session", "--source", "hook"},
		strings.NewReader(string(payload)), io.Discard)
	if code != 0 {
		return 0
	}
	// Accrual succeeded + session identifiable → marker for the Stop hook
	// nudge. Failure is quiet as a one-line stderr warning (done pattern,
	// non-interference §13). This comes before session knowledgization:
	// if maybeSessionKnowledge accrues, its marker overwrites this file
	// (same markerKindDistill), and on skip (length·dup·frequency) this
	// compact marker survives. Either way the distillation nudge persists.
	if h.SessionID != "" {
		// WriteMarker (cli marker.go) goes through the seam — the distill
		// kind and reason "compact" match the cli WriteMarker delegation
		// contract.
		if err := writeTypedMarker(markerKindDistill, h.SessionID, "compact"); err != nil {
			fmt.Fprintf(os.Stderr, "[flint] compact marker: %v\n", err)
		}
	}
	// The standard path for session knowledgization — per the Claude Code
	// spec, PostCompact is the only hook that actually delivers
	// compact_summary (Stop stdin has no such field). Besides the session
	// row (the metering source), accrue a pending knowledge candidate —
	// frequency caps and dedup stay with maybeSessionKnowledge.
	maybeSessionKnowledge(ctx, st, h.CompactSummary, "claude", h.SessionID, 0) // 0=fall-back lookup (unlike done, the session row id is not yet known)
	return 0
}

func init() {
	wire.Register("compact", RunCompact)
}
