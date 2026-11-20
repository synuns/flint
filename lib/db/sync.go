// sync.go — Doltgres remote registration (wrapping the dolt_remote SQL
// function). push/pull (wrapping dolt_push·dolt_fetch+dolt_merge). The
// 30s remote-operation cap·the no-op push verdict·the diverged-push
// guidance·the engine-panic-specific guidance·SyncReset (wrapping
// dolt_reset --hard). Measured (dolthub/doltgresql, 2026-11-17):
//   - SELECT dolt_remote('add', name, url) → int 0. A duplicate name is
//     not an error; the url is quietly overwritten — duplicate blocking
//     is the dolt_remotes pre-lookup contract.
//   - Listing: calling dolt_remote() with no arguments errors ("use
//     'dolt_remotes' system table") — read via SELECT name, url FROM
//     dolt_remotes.
//   - SELECT dolt_remote('remove', name) → int 0. A missing name errors
//     with "unknown remote" — the pre-lookup normalizes it to
//     ErrRemoteNotFound.
//
// The query functions run against an arbitrary Querier (wire.Store·its
// fakes structurally satisfy it — the BumpDailyFlatKey convention), so
// CLI wiring reuses them with the db-owns-write-SQL principle intact.
// Remote urls are personal-data defense targets — the raw value never
// rides any error message, and output goes through MaskRemoteURL by
// contract.
package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Remote-registration queries — fake-based CLI tests stub the SQL with
// these constants.
const (
	// SQLRemoteExists — the duplicate/missing pre-lookup.
	SQLRemoteExists = `SELECT 1 FROM dolt_remotes WHERE name = $1`
	// SQLRemoteAdd — wrapping the dolt_remote function (new
	// registration).
	SQLRemoteAdd = `SELECT dolt_remote('add', $1, $2)`
	// SQLRemoteList — the dolt_remotes system-table query (stable
	// name-ordered sort).
	SQLRemoteList = `SELECT name, url FROM dolt_remotes ORDER BY name`
	// SQLRemoteRemove — wrapping the dolt_remote function (removal).
	SQLRemoteRemove = `SELECT dolt_remote('remove', $1)`
	// SQLSyncRemoteURL — retrieving the push/pull target remote's url
	// (doubling as the pre-lookup and securing the masking-substitution
	// raw value).
	SQLSyncRemoteURL = `SELECT name, url FROM dolt_remotes WHERE name = $1`
	// SQLSyncPush — wrapping the dolt_push function (branch 'main'
	// fixed). The return message contains the raw url (measured), so the
	// result rows are not read — only the error presence is checked.
	SQLSyncPush = `SELECT dolt_push($1, 'main')`
	// SQLSyncFetch — wrapping the dolt_fetch function (remote branch
	// 'main' only).
	SQLSyncFetch = `SELECT dolt_fetch($1, $2)`
	// SQLSyncMerge — wrapping the dolt_merge function. The argument is
	// <remote>/main (measured: the tracking-branch reference the clone
	// created — not a free branch name).
	SQLSyncMerge = `SELECT dolt_merge($1)`
	// SQLSyncReset — wrapping the dolt_reset function (v1.7.2 S2):
	// --hard + the target ref. The argument is <remote>/main — the
	// remote-tracking ref updated by fetch.
	SQLSyncReset = `SELECT dolt_reset('--hard', $1)`
	// SQLSyncHeadHash — the main branch head hash (push results·before/
	// after pull comparison).
	SQLSyncHeadHash = `SELECT hash FROM dolt_branches WHERE name = 'main'`
)

// SyncTimeout — v1.7.2 S3: the ctx cap of remote network operations
// (push·pull·reset). Terminates with an error within 30s instead of
// waiting forever when the remote is unreachable.
const SyncTimeout = 30 * time.Second

// Remote errors — the discrimination basis of the CLI exit-code
// contract (errors.Is).
var (
	// ErrRemoteExists — the add-target name is already registered
	// (dolt_remote itself quietly overwrites, so only the pre-lookup
	// catches it).
	ErrRemoteExists = errors.New("remote name already exists")
	// ErrRemoteNotFound — the remove-target name is not registered.
	ErrRemoteNotFound = errors.New("remote not found")
	// ErrMergeConflict — a pull merge conflict (the no-automatic-
	// resolution contract — guidance accompanies it; the cause is
	// wrapping-preserved).
	ErrMergeConflict = errors.New("sync pull: merge conflict")
	// ErrPushDiverged — v1.7.2 S6: a diverged push (rejected — the
	// remote main is ahead or split). No automatic resolution; only the
	// pull-first guidance accompanies it.
	ErrPushDiverged = errors.New("sync push: remote diverged")
	// ErrRemotePanic — v1.7.2 S1 mitigation: an engine-internal panic
	// during a remote operation ("DoltgresHandler caught panic"
	// detected). Concurrent multi-device use is the main cause — a
	// dedicated guidance accompanies it.
	ErrRemotePanic = errors.New("sync: storage engine panic")
)

