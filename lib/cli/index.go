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

const (
	metaVaultPathKey     = "vault_path"
	metaVaultLastScanKey = "vault_last_scan"
)

var indexScanStoreFn = func(ctx context.Context, dsn string) (vault.Store, func(), error) {
	st, err := vault.Open(ctx, dsn)
	if err != nil {
		return nil, nil, err
	}
	return st, st.Close, nil
}

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

type vaultLastScan struct {
	At      string `json:"at"`
	Files   int    `json:"files"`
	Changed int    `json:"changed"`
}

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
	limit := 0
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

	if info, serr := os.Stat(root); serr != nil || !info.IsDir() {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
			"--vault requires a directory (the vault root) — check the path")))
		return 3
	}

	replaceNotice := false
	if explicit {
		if prev, ok := wire.GetMetaKV(ctx, st, metaVaultPathKey); ok && prev != "" && prev != root {
			replaceNotice = true
		}
	}

	scanSt, closeScan, err := indexScanStoreFn(ctx, wire.DSNFromEnv())
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	defer closeScan()

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

		if err := wire.SetMetaKV(ctx, st, metaVaultPathKey, root); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
	}

	if stats.New+stats.Changed+stats.Removed+stats.Touch > 0 {
		if _, err := st.Commit(ctx, fmt.Sprintf("index: vault files=%d changed=%d", stats.Scanned, stats.Changed)); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return store.MapExit(err)
		}
	}

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

func init() { Register("index", runIndex) }
