package ops

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func checkDB(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "db"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint setup — docker compose up -d"
		return c
	}
	if err := st.Ping(ctx); err != nil {
		c.Status = "fail"
		c.Detail = err.Error()
		c.Fix = "fix: flint setup — docker compose up -d"
		return c
	}
	c.Status = "ok"
	return c
}

func checkSchema(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "schema"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint status — verify schema migration"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT value FROM meta WHERE key = 'schema_version'`, nil)
	if err != nil {
		c.Status = "fail"
		c.Detail = err.Error()
		c.Fix = "fix: flint status — check DB connection and schema"
		return c
	}
	version := ""
	if len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		version = fmt.Sprint(rows[0][0])
	}
	if n, convErr := strconv.Atoi(version); convErr != nil || n < 4 {
		c.Status = "fail"
		c.Detail = "schema_version=" + version
		c.Fix = "fix: reconnect with the v0.6 CLI to migrate the schema — flint status"
		return c
	}
	c.Status = "ok"
	return c
}

func checkIntegrity(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "integrity"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint status — verify index migration"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT count(*) FROM pg_indexes WHERE tablename = 'knowledge' AND indexname LIKE 'flint_knowledge_%'`, nil)
	if err != nil {
		c.Status = "fail"
		c.Detail = err.Error()
		c.Fix = "fix: flint status — check DB connection and indexes"
		return c
	}
	n := -1
	if len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		fmt.Sscan(fmt.Sprint(rows[0][0]), &n)
	}
	if n < 3 {
		c.Status = "fail"
		c.Detail = fmt.Sprintf("flint_knowledge_* indexes: %d (expected 3)", n)
		c.Fix = "fix: flint status — retry the v3 index migration"
		return c
	}
	c.Status = "ok"
	return c
}

func checkLastSeenTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "last-seen"}
	if st == nil {
		c.Status, c.Fix = "fail", "fix: flint status — check DB connection"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_write_at','last_hot_at','last_read_at')`, nil)
	if err != nil {
		c.Status = "warn"
		c.Detail = "last_* query failed: " + err.Error()
		return c
	}
	seen := map[string]time.Time{}
	for _, r := range rows {
		if len(r) != 2 || r[0] == nil || r[1] == nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339, fmt.Sprint(r[1]))
		if err != nil {
			continue
		}
		seen[fmt.Sprint(r[0])] = ts
	}

	if rows, rerr := wire.QueryOnce(ctx, st,
		`SELECT max(ts) FROM read_events`, nil); rerr == nil &&
		len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		if t, ok := CellToTime(rows[0][0]); ok {
			if prev, had := seen["last_read_at"]; !had || t.After(prev) {
				seen["last_read_at"] = t
			}
		}
	}

	warns := 0
	if detail := stallDetail(seen, "last_hot_at", th.StallHotH, &warns); detail != "" {
		c.Detail = detail
	}
	if detail := stallDetail(seen, "last_write_at", th.StallWriteH, &warns); detail != "" {
		if c.Detail != "" {
			c.Detail += "; "
		}
		c.Detail += detail
	}
	if warns > 0 {
		c.Status = "warn"
		return c
	}
	c.Status = "ok"
	return c
}

func checkMetaHygiene(ctx context.Context, st wire.Store) DoctorCheck {
	return checkMetaHygieneTh(ctx, st, doctorThresholds(ctx, st)())
}

func checkMetaHygieneTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "meta-hygiene"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query meta"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT key FROM meta ORDER BY key`, nil)
	if err != nil {
		c.Status = "warn"
		c.Detail = "meta query failed: " + err.Error()
		return c
	}
	thKeys := map[string]bool{}
	for _, k := range wire.ThresholdsKeys() {
		thKeys[k] = true
	}
	retention := th.DailyRetentionD
	if retention <= 0 {
		retention = 14
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -(retention + 7)).Format("2006-01-02")
	unknown, staleDaily := 0, 0
	var samples []string
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		k := fmt.Sprint(r[0])
		switch {
		case strings.HasPrefix(k, "daily:"):

			if d, ok := dailyKeyDate(k); ok && d < cutoff {
				staleDaily++
			}
		case KnownMetaKeyFn(k) || thKeys[k]:
		default:

			known := false
			for _, mk := range TuningMetaKeys() {
				if mk.Key == k {
					known = true
					break
				}
			}
			if !known {
				unknown++
				if len(samples) < 5 {

					samples = append(samples, render.Text(k))
				}
			}
		}
	}
	if unknown == 0 && staleDaily == 0 {
		c.Status = "ok"
		c.Detail = "meta key hygiene ok (" + strconv.Itoa(len(rows)) + " keys total)"
		return c
	}
	c.Status = "warn"
	detail := ""
	if unknown > 0 {
		detail = fmt.Sprintf("%d unknown keys (%s)", unknown, strings.Join(samples, ", "))
	}
	if staleDaily > 0 {
		if detail != "" {
			detail += " · "
		}
		detail += fmt.Sprintf("%d daily counters past retention window (+%dd grace)", staleDaily, 7)
	}
	c.Detail = detail
	c.Fix = "fix: flint maintain --meta-audit — manually clean up deprecated keys and stale daily counters"
	return c
}

func checkBackupKey(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "backup-key"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot check key status"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT value FROM meta WHERE key = 'backup_encrypt'`, nil)
	if err != nil {
		c.Status = "warn"
		c.Detail = "backup_encrypt query failed — retry; if it persists, check the schema"
		return c
	}
	if len(rows) == 0 || len(rows[0]) == 0 || !BackupEncryptOnFn(fmt.Sprint(rows[0][0])) {
		c.Status = "ok"
		c.Detail = "encryption not in use (plaintext backups — opt in via backup_encrypt)"
		return c
	}
	key := BackupKeyPathFn()
	if key == "" {
		c.Status = "warn"
		c.Detail = "cannot determine config directory — cannot build key path"
		return c
	}
	fi, err := os.Stat(key)
	if err != nil {
		c.Status = "warn"
		c.Detail = "backup_encrypt is on but the key file is missing — the next encrypted backup will create a new key, risking an incompatible mix with existing ciphertext"
		c.Fix = "fix: locate the existing key (" + key + ") then verify it with flint backup --encrypt --out <path>"
		return c
	}
	if fi.Mode().Perm() != 0o600 {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("key file mode %o — must be 0600 (blocks other local accounts)", fi.Mode().Perm())
		c.Fix = "fix: chmod 600 " + key
		return c
	}
	c.Status = "ok"
	c.Detail = "backup encryption key ok (0600)"
	return c
}

func CheckBackupAge(ctx context.Context, st wire.Store) DoctorCheck {
	return checkBackupAgeTh(ctx, st, doctorThresholds(ctx, st)())
}

func checkBackupAgeTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "backup-age"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "last_backup not set — check the flint done hook or run backup manually"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT key, value FROM meta WHERE key = 'last_backup'`, nil)
	if err != nil {
		c.Status = "warn"
		c.Detail = "last_backup query failed: " + err.Error() + " — check the flint done hook or run backup manually"
		return c
	}
	last := ""
	if len(rows) > 0 && len(rows[0]) > 1 && rows[0][1] != nil {
		last = fmt.Sprint(rows[0][1])
	}
	ts, perr := time.Parse(time.RFC3339, last)
	if perr != nil {
		ts = time.Time{}
	}
	if ts.IsZero() {
		c.Status = "warn"
		c.Detail = "last_backup not set — check the flint done hook or run backup manually"
		return c
	}

	if d := time.Since(ts); d >= db.HoursSpan(th.BackupAgeWarnH, store.DefaultThresholds().BackupAgeWarnH) {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("last backup %dh ago — check the flint done hook or run backup manually", int(d.Hours()))
		return c
	}
	c.Status = "ok"
	return c
}

const DoctorConflictLimit = 50

type conflictStore interface {
	ConflictGroups(ctx context.Context, limit int) ([]db.ConflictGroup, error)
}

func CheckConflicts(ctx context.Context, st wire.Store) DoctorCheck {
	c := DoctorCheck{Name: "conflicts"}
	cs, ok := st.(conflictStore)
	if st == nil || !ok {
		c.Status = "warn"
		c.Detail = "cannot query conflict candidates — check maintain --conflict-suspects"
		return c
	}
	groups, err := cs.ConflictGroups(ctx, DoctorConflictLimit)
	if err != nil {
		c.Status = "warn"
		c.Detail = "conflict candidate query failed: " + err.Error() + " — check maintain --conflict-suspects"
		return c
	}

	n := 0
	for _, g := range groups {
		if IsConflictGroupFn(g, ConflictDupThresholdFn()) {
			n++
		}
	}
	if n > 0 {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("%d conflict candidate groups — maintain --conflict-suspects", n)
		if len(groups) >= DoctorConflictLimit {
			c.Detail += fmt.Sprintf(" · group query limit %d reached — more may exist", DoctorConflictLimit)
		}
		return c
	}
	c.Status = "ok"
	return c
}

const doctorZeroNearWindow = 7

func CheckZeroNear(ctx context.Context, st wire.Store) DoctorCheck {
	return checkZeroNearTh(ctx, st, doctorThresholds(ctx, st)())
}

func checkZeroNearTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "zero-near"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query daily metrics — check read_zero_near via flint report"
		return c
	}

	days, err := WindowedDaily(ctx, st, doctorZeroNearWindow)
	if err != nil {
		c.Status = "warn"
		c.Detail = "daily metrics query failed: " + err.Error() + " — check read_zero_near via flint report"
		return c
	}
	read, near := 0, 0
	for _, d := range days {
		read += d.Read
		near += d.ReadNear
	}
	if read < th.ZeroNearMinRead {
		c.Status = "ok"
		c.Detail = fmt.Sprintf("insufficient samples (7d read %d < %d) — verdict deferred", read, th.ZeroNearMinRead)
		return c
	}
	if pct := near * 100 / read; pct >= th.ZeroNearWarnPct {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("zero_near_pct %d%% (%d near of %d reads over 7d) — possible vocabulary mismatch — search quality could improve",
			pct, read, near)
		return c
	}
	c.Status = "ok"
	c.Detail = fmt.Sprintf("zero_near_pct %d%% (7d read %d)", near*100/read, read)
	return c
}

func CheckPendingAge(ctx context.Context, st wire.Store) DoctorCheck {
	return checkPendingAgeTh(ctx, st, doctorThresholds(ctx, st)())
}

func checkPendingAgeTh(ctx context.Context, st wire.Store, th store.Thresholds) DoctorCheck {
	c := DoctorCheck{Name: "pending-age"}
	if st == nil {
		c.Status = "warn"
		c.Detail = "cannot query pending age — clean up with maintain --expire-pending N"
		return c
	}
	rows, err := wire.QueryOnce(ctx, st, `
		SELECT valid_at FROM knowledge
		WHERE status = 'pending' AND invalid_at IS NULL
		ORDER BY valid_at ASC LIMIT 1`, nil)
	if err != nil {
		c.Status = "warn"
		c.Detail = "pending age query failed: " + err.Error() + " — clean up with maintain --expire-pending N"
		return c
	}
	if len(rows) == 0 || len(rows[0]) == 0 || rows[0][0] == nil {
		c.Status = "ok"
		return c
	}
	va, ok := AnyToTimeFn(rows[0][0])
	if !ok {
		c.Status = "ok"
		return c
	}

	if th.PendingAgeDays <= 0 {
		th = store.DefaultThresholds()
	}

	if d := time.Since(va); d >= db.DaysSpan(th.PendingAgeDays, time.Now()) {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("oldest pending %dd past — clean up with maintain --expire-pending N",
			int(d.Hours()/24))
		return c
	}
	c.Status = "ok"
	return c
}

func stallDetail(seen map[string]time.Time, key string, graceH int, warns *int) string {
	ts, ok := seen[key]
	if !ok {
		*warns++
		return key + " never recorded"
	}

	grace := db.HoursSpan(graceH, 0)
	if d := time.Since(ts); d > grace {
		*warns++
		return fmt.Sprintf("%s elapsed %s (over %dh grace)", key, d.Round(time.Hour), int(grace.Hours()))
	}
	return ""
}
