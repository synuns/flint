// marker.go — the marker helpers: pending-distill.json, left by PostCompact and consumed
// by Stop/SessionStart (the awaiting-distillation signal). Assumes a single personal agent —
// no atomicity needed. The one-shot missing-recall marker (recall-hint.json) is separate —
// durable (awaiting distillation) and one-shot (recall notice) have different lifecycles;
// sharing a file meant they overwrote each other. knowledge's WriteTypedMarkerFn seam has
// this export (via an alias) injected by the cli init — keeping the same contract.
package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/synuns/flint/lib/wire"
)

// Marker — the marker file content (shared across kinds).
type Marker struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

// marker kinds — the file names differ (they can coexist).
const (
	MarkerKindDistill = "distill" // pending-distill.json — durable (until distilled)
	MarkerKindRecall  = "recall"  // recall-hint.json — one-shot (the next SessionStart)
)

// markerFileOf — the full marker file path per kind. An empty configDir yields "".
func markerFileOf(kind string) string {
	if flintHome() == "" {
		return ""
	}
	name := "pending-distill.json"
	if kind == MarkerKindRecall {
		name = "recall-hint.json"
	}
	return DataFile(name)
}

// ConfigDir — FLINT_CONFIG_DIR first, else os.UserConfigDir()/flint.
// On UserConfigDir failure, "" (MarkerFile also "" -> the caller silently skips).
func ConfigDir() string {
	if d := os.Getenv("FLINT_CONFIG_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "flint")
}

// flintHome — the personal DB infrastructure data root (backups, keys, markers, and caches
// in one place — the semantic separation of settings (configDir) from data). FLINT_HOME env >
// FLINT_CONFIG_DIR (doubling as a single-directory override and test isolation) > ~/.flint.
func flintHome() string {
	if d := os.Getenv("FLINT_HOME"); d != "" {
		return d
	}
	if d := os.Getenv("FLINT_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".flint")
}

// DataFile — a runtime artifact path under FLINT_HOME. If the file exists at the legacy
// location (configDir) but not the new one, it migrates (a one-time migration — copy then
// remove the original; failure is silent: the next call retries).
func DataFile(name string) string {
	home := flintHome()
	if home == "" {
		return ""
	}
	dst := filepath.Join(home, name)
	if _, err := os.Stat(dst); err != nil {
		if old := ConfigDir(); old != "" && old != home {
			src := filepath.Join(old, name)
			if b, rerr := os.ReadFile(src); rerr == nil {
				if merr := os.MkdirAll(home, 0o700); merr == nil {
					// settling crash residue tmp files from a prior migration attempt —
					// the same pattern is wiped just before CreateTemp (a one-time migration).
					if leftovers, gerr := filepath.Glob(filepath.Join(home, "."+name+"-*")); gerr == nil {
						for _, lv := range leftovers {
							_ = os.Remove(lv)
						}
					}
					// atomic write — tmp + rename prevents a partial
					// failure from permanentizing an incomplete dst.
					if tmp, terr := os.CreateTemp(home, "."+name+"-*"); terr == nil {
						tmpName := tmp.Name()
						_, werr := tmp.Write(b)
						tmp.Close()
						if werr == nil && os.Chmod(tmpName, 0o600) == nil && os.Rename(tmpName, dst) == nil {
							_ = os.Remove(src) // migration done — remove the legacy original
						} else {
							_ = os.Remove(tmpName)
						}
					}
				}
			}
		}
	}
	return dst
}

func init() {
	// the contract-layer ConfigDir injection (moved into ops with the markers — the same
	// function value as the cli cliwire_alias init's identical injection, so double assignment is harmless).
	wire.ConfigDir = ConfigDir
}

// MarkerFile — the full path of the awaiting-distillation marker file (a backward-compatible alias).
func MarkerFile() string { return markerFileOf(MarkerKindDistill) }

// RecallMarkerFile — the full path of the missing-recall marker file.
func RecallMarkerFile() string { return markerFileOf(MarkerKindRecall) }

// WriteMarker — writes the awaiting-distillation marker (a backward-compatible alias).
func WriteMarker(sessionID string, reason string) error {
	return WriteTypedMarker(MarkerKindDistill, sessionID, reason)
}

// WriteTypedMarker — marshals to JSON, creates the directory (0700), and writes the per-kind marker.
// CreatedAt is the current UTC RFC3339.
func WriteTypedMarker(kind, sessionID, reason string) error {
	path := markerFileOf(kind)
	if path == "" {
		return os.ErrInvalid
	}
	m := Marker{SessionID: sessionID, Reason: reason, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// ReadMarker — reads the marker. Absent is (nil, nil); corrupt is (nil, err).
func ReadMarker() (*Marker, error) { return ReadTypedMarker(MarkerKindDistill) }

// ReadTypedMarker — reads the per-kind marker. Absent is (nil, nil).
func ReadTypedMarker(kind string) (*Marker, error) {
	path := markerFileOf(kind)
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Marker
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ClearMarker — deletes the awaiting-distillation marker (a backward-compatible alias). nil even if the file is absent.
func ClearMarker() error { return ClearTypedMarker(MarkerKindDistill) }

// ClearTypedMarker — deletes the per-kind marker. nil even if the file is absent.
func ClearTypedMarker(kind string) error {
	path := markerFileOf(kind)
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// WriteSessionLabel — writes the current session label as 1 line (an empty label is not written).
// The label file path rule is owned by wire.SessionLabelFile.
// A per-session file (session-<id>.label) — parallel sessions no longer
// overwrite each other's labels (closing the old single-shared-file race).
func WriteSessionLabel(label string) error {
	if label == "" {
		return nil
	}
	path := wire.SessionLabelFile(label)
	if path == "" {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(label), 0o600)
}

// ClearSessionLabel — removes this session's label file (session-end).
// Per-session removal — parallel sessions' labels are not wiped (closing the shared-file
// removal race). Legacy residual label files are ignored (the user's disk —
// not deleted).
func ClearSessionLabel(session string) {
	if session != "" {
		if p := wire.SessionLabelFile(session); p != "" {
			_ = os.Remove(p)
		}
	}
}
