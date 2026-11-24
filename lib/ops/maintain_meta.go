package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func maintainRunCheck(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	return maintainCheck(ctx, r.ms, stdout)
}

func maintainRunMetaAudit(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	return maintainMetaAudit(ctx, r.ms, stdout)
}
func maintainCheck(ctx context.Context, ms MaintStore, stdout io.Writer) int {
	counts, err := ms.Counts(ctx)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	th, err := wire.LoadThresholds(ctx, ms)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	line := `{"status":"ok"}`
	if counts.Pending > th.PendingMax || counts.DupSuspect > th.DupSuspMax {
		line = fmt.Sprintf(`{"status":"review-needed","pending":%d,"dup_suspect":%d}`,
			counts.Pending, counts.DupSuspect)
	}

	if warns := stallWarnings(ctx, ms, th); len(warns) > 0 {
		b, mErr := json.Marshal(warns)
		if mErr == nil {
			line = line[:len(line)-1] + `,"warnings":` + string(b) + `}`
		}
	}

	if extra := drillReport(ctx, ms); extra != "" {
		line = line[:len(line)-1] + extra + "}"
	}

	line = line[:len(line)-1] + `,"next":` + strconv.Quote(maintainCheckNext(counts, th)) + `}`
	fmt.Fprintln(stdout, line)
	return 0
}

func maintainCheckNext(counts db.Counts, th store.Thresholds) string {
	var cmds []string
	if counts.Pending > th.PendingMax {
		cmds = append(cmds, "flint pending", "flint maintain --expire-pending N")
	}
	if counts.DupSuspect > th.DupSuspMax {
		cmds = append(cmds, "flint maintain --dup-suspects")
	}
	if len(cmds) == 0 {
		return "none — counts within thresholds"
	}
	return strings.Join(cmds, " · ")
}

func stallWarnings(ctx context.Context, st wire.Store, th store.Thresholds) []string {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_hot_at','last_write_at')`, nil)
	if err != nil {
		return nil
	}
	kv := map[string]string{}
	for _, r := range rows {
		if len(r) == 2 {
			kv[fmt.Sprint(r[0])] = fmt.Sprint(r[1])
		}
	}
	var warns []string
	now := time.Now()

	graceHot := db.HoursSpan(th.StallHotH, store.DefaultThresholds().StallHotH)
	graceWrite := db.HoursSpan(th.StallWriteH, store.DefaultThresholds().StallWriteH)
	if ts, ok := AnyToTime(kv["last_hot_at"]); ok {
		if el := now.Sub(ts); el > graceHot {
			warns = append(warns, fmt.Sprintf(
				"hot stall: %dh since last_hot_at (grace %dh)", int(el.Hours()), int(graceHot.Hours())))
		}
	}
	if ts, ok := AnyToTime(kv["last_write_at"]); ok {
		if el := now.Sub(ts); el > graceWrite {
			warns = append(warns, fmt.Sprintf(
				"write stall: %dh since last_write_at (grace %dh)", int(el.Hours()), int(graceWrite.Hours())))
		}
	}
	return warns
}

func drillThresholds(ctx context.Context, st wire.Store) int {
	if th, err := wire.LoadThresholds(ctx, st); err == nil {
		return th.DrillMaxDays
	}
	return 30
}

