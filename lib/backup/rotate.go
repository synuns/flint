// rotate.go — backup rotation. Keeps the newest N .sql dumps by mtime and
// deletes the rest together with their paired .sha256 sidecars (the
// ADR-0006 sidecar convention).
package backup

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BackupKeepKey is the meta key storing the rotation retention count.
const BackupKeepKey = "backup_keep"

// DefaultBackupKeep — the default retention count when backup_keep is unset or invalid.
const DefaultBackupKeep = 14

// BackupKeep reads the meta backup_keep value and returns the retention
// count. getKV is a key -> value lookup function (same shape as the metaKV
// of auto.RunIfNeeded). Errors, unset, or invalid values (non-integer,
// below 1) fall back to the default 14 (not an error). Unset (blank value
// or lookup error — the fresh-DB "meta backup_keep not set") is a normal
// default state and falls back quietly (warning removed). Only the warning
// for a set-but-invalid value remains.
// e.g. BackupKeep(getKV) with backup_keep="3" -> 3; "0"/"x"/error -> 14
func BackupKeep(getKV func(string) (string, error)) int {
	if getKV == nil {
		return DefaultBackupKeep
	}
	v, err := getKV(BackupKeepKey)
	if err != nil || strings.TrimSpace(v) == "" {
		return DefaultBackupKeep // unset = normal default — quiet (§13 non-interference)
	}
	n, perr := strconv.Atoi(strings.TrimSpace(v))
	if perr != nil || n < 1 {
		log.Printf("[flint] backup: %s=%q invalid — default %d", BackupKeepKey, v, DefaultBackupKeep)
		return DefaultBackupKeep
	}
	return n
}

// flintDumpName — flint-generated dump file names (flint-YYYYMMDD-HHMMSS-PID.sql
// and encrypted .sql.age, exactly as lazyDumpFile assembles them — see the
// done.go ext; includes bare .age). Rotation only targets files matching
// this pattern — user-placed .sql/.age in the same directory are excluded
// from both deletion and the keep count.
var flintDumpName = regexp.MustCompile(`^flint-\d{8}-\d{6}-\d+\.(sql|sql\.age|age)$`)

// Rotate keeps the keep newest flint-generated dumps in dir (name pattern
// flintDumpName) by mtime and deletes the rest. Off-pattern .sql/.age are
// treated as user files and excluded from the keep count too. For each
// deletion target, a paired <name>.sha256 sidecar is deleted alongside if
// present (alone when missing); removed records the deleted basenames
// (oldest first: the original immediately followed by its sidecar).
// keep < 1 is clamped to at least 1 (no deleting everything). A dir read
// failure is an error; a delete failure is a partial list + error.
// e.g. Rotate(dir,1) with a.sql(new)·b.sql(old) -> removed=[b.sql b.sql.sha256]
func Rotate(dir string, keep int) ([]string, error) {
	if keep < 1 {
		keep = 1
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("backup rotate: %w", err)
	}

	type dump struct {
		name string
		mod  time.Time
	}
	var dumps []dump
	for _, e := range entries {
		// Encrypted backups (.sql.age) rotate too — ext is .age.
		// Only the flint-generated pattern — user .sql/.age are excluded from the count.
		if e.IsDir() || !isDumpExt(filepath.Ext(e.Name())) || !flintDumpName.MatchString(e.Name()) {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			return nil, fmt.Errorf("backup rotate: %s: %w", e.Name(), ierr)
		}
		dumps = append(dumps, dump{name: e.Name(), mod: info.ModTime()})
	}
	sort.Slice(dumps, func(i, j int) bool { return dumps[i].mod.After(dumps[j].mod) })
	if len(dumps) <= keep {
		return nil, nil
	}

	var removed []string
	for _, d := range dumps[keep:] { // given the sort, the tail is oldest-first
		if rerr := os.Remove(filepath.Join(dir, d.name)); rerr != nil {
			return removed, fmt.Errorf("backup rotate: %w", rerr)
		}
		removed = append(removed, d.name)
		side := d.name + ".sha256"
		if _, serr := os.Stat(filepath.Join(dir, side)); serr == nil {
			if rerr := os.Remove(filepath.Join(dir, side)); rerr != nil {
				return removed, fmt.Errorf("backup rotate: %w", rerr)
			}
			removed = append(removed, side)
		}
	}
	return removed, nil
}

// isDumpExt — dump extensions eligible for rotation (.sql plaintext, .age ciphertext).
func isDumpExt(ext string) bool {
	return ext == ".sql" || ext == ".age"
}
