// sync.go — CLI entry for `flint sync remote add|list|remove`.
// Extended with `flint sync push|pull [--dry]` (manual-resolution guidance on conflict).
// The query implementation is owned by lib/db/sync.go (wrapping dolt_remote) — this file handles only argument
// validation, exit contracts, and output masking (the db-owns-write-SQL-literals principle,
// the BumpDailyFlatKey convention). The raw remote url never
// appears in any stdout line — success, error, or usage: list's url is only the
// db.MaskRemoteURL (scheme+host) result, and the add success line carries only name and status.
// Consistent with the "sync" entry in usageOrder — Register lives here, the usage wording is
// the single source in wire.usageOrder. push/pull were also extended in this file.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// syncUsage — usage guidance (the shared exit-3 wording — url examples show only scheme+host
// form; real-format guidance never includes credential examples). v1.7.2 T3:
// added the reset subcommand (S2 mitigation).
const syncUsage = "usage: flint sync remote add <name> <url> | flint sync remote list | flint sync remote remove <name> | flint sync push <remote> | flint sync pull <remote> [--dry] | flint sync reset --to-remote <name> [--yes]"

func init() { Register("sync", runSync) }

// runSync — sync command entry (wire.Handler signature). Contract:
//   - usage errors (arg count, invalid sub, empty value, scheme-less url) exit 3
//   - duplicate add or missing remove exits 3 (usage-class error contract)
//   - nil store or server-down exits 2; other execution errors exit 1 (cause preserved)
//   - success is one result-line JSON on stdout, exit 0
func runSync(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
	switch args[0] {
	case "remote":
		return runSyncRemote(ctx, st, args[1:], stdout)
	case "push":
		return runSyncPush(ctx, st, args[1:], stdout)
	case "pull":
		return runSyncPull(ctx, st, args[1:], stdout)
	case "reset":
		return runSyncReset(ctx, st, args[1:], stdout)
	default:
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
}

// runSyncRemote — subcommand branching for `flint sync remote ...`.
func runSyncRemote(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
	switch args[0] {
	case "add":
		return runSyncRemoteAdd(ctx, st, args[1:], stdout)
	case "list":
		if len(args) != 1 {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
			return 3
		}
		return runSyncRemoteList(ctx, st, stdout)
	case "remove":
		return runSyncRemoteRemove(ctx, st, args[1:], stdout)
	default:
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
}

// runSyncPush — `flint sync push <remote>`: the dolt_push(remote,'main') result as
// {remote,status,hash} JSON. Unregistered exits 3; execution errors exit 1 (masked).
func runSyncPush(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	if len(args) != 1 || args[0] == "" {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
	remote := args[0]
	if !syncStoreReady(ctx, st, stdout) {
		return store.MapExit(store.ErrServerDown)
	}
	res, err := db.SyncPush(ctx, st, remote)
	if err != nil {
		return syncOpExit(stdout, err)
	}
	syncLineJSON(stdout, map[string]string{"remote": remote, "status": res.Status, "hash": res.Hash})
	return 0
}

// runSyncPull — `flint sync pull <remote> [--dry]`: dolt_fetch+dolt_merge.
// conflicts exit 1 + manual-resolution guidance (no auto-resolution). --dry stops at fetch.
func runSyncPull(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	if len(args) < 1 || len(args) > 2 || args[0] == "" {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
	dry := false
	if len(args) == 2 {
		if args[1] != "--dry" {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
			return 3
		}
		dry = true
	}
	remote := args[0]
	if !syncStoreReady(ctx, st, stdout) {
		return store.MapExit(store.ErrServerDown)
	}
	res, err := db.SyncPull(ctx, st, remote, dry)
	if err != nil {
		return syncOpExit(stdout, err)
	}
	syncLineJSON(stdout, map[string]string{"remote": remote, "status": res.Status, "hash": res.Hash})
	return 0
}

// runSyncReset — `flint sync reset --to-remote <name> [--yes]`: v1.7.2
// S2 mitigation. After fetch, resets main to the remote head with dolt_reset --hard (discarding local
// uncommitted changes and local-only commits — a destructive operation). Carrying over the teardown convention:
// dry-run by default (loss warning, no execution, exit 0); --yes alone executes. An unregistered remote
// exits 3 (the db ErrRemoteNotFound contract).
func runSyncReset(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	name := ""
	confirm := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--to-remote":
			if i+1 >= len(args) || args[i+1] == "" {
				fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
				return 3
			}
			name = args[i+1]
			i++
		case "--yes":
			confirm = true
		default:
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
			return 3
		}
	}
	if name == "" {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
	if !confirm {
		syncLineJSON(stdout, map[string]any{
			"remote":  name,
			"status":  "dry_run",
			"warning": "reset --hard discards ALL local uncommitted changes and local-only commits on 'main'",
			"next":    "re-run with --yes to execute",
		})
		return 0
	}
	if !syncStoreReady(ctx, st, stdout) {
		return store.MapExit(store.ErrServerDown)
	}
	res, err := db.SyncReset(ctx, st, name)
	if err != nil {
		return syncOpExit(stdout, err)
	}
	syncLineJSON(stdout, map[string]string{"remote": name, "status": res.Status, "hash": res.Hash})
	return 0
}

// syncStoreReady — nil-store/server-down check (carrying over the export convention — one ErrLine
// + MapExit, exit 2).
func syncStoreReady(ctx context.Context, st Store, stdout io.Writer) bool {
	if st == nil {
		fmt.Fprintln(stdout, wire.ErrLine(store.ErrServerDown))
		return false
	}
	if err := st.Ping(ctx); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return false
	}
	return true
}

// runSyncRemoteAdd — `flint sync remote add <name> <url>`. v1.7.2 T3 S5:
// the url scheme whitelist admits only https, http, and file — dolt_push/fetch has the
// remote url interpreted by the Doltgres server, so host-local schemes like postgres:// are
// misconfiguration to register in the first place (add-time exit 3). The success line carries no url
// (name and status only). S4: one line of guidance about the file:// path interpretation right after success.
func runSyncRemoteAdd(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
	name, remoteURL := args[0], args[1]
	if name == "" {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "name", Expect: "non-empty"}))
		return 3
	}
	// url validation — missing scheme, whitespace, malformed, or disallowed scheme is a usage error (the raw value is
	// not echoed in the guidance — scheme-form guidance only).
	if !validRemoteURL(remoteURL) {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "url", Expect: "remote URL with allowed scheme (https://, http://, file://)"}))
		return 3
	}
	if !syncStoreReady(ctx, st, stdout) {
		return store.MapExit(store.ErrServerDown)
	}
	if err := db.RemoteAdd(ctx, st, name, remoteURL); err != nil {
		return syncOpExit(stdout, err)
	}
	syncLineJSON(stdout, map[string]string{"remote": name, "status": "added"})
	fmt.Fprintln(stdout, "note: file:// paths resolve on the Doltgres server")
	return 0
}

