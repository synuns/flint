package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

func maintainRunGC(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	return maintainGC(ctx, r.ms, stdout, r.shallow, r.yes)
}

func maintainRunPurgeDropped(ctx context.Context, r *maintainRequest, stdout io.Writer) int {
	return RunPurgeDropped(ctx, r.ms, r.yes, stdout)
}

const gcPingAttempts = 5

var maintainGCFn = func(ctx context.Context, st wire.Store, shallow bool) error {
	q := "SELECT dolt_gc()"
	if shallow {
		q = "SELECT dolt_gc('--shallow')"
	}
	_, err := st.Query(ctx, q)
	return err
}

var maintainGCPingDelay = time.Second

func maintainGC(ctx context.Context, ms wire.Store, stdout io.Writer, shallow, yes bool) int {
	if !yes {
		fmt.Fprintln(stdout, wire.ErrLine(maintainUsageErrf("%s", errors.New("maintain: --gc requires --yes (confirmation)"))))
		return 1
	}
	mode := "full"
	if shallow {
		mode = "shallow"
	}

	fmt.Fprintln(stdout, `{"status":"gc-pending","notice":"dolt_gc() disconnects all active sessions — flint will reconnect (up to `+
		strconv.Itoa(gcPingAttempts)+` pings)"}`)
	if err := maintainGCFn(ctx, ms, shallow); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("maintain: --gc: %w", err)))
		return store.MapExit(err)
	}

	reconnected := false
	for i := 0; i < gcPingAttempts; i++ {
		if i > 0 {
			time.Sleep(maintainGCPingDelay)
		}
		if err := ms.Ping(ctx); err == nil {
			reconnected = true
			break
		}
	}
	if !reconnected {
		fmt.Fprintln(stdout, `{"status":"gc-incomplete","notice":"the GC ran but the connection did not return — verify with flint doctor, then rerun maintain --gc --yes"}`)
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: --gc: reconnect failed after %d pings", gcPingAttempts)))
		return 1
	}

	rows, err := wire.QueryOnce(ctx, ms, `SELECT value FROM meta WHERE key = 'schema_version'`, nil)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("maintain: --gc: schema recheck failed: %w", err)))
		return 1
	}
	version := ""
	if len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
		version = fmt.Sprint(rows[0][0])
	}
	if _, convErr := strconv.Atoi(version); convErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf(
			"maintain: --gc: schema recheck failed: schema_version=%q", version)))
		return 1
	}

	stamped := time.Now().UTC().Format(time.RFC3339)
	if err := wire.SetMetaKV(ctx, ms, "last_gc_at", stamped); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("maintain: --gc: last_gc_at: %w", err)))
		return store.MapExit(err)
	}
	if err := wire.SetMetaKV(ctx, ms, "last_gc_mode", mode); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("maintain: --gc: last_gc_mode: %w", err)))
		return store.MapExit(err)
	}
	out, mErr := json.Marshal(struct {
		Status string `json:"status"`
		Mode   string `json:"mode"`
		LastAt string `json:"last_gc_at"`
	}{Status: "gc", Mode: mode, LastAt: stamped})
	if mErr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(mErr))
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

const purgeDroppedListMarker = "available databases that can be undropped:"
const purgeDroppedEmptyMarker = "there are no databases currently available to be undropped"

var purgeDroppedSQLStateRe = regexp.MustCompile(`\(SQLSTATE [0-9A-Z]{5}\)`)

var PurgeDSNFn = wire.ResolveDSN

var PurgeEngineModeFn = loadEngineMode

func loadEngineMode() string {
	p := wire.ConfigPath()
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var c struct {
		Engine struct {
			Mode string `json:"mode"`
		} `json:"engine"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return ""
	}
	return c.Engine.Mode
}

func purgeDroppedLoopback(dsn string) bool {
	u, err := url.Parse(dsn)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1"
}

func listDroppedDatabases(ctx context.Context, st wire.Store) ([]string, bool) {
	_, err := st.Query(ctx, `SELECT dolt_undrop()`)
	if err == nil {
		return nil, false
	}
	msg := err.Error()
	if strings.Contains(msg, purgeDroppedEmptyMarker) {
		return []string{}, true
	}
	i := strings.Index(msg, purgeDroppedListMarker)
	if i < 0 {
		return nil, false
	}
	rest := purgeDroppedSQLStateRe.ReplaceAllString(msg[i+len(purgeDroppedListMarker):], "")
	names := []string{}
	for _, p := range strings.Split(rest, ",") {
		n := strings.TrimSpace(p)
		if n != "" {
			names = append(names, n)
		}
	}
	return names, true
}

func RunPurgeDropped(ctx context.Context, st wire.Store, yes bool, stdout io.Writer) int {
	if PurgeEngineModeFn() == "external" {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
			"maintain: --purge-dropped: managed-mode engine only — config engine.mode is \"external\"")))
		return 3
	}
	if !purgeDroppedLoopback(PurgeDSNFn()) {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
			"maintain: --purge-dropped: loopback-only — resolved DSN host is not 127.0.0.1/localhost")))
		return 3
	}
	names, ok := listDroppedDatabases(ctx, st)
	if !ok {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
			"maintain: --purge-dropped: dropped-database listing unsupported on this engine — purge refused (dolt catalog query failed)")))
		return 3
	}
	if !yes {
		out, mErr := json.Marshal(struct {
			Status    string   `json:"status"`
			Dropped   int      `json:"dropped"`
			Databases []string `json:"databases"`
			Summary   string   `json:"summary"`
		}{Status: "dry_run", Dropped: len(names), Databases: names,
			Summary: fmt.Sprintf("%d restorable — apply with maintain --purge-dropped --yes (irreversible)", len(names))})
		if mErr != nil {
			fmt.Fprintln(stdout, wire.ErrLine(mErr))
			return 1
		}
		fmt.Fprintln(stdout, string(out))
		return 0
	}
	if _, err := st.Query(ctx, `SELECT dolt_purge_dropped_databases()`); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("maintain: --purge-dropped: %w", err)))
		return store.MapExit(err)
	}
	fmt.Fprintf(stdout, `{"status":"purged","dropped":%d}`+"\n", len(names))
	return 0
}