// MergeConflictGuide — the manual-resolution guidance on conflict (no
// automatic resolution — preventing destruction).
const MergeConflictGuide = "automatic resolution is disabled; resolve manually with the dolt CLI (dolt_conflicts) and re-run flint sync pull"

// PushDivergedGuide — v1.7.2 S6: the diverged-push guidance. Points to
// flint sync pull, not dolt pull (the user's operating contract is the
// flint CLI only).
const PushDivergedGuide = "the remote 'main' has diverged — run 'flint sync pull' first, resolve if needed, then push"

// RemotePanicGuide — v1.7.2 S1 mitigation: the engine-panic-specific
// guidance (concurrent multi-device use unsupported·a single active
// device recommended — the recovery, reset --to-remote, carries the
// remote name in the guidance).
const RemotePanicGuide = "concurrent multi-device use is not supported by the storage engine; keep a single active device per database"

// Remote — one dolt_remotes row. The URL is kept raw but exposure must
// go through MaskRemoteURL (the calling CLI's output contract).
type Remote struct {
	Name string
	URL  string
}

// RemoteAdd — remote registration. A duplicate name yields
// ErrRemoteExists without running dolt_remote (measured: dolt_remote
// quietly overwrites duplicates — fundamentally blocking replacement
// with a remote other than the user's intent). The error wrapping never
// carries the raw url.
func RemoteAdd(ctx context.Context, q Querier, name, remoteURL string) error {
	rows, err := q.Query(ctx, SQLRemoteExists, name)
	if err != nil {
		return fmt.Errorf("sync remote add: check existing: %w", err)
	}
	if len(rows) > 0 {
		return fmt.Errorf("%w: %s", ErrRemoteExists, name)
	}
	if _, err := q.Query(ctx, SQLRemoteAdd, name, remoteURL); err != nil {
		// The url is passed as an argument, so preserve the cause
		// defensively, but this wrapper never re-records the raw url (the
		// underlying error message is the standard dolt_remote wording —
		// url absence confirmed by slow-run measurement).
		return fmt.Errorf("sync remote add: dolt_remote: %w", err)
	}
	return nil
}

// RemoteList — all registered remotes (name-ordered). The url is
// retrieved raw — masking belongs to the output path (CLI); the source
// is preserved (reused by push/pull).
func RemoteList(ctx context.Context, q Querier) ([]Remote, error) {
	rows, err := q.Query(ctx, SQLRemoteList)
	if err != nil {
		return nil, fmt.Errorf("sync remote list: %w", err)
	}
	remotes := make([]Remote, 0, len(rows))
	for _, row := range rows {
		if len(row) < 2 {
			return nil, fmt.Errorf("sync remote list: unexpected row arity %d", len(row))
		}
		remotes = append(remotes, Remote{
			Name: colText(row[0]),
			URL:  colText(row[1]),
		})
	}
	return remotes, nil
}

