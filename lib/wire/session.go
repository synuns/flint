package wire

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	sessionLabelPrefix = "session-"
	sessionLabelSuffix = ".label"
)

func SessionLabelFile(session string) string {
	d := configDirOrEmpty()
	if d == "" || session == "" || strings.ContainsAny(session, "/") || len(session) > 200 {
		return ""
	}
	return filepath.Join(d, sessionLabelPrefix+session+sessionLabelSuffix)
}

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

	now := time.Now()
	_ = os.Chtimes(newest, now, now)
	return s, true
}

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
