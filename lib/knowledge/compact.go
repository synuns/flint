package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/wire"
)

const compactMaxBytes = 8 << 20

type compactHook struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	CompactSummary string `json:"compact_summary"`
}

func RunCompact(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {

	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(i18n.T("err.unknown_flag")+" — "+i18n.T("usage.compact"), a)))
			return 3
		}
	}
	if st == nil {
		return 0
	}

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

	if h.SessionID != "" {

		if err := writeTypedMarker(markerKindDistill, h.SessionID, "compact"); err != nil {
			fmt.Fprintf(os.Stderr, "[flint] compact marker: %v\n", err)
		}
	}

	maybeSessionKnowledge(ctx, st, h.CompactSummary, "claude", h.SessionID, 0)
	return 0
}

func init() {
	wire.Register("compact", RunCompact)
}