func drillReport(ctx context.Context, st wire.Store) string {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT key, value FROM meta WHERE key IN ('last_drill')`, nil)
	if err != nil {
		return ""
	}
	kv := map[string]string{}
	for _, r := range rows {
		if len(r) == 2 {
			kv[fmt.Sprint(r[0])] = fmt.Sprint(r[1])
		}
	}
	maxDays := drillThresholds(ctx, st)
	last, err := time.Parse(time.RFC3339, kv["last_drill"])
	if err != nil {
		return `,"drill":"never"`
	}
	if time.Since(last) > db.DaysSpan(maxDays, time.Now()) {
		return `,"drill":"stale"`
	}
	return ""
}

var metaKnownKeyList = []string{
	"schema_version", "views_hash", "views_sig",
	"exposure_profile", "last_backup", "last_drill",
	"last_hot_at", "last_read_at", "last_write_at",
	"read_stats", "read_fallback_calls", "hot_stats",
	"session_knowledge_last", "recall_starved_count",
	"last_brief_at", "last_events_prune_at", "vault_path",
	"vault_last_scan", "scan_stats", "auto_mode", "last_shadow_at",
	"last_gc_at", "last_gc_mode", "stray_dbs_allow",
}

var metaKnownKeys = buildMetaKnownKeys()

func buildMetaKnownKeys() map[string]bool {
	m := make(map[string]bool, len(metaKnownKeyList)+len(meteringFreshnessGateKeys))
	for _, k := range metaKnownKeyList {
		m[k] = true
	}
	for _, k := range meteringFreshnessGateKeys {
		m[k] = true
	}
	return m
}

var metaKnownPrefixes = []string{"daily:", "pb_", "fired_", "auto_streak_", "auto_rounds_", "self_version_last:", "recall_active:"}

func KnownMetaKey(k string) bool {
	if metaKnownKeys[k] {
		return true
	}
	for _, p := range metaKnownPrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

func dailyKeyDate(k string) (string, bool) {
	segs := strings.Split(strings.TrimPrefix(k, "daily:"), ":")
	d := segs[len(segs)-1]
	if len(d) != 10 {
		return "", false
	}
	if _, err := time.Parse("2006-01-02", d); err != nil {
		return "", false
	}
	return d, true
}
func maintainMetaAudit(ctx context.Context, ms MaintStore, stdout io.Writer) int {
	rows, err := wire.QueryOnce(ctx, ms, `SELECT key, value FROM meta ORDER BY key`, nil)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	thKeys := map[string]bool{}
	for _, k := range wire.ThresholdsKeys() {
		thKeys[k] = true
	}
	tunKeys := map[string]bool{}
	for _, mk := range TuningMetaKeys() {
		tunKeys[mk.Key] = true
	}
	now := time.Now().UTC()
	known, thresholdN, tuningN, dailyN, unknown := 0, 0, 0, 0, 0
	var oldestDaily, oldestDate string
	var unknownList []string
	for _, r := range rows {
		if len(r) < 1 {
			continue
		}
		k := fmt.Sprint(r[0])
		switch {
		case strings.HasPrefix(k, "daily:"):
			dailyN++

			if d, ok := dailyKeyDate(k); ok && d < now.Format("2006-01-02") {
				if oldestDaily == "" || d < oldestDate {
					oldestDaily, oldestDate = k, d
				}
			}
		case KnownMetaKey(k):
			known++
		case thKeys[k]:
			thresholdN++
		case tunKeys[k]:
			tuningN++
		default:
			unknown++
			unknownList = append(unknownList, k)
		}
	}

	retention := 0
	for _, r := range rows {
		if len(r) >= 2 && fmt.Sprint(r[0]) == "daily_retention_days" {
			if n, ok := render.CellToInt(r[1]); ok {
				retention = n
			}
		}
	}
	if retention <= 0 {
		retention = 14
	}
	fmt.Fprintf(stdout, `{"status":"meta-audit","total":%d,"known":%d,"thresholds":%d,"tuning":%d,"daily":%d,"retention_days":%d,"oldest_daily":%q,"unknown":%d,"unknown_keys":[`,
		len(rows), known, thresholdN, tuningN, dailyN, retention, oldestDaily, unknown)
	for i, k := range unknownList {
		if i > 0 {
			fmt.Fprint(stdout, ",")
		}
		fmt.Fprint(stdout, QuoteJSONFn(k))
	}
	fmt.Fprintln(stdout, `]}`)
	if unknown > 0 {
		fmt.Fprintln(stdout, `{"hint":"unknown keys are deprecation candidates — verify then clean up manually: DELETE FROM meta WHERE key = '...'"}`)
	}
	return 0
}
