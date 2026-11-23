package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	SQLRemoteExists = `SELECT 1 FROM dolt_remotes WHERE name = $1`

	SQLRemoteAdd = `SELECT dolt_remote('add', $1, $2)`

	SQLRemoteList = `SELECT name, url FROM dolt_remotes ORDER BY name`

	SQLRemoteRemove = `SELECT dolt_remote('remove', $1)`

	SQLSyncRemoteURL = `SELECT name, url FROM dolt_remotes WHERE name = $1`

	SQLSyncPush = `SELECT dolt_push($1, 'main')`

	SQLSyncFetch = `SELECT dolt_fetch($1, $2)`

	SQLSyncMerge = `SELECT dolt_merge($1)`

	SQLSyncReset = `SELECT dolt_reset('--hard', $1)`

	SQLSyncHeadHash = `SELECT hash FROM dolt_branches WHERE name = 'main'`
)

const SyncTimeout = 30 * time.Second

var (
	ErrRemoteExists = errors.New("remote name already exists")

	ErrRemoteNotFound = errors.New("remote not found")

	ErrMergeConflict = errors.New("sync pull: merge conflict")

	ErrPushDiverged = errors.New("sync push: remote diverged")

	ErrRemotePanic = errors.New("sync: storage engine panic")
)

const MergeConflictGuide = "automatic resolution is disabled; resolve manually with the dolt CLI (dolt_conflicts) and re-run flint sync pull"

const PushDivergedGuide = "the remote 'main' has diverged — run 'flint sync pull' first, resolve if needed, then push"

const RemotePanicGuide = "concurrent multi-device use is not supported by the storage engine; keep a single active device per database"

type Remote struct {
	Name string
	URL  string
}

func RemoteAdd(ctx context.Context, q Querier, name, remoteURL string) error {
	rows, err := q.Query(ctx, SQLRemoteExists, name)
	if err != nil {
		return fmt.Errorf("sync remote add: check existing: %w", err)
	}
	if len(rows) > 0 {
		return fmt.Errorf("%w: %s", ErrRemoteExists, name)
	}
	if _, err := q.Query(ctx, SQLRemoteAdd, name, remoteURL); err != nil {

		return fmt.Errorf("sync remote add: dolt_remote: %w", err)
	}
	return nil
}

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

func MaskRemoteURL(raw string) string {
	const unparsable = "(unparsable)"
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || strings.ContainsAny(raw, " \t\r\n") {
		return unparsable
	}
	host := u.Hostname()
	if host == "" {
		return u.Scheme + "://"
	}
	return u.Scheme + "://" + host
}

type SyncResult struct {
	Status string
	Hash   string
}

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

func syncRemoteOpErr(err error, rawURL, remote string) error {
	if strings.Contains(strings.ToLower(err.Error()), "caught panic") {
		return fmt.Errorf("%w: %s; recover with 'flint sync reset --to-remote %s' once the database is idle (cause: %v)",
			ErrRemotePanic, RemotePanicGuide, remote, maskRemoteError(err, rawURL))
	}
	return maskRemoteError(err, rawURL)
}

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

func maskRemoteError(err error, rawURL string) error {
	if rawURL == "" || !strings.Contains(err.Error(), rawURL) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), rawURL, MaskRemoteURL(rawURL)))
}

func colText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