// RemoteRemove — remote removal. A missing name yields
// ErrRemoteNotFound without running dolt_remote (measured: dolt_remote
// raises "unknown remote", but the pre-lookup stabilizes the error
// contract).
func RemoteRemove(ctx context.Context, q Querier, name string) error {
	rows, err := q.Query(ctx, SQLRemoteExists, name)
	if err != nil {
		return fmt.Errorf("sync remote remove: check existing: %w", err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("%w: %s", ErrRemoteNotFound, name)
	}
	if _, err := q.Query(ctx, SQLRemoteRemove, name); err != nil {
		return fmt.Errorf("sync remote remove: dolt_remote: %w", err)
	}
	return nil
}

// MaskRemoteURL — remote-url masking: exposes scheme+host only
// (credentials·port·path·query all hidden). A scheme without a host,
// like file://, yields "scheme://" only. Unparsable·scheme-less raw
// values are replaced with a fixed token so the raw value is never
// recoverable (personal-data defense).
func MaskRemoteURL(raw string) string {
	const unparsable = "(unparsable)"
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || strings.ContainsAny(raw, " \t\r\n") {
		return unparsable
	}
	host := u.Hostname() // strips userinfo·port
	if host == "" {
		return u.Scheme + "://"
	}
	return u.Scheme + "://" + host
}

// SyncResult — one push/pull result (the JSON output source). Hash is
// the main head.
type SyncResult struct {
	Status string
	Hash   string
}

// SyncPush — runs dolt_push(remote, 'main'). An unregistered remote
// yields ErrRemoteNotFound without running dolt_push (measured:
// dolt_push's unregistered error is "fatal: remote 'x' not found" — the
// pre-lookup stabilizes the error contract). Execution errors
// (unreachable etc.) preserve the cause, but if the message carries the
// raw url it is replaced with the masked form. v1.7.2 T3: the
// SyncTimeout cap on all operations (S3). The result rows are read
// internally for the no-op ("Everything up-to-date") verdict only and
// never carried to the output (S6 — rows may contain the raw url, so
// exposure beyond the verdict is forbidden). rejected-class errors
// yield ErrPushDiverged + the 'flint sync pull' guidance (S6).
func SyncPush(ctx context.Context, q Querier, remote string) (SyncResult, error) {
	ctx, cancel := context.WithTimeout(ctx, SyncTimeout)
	defer cancel()
	rawURL, err := syncRemoteURL(ctx, q, remote)
	if err != nil {
		return SyncResult{}, err
	}
	rows, err := q.Query(ctx, SQLSyncPush, remote)
	if err != nil {
		werr := maskRemoteError(fmt.Errorf("sync push: dolt_push: %w", err), rawURL)
		if lp := strings.ToLower(err.Error()); strings.Contains(lp, "rejected") || strings.Contains(lp, "non-fast-forward") {
			werr = fmt.Errorf("%w: %s (cause: %v)", ErrPushDiverged, PushDivergedGuide, werr)
		}
		return SyncResult{}, werr
	}
	status := "pushed"
	if pushRowsUpToDate(rows) {
		status = "up_to_date"
	}
	hash, err := syncHeadHash(ctx, q)
	if err != nil {
		return SyncResult{}, err
	}
	return SyncResult{Status: status, Hash: hash}, nil
}

// SyncPull — dolt_fetch(remote,'main') + dolt_merge(<remote>/main). dry
// stops at fetch (nothing applied). A merge conflict yields
// ErrMergeConflict + the manual-resolution guidance (measured: on
// conflict the @autocommit transaction rolls back and the working set
// is preserved — no automatic resolution·no partial application). The
// before/after hash comparison distinguishes merged/up_to_date.
// v1.7.2 T3: the SyncTimeout cap (S3)·on engine-panic detection
// ("DoltgresHandler caught panic"), ErrRemotePanic + the multi-device
// guidance (S1 mitigation).
func SyncPull(ctx context.Context, q Querier, remote string, dry bool) (SyncResult, error) {
	ctx, cancel := context.WithTimeout(ctx, SyncTimeout)
	defer cancel()
	rawURL, err := syncRemoteURL(ctx, q, remote)
	if err != nil {
		return SyncResult{}, err
	}
	if _, err := q.Query(ctx, SQLSyncFetch, remote, "main"); err != nil {
		return SyncResult{}, syncRemoteOpErr(fmt.Errorf("sync pull: dolt_fetch: %w", err), rawURL, remote)
	}
	if dry {
		hash, err := syncHeadHash(ctx, q)
		if err != nil {
			return SyncResult{}, err
		}
		return SyncResult{Status: "fetched", Hash: hash}, nil
	}
	before, err := syncHeadHash(ctx, q)
	if err != nil {
		return SyncResult{}, err
	}
	if _, err := q.Query(ctx, SQLSyncMerge, remote+"/main"); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "conflict") {
			return SyncResult{}, fmt.Errorf("%w: %s (cause: %v)", ErrMergeConflict, MergeConflictGuide, err)
		}
		return SyncResult{}, syncRemoteOpErr(fmt.Errorf("sync pull: dolt_merge: %w", err), rawURL, remote)
	}
	after, err := syncHeadHash(ctx, q)
	if err != nil {
		return SyncResult{}, err
	}
	status := "up_to_date"
	if after != before {
		status = "merged"
	}
	return SyncResult{Status: status, Hash: after}, nil
}

