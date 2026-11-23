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

const DefaultBackupIntervalH = 24

const BackupIntervalKey = "backup_interval_h"

const LastBackupKey = "last_backup"

var dumpFn = Dump

func realDumpEnabled() bool { return os.Getenv("SUPERLITE_REAL") == "1" }

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

type Auto struct {
	DSN string
}

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

	won, err := claim(last, now.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("backup auto: claim (CAS): %w", err)
	}
	if !won {
		return nil
	}
	if err := dumpFn(ctx, a.DSN, dump); err != nil {
		return fmt.Errorf("backup auto: dump: %w", err)
	}
	return nil
}
