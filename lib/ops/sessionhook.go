// sessionhook.go — unifies the SessionStart and Stop hook executions into one command
// each (session-start, session-end). Internally it runs the existing hot->nudge / done->nudge
// sequences in order on the same stdout, inheriting output, metering, and exit. The existing
// direct-run commands (hot, done, nudge) also remain valid (backward compatible).
// SessionStart records the session-start boundary into session_starts —
// the source for the effect metering's session-interval assignment (an internal account with no output, §13).
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/wire"
)

// SessionStartRecorder — the seam for recording session-start boundaries. realStore
// implements it via db.Repo.RecordSessionStart (cli realstore_care.go); an unimplemented
// Store (fake) silently skips.
type SessionStartRecorder interface {
	RecordSessionStart(ctx context.Context, session string) error
}

// sanitizeHookSession — the --session cleanup dedicated to the hook entry points
// (session-start, session-end). An invalid label (empty value, or a '-'-prefixed
// flag-absorption typo — the shared knowledge.ValidateSessionLabel guard) is treated as
// label-absent: it returns the arguments with the --session pair stripped and the remaining
// valid label. Hook stdout is a prompt-injection path, so even invalid arguments stay silent
// (non-interference §3, §13) — the validation-error exit 3 remains only in the direct CLI-call
// contract (hot, done, read). Since hooks.json's session-start and session-end pass only the
// harness session_id, this cleanup aligns the fallback (legacy and variant hooks) path.
func sanitizeHookSession(args []string) ([]string, string) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" {
			val := ""
			if i+1 < len(args) {
				val = args[i+1]
				i++
			}
			if knowledge.ValidateSessionLabel(true, val) == nil {
				out = append(out, "--session", val)
			}
			continue
		}
		out = append(out, args[i])
	}
	return out, SessionLabel(out)
}

// sessionLabelMaxAge — the session-label expiry window: reuses
// events_retention_days — aligned with the retention window of the read_events rows the label
// points at (a label whose rows were cleaned is stale). No new tuning value; the meta override
// is inherited, and a lookup failure or non-value falls back to the default 14 days (the threshold_meta registered value).
func sessionLabelMaxAge(ctx context.Context, st wire.Store) time.Duration {
	d := knowledge.WriteThresholds(ctx, st).EventsRetentionD
	if d <= 0 {
		d = 14
	}
	return time.Duration(d) * 24 * time.Hour
}

// RunSessionStart — one SessionStart hook: records the session boundary, then hot injection and nudge.
// stdin is not consumed by hot or done, so nudge parses it as-is (the nudge branch keys on
// the hook stdin JSON's hook_event_name). nudge always returns 0, so the exit is effectively
// hot's code. A boundary-record failure is silent (the §13 metering principle —
// only the session metrics demote to 0; injection and notices are unchanged).
// Invalid-label validation now precedes label recording and session-start recording —
// closing the path where an invalid value (e.g. a "--json" flag absorption) remained in the
// label file and the boundary, reviving pollution via read's auto-adoption (invalid is treated as absent, silently).
func RunSessionStart(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	args, label := sanitizeHookSession(args)
	if rec, ok := st.(SessionStartRecorder); ok {
		_ = rec.RecordSessionStart(ctx, label)
	}
	// leave the current session label in the config path so read adopts it automatically —
	// G4 label automation. Failure is silent (§13 — the label is auxiliary).
	// a per-session file + expiry cleanup (settling residues that never meet
	// session-end — the same hook path nudge and compact ride).
	_ = WriteSessionLabel(label)
	if label != "" {
		wire.PruneSessionLabels(sessionLabelMaxAge(ctx, st))
	}
	code := knowledge.RunHot(ctx, st, args, stdin, stdout)
	if ncode := RunNudge(ctx, st, nil, stdin, stdout); ncode != 0 {
		return ncode
	}
	// the update notice — 1 line inside the [flint] banner, checked once a day
	// via the cache TTL. Network failure or not-latest means no output (§13). The real
	// implementation is cli update.go — injected via the MaybeUpdateNoticeFn seam.
	if MaybeUpdateNoticeFn != nil {
		if line := MaybeUpdateNoticeFn(); line != "" {
			fmt.Fprintln(stdout, line)
			// fired counter — the update notice actually firing once (one of the 3 kinds
			// distill, recall, update — spec §2.5).
			knowledge.BumpMetaCounter(ctx, st, "fired_nudge_update")
		}
	}
	// the maintenance-warning summary (24h cache).
	// Connects pending_max — whose firing window had been manual commands only — as one line
	// inside the [flint] banner once a day. Failure means no output (§13).
	if line := maybeMaintenanceNotice(ctx, st); line != "" {
		fmt.Fprintln(stdout, line)
	}
	return code
}

