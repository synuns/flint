// auto.go owns the once-daily automatic backup trigger (PLAN v0.2 §8).
// The 24h-elapsed check is split into a pure function, and DB access is
// injected as meta accessor functions for testability. Failures only return
// an error — non-blocking handling that keeps the session alive is the
// caller's responsibility.
package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
)

// DefaultBackupIntervalH — the default minimum automatic backup interval, in
// hours. Overridable via the meta backup_interval_h (paired with backup_keep).
const DefaultBackupIntervalH = 24

// BackupIntervalKey — the interval meta key.
const BackupIntervalKey = "backup_interval_h"

// LastBackupKey is the meta key storing the last backup time.
const LastBackupKey = "last_backup"

// dumpFn is the Dump call seam (swapped with a fake in tests).
var dumpFn = Dump

func realDumpEnabled() bool { return os.Getenv("SUPERLITE_REAL") == "1" }

// ShouldBackup reports whether intervalH hours have elapsed since the last
// backup (lastISO, RFC3339). An empty or unparseable value counts as a first
// backup and yields true. intervalH <= 0 falls back to the default 24h, and the
// span shares the db.HoursSpan clamp (v1.19.3): a backup_interval_h override
// past the Duration ceiling wraps the interval negative — every check instantly
// due (a dump every session).
func ShouldBackup(lastISO string, now time.Time, intervalH int) bool {
	if lastISO == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339, lastISO)
	if err != nil {
		return true
	}
	return now.Sub(last) >= db.HoursSpan(intervalH, DefaultBackupIntervalH)
}

// Auto is the automatic backup runner for a target DSN.
type Auto struct {
	DSN string
}

// RunIfNeeded writes a dump to dump only when the last_backup meta shows 24h
// or more have elapsed, then updates the meta. No-op when not elapsed; on
// failure it returns an error. Atomic claiming (CAS) — only the session that
// wins claim(old -> now) dumps. The claim value itself is the timestamp, so
// no separate final stamp is kept. Trade-off: if the dump fails, the same-day
// retry is postponed to the next day (consistent with the once-daily backup
// contract; failures stay quiet, §13).
func (a *Auto) RunIfNeeded(ctx context.Context,
	metaKV func(string) (string, error),
	claim func(oldVal, newVal string) (bool, error),
	dump io.Writer) error {

	last, err := metaKV(LastBackupKey)
	if err != nil {
		return fmt.Errorf("backup auto: read meta: %w", err)
	}
	intervalH := 0
	if v, err := metaKV(BackupIntervalKey); err == nil {
		if n, perr := strconv.Atoi(strings.TrimSpace(v)); perr == nil {
			intervalH = n
		}
	}
	now := time.Now().UTC()
	if !ShouldBackup(last, now, intervalH) {
		return nil
	}
	// RFC3339Nano — with second-granularity timestamps, parallel sessions
	// within the same second write the same newVal, which breaks CAS's
	// single-winner guarantee (verified on a real DB, 8-way: identical values
	// all win after the WHERE snapshot evaluation). Nanoseconds give the
	// value uniqueness. ShouldBackup parses RFC3339 and reads nanoseconds
	// as-is (time.Parse compatible).
	won, err := claim(last, now.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("backup auto: claim (CAS): %w", err)
	}
	if !won {
		return nil // a parallel session claimed it — this one is a no-op
	}
	if err := dumpFn(ctx, a.DSN, dump); err != nil {
		return fmt.Errorf("backup auto: dump: %w", err)
	}
	return nil
}