// runSyncRemoteList — `flint sync remote list`. The url is masked only.
func runSyncRemoteList(ctx context.Context, st Store, stdout io.Writer) int {
	if !syncStoreReady(ctx, st, stdout) {
		return store.MapExit(store.ErrServerDown)
	}
	remotes, err := db.RemoteList(ctx, st)
	if err != nil {
		return syncOpExit(stdout, err)
	}
	out := make([]map[string]string, 0, len(remotes))
	for _, r := range remotes {
		out = append(out, map[string]string{"name": r.Name, "url": db.MaskRemoteURL(r.URL)})
	}
	syncLineJSON(stdout, map[string]any{"remotes": out})
	return 0
}

// runSyncRemoteRemove — `flint sync remote remove <name>`.
func runSyncRemoteRemove(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("%s", syncUsage)))
		return 3
	}
	name := args[0]
	if name == "" {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "name", Expect: "non-empty"}))
		return 3
	}
	if !syncStoreReady(ctx, st, stdout) {
		return store.MapExit(store.ErrServerDown)
	}
	if err := db.RemoteRemove(ctx, st, name); err != nil {
		return syncOpExit(stdout, err)
	}
	syncLineJSON(stdout, map[string]string{"remote": name, "status": "removed"})
	return 0
}

// syncOpExit — exit mapping for remote-operation errors. Duplicate and missing names are usage-class
// exit 3; other execution errors exit 1 (ErrLine preserves the cause — that no raw url
// appears in the error chain is the db-side wrapping contract).
func syncOpExit(stdout io.Writer, err error) int {
	fmt.Fprintln(stdout, wire.ErrLine(err))
	if errors.Is(err, db.ErrRemoteExists) || errors.Is(err, db.ErrRemoteNotFound) {
		return 3
	}
	return 1
}

// syncLineJSON — one result-line JSON output (the ErrLine channel convention — one stdout line).
func syncLineJSON(w io.Writer, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return // no unmarshalable values — effectively unreachable
	}
	fmt.Fprintln(w, string(b))
}

// validRemoteURL — v1.7.2 T3 S5: add url validation. Beyond parseability and no whitespace,
// a scheme whitelist (https, http, file) — the remote url is interpreted by the Doltgres server, so
// other schemes (postgres:// etc.) are rejected at add time.
func validRemoteURL(raw string) bool {
	if raw == "" || strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return false
	}
	switch u.Scheme {
	case "https", "http", "file":
		return true
	}
	return false
}