// MaybeUpdateNoticeFn — the update-notice seam (the cli update.go real implementation is
// injected by the cli init — no notice in standalone ops runs or tests).
var MaybeUpdateNoticeFn func() string

// UpdateNoticeTTL — the update/maintenance notice cache TTL (the same value as cli update.go's
// updateNoticeTTL — injected by the cli init, keeping a single source).
var UpdateNoticeTTL = 24 * time.Hour

// maintenanceNoticeCache — the maintenance-summary cache file format. The checked_at field
// follows the same contract as cli update.go's updateCache (a session-hook-owned cache file).
type maintenanceNoticeCache struct {
	CheckedAt string `json:"checked_at"`
}

// maintenanceCacheFile — the maintenance-summary cache path (inheriting the update-cache pattern).
func maintenanceCacheFile() string {
	return DataFile("maintenance-check.json")
}

// maybeMaintenanceNotice — checks only the pending_max excess (the most frequently
// triggered maintenance warning — 1 round trip via the 24h cache). Keeping the minimal-round-trip principle (§2).
func maybeMaintenanceNotice(ctx context.Context, st wire.Store) string {
	path := maintenanceCacheFile()
	if path == "" {
		return ""
	}
	if b, err := os.ReadFile(path); err == nil {
		var c maintenanceNoticeCache
		if json.Unmarshal(b, &c) == nil {
			if checked, err := time.Parse(time.RFC3339, c.CheckedAt); err == nil &&
				time.Since(checked) < UpdateNoticeTTL {
				return "" // already checked today — no output
			}
		}
	}
	th := knowledge.WriteThresholds(ctx, st)
	// the pending-count lookup delegates to the single countPending implementation.
	n, err := knowledge.CountPending(ctx, st)
	if err != nil {
		return "" // check failure — the cache is not refreshed (the update-notice pattern: retry next session)
	}
	_ = os.WriteFile(path, []byte(`{"checked_at":"`+time.Now().UTC().Format(time.RFC3339)+`"}`), 0o600)
	if n > th.PendingMax {
		return "[flint] pending " + strconv.Itoa(n) + " items (limit " + strconv.Itoa(th.PendingMax) + ") — check flint doctor and maintain"
	}
	return ""
}

// SessionLabel — extracts the --session value from the arguments (the shared single
// parser of hot and session-start — preventing the two paths from diverging via
// duplicated mirrors). The knowledge.SessionLabelFn seam receives this export (via an alias) as its injection.
func SessionLabel(args []string) string {
	s := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" && i+1 < len(args) {
			s = args[i+1]
			i++
		}
	}
	return s
}

// RunSessionEnd — one Stop hook: the done verdict, then nudge (the Stop additional context).
// done always returns 0, so the exit is effectively 0 (inheriting non-interference §13).
// The session interval is defined by session_starts alone ([start_i, start_{i+1})) —
// no end boundary is recorded separately (ruling).
func RunSessionEnd(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	// an invalid label is treated as absent (silently — Stop stdout is also a hook path).
	args, label := sanitizeHookSession(args)
	// session end — clean up only this session's label file
	// (preventing staleness after exit, preserving parallel-session labels).
	ClearSessionLabel(label)
	code := knowledge.RunDone(ctx, st, args, stdin, stdout)
	if ncode := RunNudge(ctx, st, nil, stdin, stdout); ncode != 0 {
		return ncode
	}
	return code
}
