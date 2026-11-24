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
	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

var syncUsage = i18n.T("usage.sync")

func init() { Register("sync", runSync) }

func runSync(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", syncUsage)))
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
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", syncUsage)))
		return 3
	}
}

func runSyncRemote(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", syncUsage)))
		return 3
	}
	switch args[0] {
	case "add":
		return runSyncRemoteAdd(ctx, st, args[1:], stdout)
	case "list":
		if len(args) != 1 {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", syncUsage)))
			return 3
		}
		return runSyncRemoteList(ctx, st, stdout)
	case "remove":
		return runSyncRemoteRemove(ctx, st, args[1:], stdout)
	default:
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", syncUsage)))
		return 3
	}
}

func runSyncPush(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	if len(args) != 1 || args[0] == "" {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", syncUsage)))
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

func runSyncPull(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	if len(args) < 1 || len(args) > 2 || args[0] == "" {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", syncUsage)))
		return 3
	}
	dry := false
	if len(args) == 2 {
		if args[1] != "--dry" {
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", syncUsage)))
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

func runSyncReset(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	name := ""
	confirm := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--to-remote":
			if i+1 >= len(args) || args[i+1] == "" {
				fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", syncUsage)))
				return 3
			}
			name = args[i+1]
			i++
		case "--yes":
			confirm = true
		default:
			fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", syncUsage)))
			return 3
		}
	}
	if name == "" {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", syncUsage)))
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

func runSyncRemoteAdd(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", syncUsage)))
		return 3
	}
	name, remoteURL := args[0], args[1]
	if name == "" {
		fmt.Fprintln(stdout, wire.ErrLine(&store.SchemaError{Field: "name", Expect: "non-empty"}))
		return 3
	}

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

func runSyncRemoteRemove(ctx context.Context, st Store, args []string, stdout io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", syncUsage)))
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

func syncOpExit(stdout io.Writer, err error) int {
	fmt.Fprintln(stdout, wire.ErrLine(err))
	if errors.Is(err, db.ErrRemoteExists) || errors.Is(err, db.ErrRemoteNotFound) {
		return 3
	}
	return 1
}

func syncLineJSON(w io.Writer, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintln(w, string(b))
}

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
