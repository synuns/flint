package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/synuns/flint/lib/wire"
)

type Marker struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

const (
	MarkerKindDistill = "distill"
	MarkerKindRecall  = "recall"
)

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

					if leftovers, gerr := filepath.Glob(filepath.Join(home, "."+name+"-*")); gerr == nil {
						for _, lv := range leftovers {
							_ = os.Remove(lv)
						}
					}

					if tmp, terr := os.CreateTemp(home, "."+name+"-*"); terr == nil {
						tmpName := tmp.Name()
						_, werr := tmp.Write(b)
						tmp.Close()
						if werr == nil && os.Chmod(tmpName, 0o600) == nil && os.Rename(tmpName, dst) == nil {
							_ = os.Remove(src)
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

	wire.ConfigDir = ConfigDir
}

func MarkerFile() string { return markerFileOf(MarkerKindDistill) }

func RecallMarkerFile() string { return markerFileOf(MarkerKindRecall) }

func WriteMarker(sessionID string, reason string) error {
	return WriteTypedMarker(MarkerKindDistill, sessionID, reason)
}

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

func ReadMarker() (*Marker, error) { return ReadTypedMarker(MarkerKindDistill) }

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

func ClearMarker() error { return ClearTypedMarker(MarkerKindDistill) }

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

func ClearSessionLabel(session string) {
	if session != "" {
		if p := wire.SessionLabelFile(session); p != "" {
			_ = os.Remove(p)
		}
	}
}
