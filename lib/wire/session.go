// session.go — ReadSessionLabel (and the path rule
// SessionLabelFile). The label 'store'
// functions (WriteSessionLabel, ClearSessionLabel) remain in cli marker.go —
// the filesystem location rule stays command-package-owned via the ConfigDir
// seam (injected by the cli init).
// The label file is per-session (session-<id>.label) — the old single shared
// file (session-current) was the source of the race where parallel sessions
// overwrote or removed each other's labels (read_events label pollution).
// The legacy session-current fallback read and cleanup are gone — residual files
// are ignored (the user's disk — not deleted).
package wire

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// the session-label file name fragment — session-<id>.label.
const (
	sessionLabelPrefix = "session-"
	sessionLabelSuffix = ".label"
)

// SessionLabelFile — the per-session label file path. The session-start hook
// leaves the harness session id, and read silently adopts it when --session is
// unspecified — G4 label automation (no agent cooperation needed). session-end
// removes only this session's file (preventing stale labels after exit —
// preserving parallel-session labels). If session contains a path separator or
// is too long to be a file name, "") (the caller silently skips — the label is an auxiliary account). configDir goes through the injected ConfigDir seam.
func SessionLabelFile(session string) string {
	d := configDirOrEmpty()
	if d == "" || session == "" || strings.ContainsAny(session, "/") || len(session) > 200 {
		return ""
	}
	return filepath.Join(d, sessionLabelPrefix+session+sessionLabelSuffix)
}

// ReadSessionLabel — the current session label: the content of the
// session-*.label file with the newest mtime. Absent or empty content is
// ok=false (the legacy session-current fallback is removed — residual files
// ignored). Adoption under simultaneous parallel sessions inherits the existing
// last-writer-wins ruling (the label is an auxiliary account — METRICS.md).
// On adoption (a successful read), the label file's mtime is touched —
// preventing the label of a session active beyond 14 days (default) from
// vanishing in another session's session-start cleanup (PruneSessionLabels). An mtime update failure is silently ignored (a read-only FS — the label-lookup failure path is unchanged).
func ReadSessionLabel() (string, bool) {
	d := configDirOrEmpty()
	if d == "" {
		return "", false
	}
	ms, err := filepath.Glob(filepath.Join(d, sessionLabelPrefix+"*"+sessionLabelSuffix))
	if err != nil || len(ms) == 0 {
		return "", false
	}
	newest, newestT := "", time.Time{}
	for _, m := range ms {
		fi, err := os.Stat(m)
		if err != nil {
			continue
		}
		if newest == "" || fi.ModTime().After(newestT) {
			newest, newestT = m, fi.ModTime()
		}
	}
	if newest == "" {
		return "", false
	}
	b, err := os.ReadFile(newest)
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", false
	}
	// the adoption mtime touch — the cleanup survival of an in-use label. Failures ignored.
	now := time.Now()
	_ = os.Chtimes(newest, now, now)
	return s, true
}

// PruneSessionLabels — removes expired session-label files.
// Prevents the residual files of sessions that never met a session-end (crash,
// forced termination) from piling up forever. maxAge is computed by the caller
// (RunSessionStart) from the events_retention_days default — a label whose
// read_events rows were cleaned is stale (reusing the metering-cleanup threshold —
// no new tuning value; the meta override is inherited). Runs at every
// session-start (the same hook path nudge and compact ride — no separate daemon or scan).
func PruneSessionLabels(maxAge time.Duration) {
	d := configDirOrEmpty()
	if d == "" || maxAge <= 0 {
		return
	}
	ms, err := filepath.Glob(filepath.Join(d, sessionLabelPrefix+"*"+sessionLabelSuffix))
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, m := range ms {
		if fi, err := os.Stat(m); err == nil && fi.ModTime().Before(cutoff) {
			_ = os.Remove(m)
		}
	}
}
