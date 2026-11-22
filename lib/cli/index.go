// index.go — `flint index [--vault PATH] [--limit N]` (v1.21.0 vault
// bridge I): one snapshot-diff scan of a markdown vault via vault.Scan and
// one batch dolt commit per diff. The vault path is user-declared (no auto
// detection): an explicit --vault is validated (a directory, stat-checked)
// and persisted to meta 'vault_path' only AFTER a successful scan (a
// failed or mistyped run persists nothing — v1.21.1); without the flag the
// stored path is reused, and its absence is usage exit 3 guidance. A
// different explicit root emits the snapshot-replacement notice. The scan
// stats round-trip through meta 'vault_last_scan' JSON for the status
// vault row. The vault root path never appears in output, commit
// messages, or error lines (the §3 privacy contract — string output fields
// go through the render.Text chokepoint).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/vault"
	"github.com/synuns/flint/lib/wire"
)

// meta keys owned by this command (persisted vault path + last-scan stats).
const (
	metaVaultPathKey     = "vault_path"
	metaVaultLastScanKey = "vault_last_scan"
)

// indexScanStoreFn — seam over the real scan-store connection (vault.Open on
// the resolved DSN — the pool is separate from the command store because the
// diff SQL is vault-package-owned). Tests swap in a fake vault.Store; the
// SUPERLITE_REAL=1 path keeps the real implementation.
var indexScanStoreFn = func(ctx context.Context, dsn string) (vault.Store, func(), error) {
	st, err := vault.Open(ctx, dsn)
	if err != nil {
		return nil, nil, err
	}
	return st, st.Close, nil
}

// indexOutput — the one-line JSON result, keys in the contract order
// scanned·changed·new·removed·skipped·duration (note appended only when a
// limit left files unscanned; notice appended only when an explicit --vault
// replaced a different stored vault_path — v1.21.1 r94).
type indexOutput struct {
	Scanned  int    `json:"scanned"`
	Changed  int    `json:"changed"`
	New      int    `json:"new"`
	Removed  int    `json:"removed"`
	Skipped  int    `json:"skipped"`
	Duration string `json:"duration"`
	Note     string `json:"note,omitempty"`
	Notice   string `json:"notice,omitempty"`
}

// vaultLastScan — the meta 'vault_last_scan' JSON payload: the status vault
// row's source (last index time, file count, changed count).
type vaultLastScan struct {
	At      string `json:"at"`
	Files   int    `json:"files"`
	Changed int    `json:"changed"`
}

// runIndex — the index command handler.
func runIndex(ctx context.Context, st wire.Store, args []string, stdin io.Reader, stdout io.Writer) int {
	parsed, err := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--vault", Value: true},
			{Name: "--limit", Value: true},
		},
		Usage: "usage: flint index [--vault PATH] [--limit N]",
	})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 3
	}
	limit := 0 // 0 = unlimited (the cap alone applies)
	if parsed.Given("--limit") {
		n, perr := strconv.Atoi(parsed.Value("--limit"))
		if perr != nil || n < 0 {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
				"--limit must be a non-negative integer: %q — usage: flint index [--vault PATH] [--limit N]",
				parsed.Value("--limit"))))
			return 3
		}
		limit = n
	}
	root := ""
	explicit := false
	if parsed.Given("--vault") {
		root = parsed.Value("--vault")
		if root == "" || !parsed.HasValue("--vault") {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
				"--vault requires a path — usage: flint index [--vault PATH] [--limit N]")))
			return 3
		}
		explicit = true
	} else {
		v, ok := wire.GetMetaKV(ctx, st, metaVaultPathKey)
		if !ok || v == "" {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
				"no vault path on record — run once with --vault PATH (stored for later runs)")))
			return 3
		}
		root = v
	}
	// Root guard (v1.21.1 r91/r93/r94): a file (or a typo'd/nonexistent
	// path) is a usage error BEFORE any scan or persistence — a failing
	// scan must not leave the declared path behind in meta.
	if info, serr := os.Stat(root); serr != nil || !info.IsDir() {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
			"--vault requires a directory (the vault root) — check the path")))
		return 3
	}
	// A different explicit root replaces the stored snapshot: remember the
	// change BEFORE the scan (the persist happens after success), so the
	// output can carry the replacement notice (r94 — guidance only).
	replaceNotice := false
	if explicit {
		if prev, ok := wire.GetMetaKV(ctx, st, metaVaultPathKey); ok && prev != "" && prev != root {
			replaceNotice = true
		}
	}

	// scan — the store connects on the resolved DSN (the vault-owned pool).
	scanSt, closeScan, err := indexScanStoreFn(ctx, wire.DSNFromEnv())
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	defer closeScan()
	// Threshold overrides (v1.21.1 r93): the 3 vault keys ride the shared
	// LoadThresholds surface (TUNING §2); a load failure or unset key falls
	// back to the scan-side DefaultThresholds (the consumer-fallback rule).
	opts := vault.Options{Limit: limit}
	if th, terr := wire.LoadThresholdsFn(ctx, st); terr == nil {
		opts.ScanMaxFiles = th.VaultScanMaxFiles
		opts.SummaryMaxRunes = th.VaultSummaryMaxRunes
		if s := strings.TrimSpace(th.VaultSkipDirs); s != "" {
			opts.SkipDirs = strings.Split(s, ",")
		}
	}
	stats, err := vault.Scan(ctx, scanSt, root, opts)
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	if explicit {
		// persist the declared path AFTER a successful scan (the
		// 1-then-status-forever contract — a failed run persists nothing)
		if err := wire.SetMetaKV(ctx, st, metaVaultPathKey, root); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
	}

	// one batch dolt commit per diff — a clean diff-free rerun commits
	// nothing (a touch-only refresh IS a snapshot change: the pointer
	// update is staged, so it is committed too).
	if stats.New+stats.Changed+stats.Removed+stats.Touch > 0 {
		if _, err := st.Commit(ctx, fmt.Sprintf("index: vault files=%d changed=%d", stats.Scanned, stats.Changed)); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
	}

	// the stats snapshot for the status vault row (a metering indication —
	// a persist failure is absorbed silently, it must not fail the index).
	if b, merr := json.Marshal(vaultLastScan{
		At:      time.Now().UTC().Format(time.RFC3339),
		Files:   stats.Scanned,
		Changed: stats.Changed,
	}); merr == nil {
		_ = wire.SetMetaKV(ctx, st, metaVaultLastScanKey, string(b))
	}

	out := indexOutput{
		Scanned:  stats.Scanned,
		Changed:  stats.Changed,
		New:      stats.New,
		Removed:  stats.Removed,
		Skipped:  stats.Skipped,
		Duration: stats.Duration.Round(time.Millisecond).String(),
	}
	if stats.Skipped > 0 {
		// the limit overflow alone (Vanished is a different counter — a
		// vanished note must not suggest raising --limit)
		out.Note = render.Text(fmt.Sprintf("%d skipped — rerun with a higher --limit to cover the rest", stats.Skipped))
	}
	if replaceNotice {
		out.Notice = render.Text(fmt.Sprintf(
			"vault path changed — the previous snapshot is replaced by this scan (removed=%d)", stats.Removed))
	}
	b, _ := json.Marshal(out)
	fmt.Fprintln(stdout, string(b))
	return 0
}

// init — open-closed registration (the per-file Register convention).
func init() { Register("index", runIndex) }