// SyncReset — v1.7.2 S2 mitigation: dolt_fetch(remote,'main') then
// dolt_reset('--hard', <remote>/main) — rolls main back to the remote
// head and discards the local uncommitted working set·local-only
// commits (a destructive operation — the CLI-side --yes gate is the
// premise). An unregistered remote yields ErrRemoteNotFound without
// running fetch.
func SyncReset(ctx context.Context, q Querier, remote string) (SyncResult, error) {
	ctx, cancel := context.WithTimeout(ctx, SyncTimeout)
	defer cancel()
	rawURL, err := syncRemoteURL(ctx, q, remote)
	if err != nil {
		return SyncResult{}, err
	}
	if _, err := q.Query(ctx, SQLSyncFetch, remote, "main"); err != nil {
		return SyncResult{}, syncRemoteOpErr(fmt.Errorf("sync reset: dolt_fetch: %w", err), rawURL, remote)
	}
	if _, err := q.Query(ctx, SQLSyncReset, remote+"/main"); err != nil {
		return SyncResult{}, syncRemoteOpErr(fmt.Errorf("sync reset: dolt_reset: %w", err), rawURL, remote)
	}
	hash, err := syncHeadHash(ctx, q)
	if err != nil {
		return SyncResult{}, err
	}
	return SyncResult{Status: "reset", Hash: hash}, nil
}

// syncRemoteOpErr — the common remote-operation error wrapping: an
// engine panic (containing "caught panic" — covering the measured
// "DoltgresHandler caught panic" wording) yields ErrRemotePanic + the
// multi-device guidance + the reset recovery guidance (remote name
// included). Everything else is masked only.
func syncRemoteOpErr(err error, rawURL, remote string) error {
	if strings.Contains(strings.ToLower(err.Error()), "caught panic") {
		return fmt.Errorf("%w: %s; recover with 'flint sync reset --to-remote %s' once the database is idle (cause: %v)",
			ErrRemotePanic, RemotePanicGuide, remote, maskRemoteError(err, rawURL))
	}
	return maskRemoteError(err, rawURL)
}

// pushRowsUpToDate — the dolt_push result-row verdict: an up-to-date-
// class phrase in any cell means a no-op push (S6). The rows are used
// for the verdict only and never returned.
func pushRowsUpToDate(rows [][]any) bool {
	for _, row := range rows {
		for _, cell := range row {
			if lp := strings.ToLower(colText(cell)); strings.Contains(lp, "up-to-date") || strings.Contains(lp, "up to date") {
				return true
			}
		}
	}
	return false
}

// syncRemoteURL — retrieves the registered url by remote name.
// Unregistered yields ErrRemoteNotFound. The url is used only as the
// masking-substitution raw value; the return value itself is never
// exposed.
func syncRemoteURL(ctx context.Context, q Querier, remote string) (string, error) {
	rows, err := q.Query(ctx, SQLSyncRemoteURL, remote)
	if err != nil {
		return "", fmt.Errorf("sync: lookup remote: %w", err)
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("%w: %s", ErrRemoteNotFound, remote)
	}
	if len(rows[0]) < 2 {
		return "", fmt.Errorf("sync: lookup remote: unexpected row arity %d", len(rows[0]))
	}
	return colText(rows[0][1]), nil
}

// syncHeadHash — the main branch head hash (0 rows·corrupt arity is an
// error). v1.7.2 S6: the missing-main message guides with both the
// cause ('main' absent) and the action (flint operates on 'main' only).
func syncHeadHash(ctx context.Context, q Querier) (string, error) {
	rows, err := q.Query(ctx, SQLSyncHeadHash)
	if err != nil {
		return "", fmt.Errorf("sync: head hash: %w", err)
	}
	if len(rows) == 0 || len(rows[0]) < 1 {
		return "", fmt.Errorf("sync: main branch not found — flint sync operates on the 'main' branch; make a commit on 'main' before sync")
	}
	return colText(rows[0][0]), nil
}

// maskRemoteError — replaces the raw remote url in an error message
// with the masked form (server errors may carry the url verbatim — per
// the measured push success message). The substitution breaks the error
// chain, but this path is a terminal exit-1 error with no chain
// consumers (the no-url-exposure contract takes precedence).
func maskRemoteError(err error, rawURL string) error {
	if rawURL == "" || !strings.Contains(err.Error(), rawURL) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), rawURL, MaskRemoteURL(rawURL)))
}

// colText — normalizes a cell value to text (pgx text is a string, but
// defensive against arbitrary types via the [][]any path — fmt.Sprint
// fallback).
func colText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
