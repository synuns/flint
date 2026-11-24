package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/synuns/flint/data"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/i18n"
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

const (
	demoDSNSuffix   = "_demo"
	demoTopicPrefix = "demo/"
	demoOwnerKey    = "demo_owner"
	demoOwnerValue  = "flint-seed-demo"
	demoDSNEnv      = "FLINT_DEMO_DSN"
)

var seedWriteFn = runWrite

var seedEnsureDBFn = func(ctx context.Context, adminDSN, dbName string) error {
	pool, err := db.Connect(ctx, adminDSN)
	if err != nil {
		return err
	}
	defer pool.Close()
	rows, err := pool.Query(ctx, `SELECT 1 FROM pg_database WHERE datname = $1`, dbName)
	if err != nil {
		return err
	}
	exists := rows.Next()
	rows.Close()
	if exists {
		return nil
	}
	_, err = pool.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{dbName}.Sanitize())
	if seedToleratedCreateErr(err) {
		return nil
	}
	return err
}

func seedToleratedCreateErr(err error) bool {
	if err == nil {
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgDuplicateDatabase
}

const pgDuplicateDatabase = "42P04"

func deriveDemoDSN(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("seed: DSN parse failed: %w", err)
	}
	if u.Scheme == "" {
		return "", fmt.Errorf("seed: keyword-form DSN cannot derive a demo database — set %s to the demo database DSN explicitly", demoDSNEnv)
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" {
		return "", fmt.Errorf("seed: no database name in DSN — set %s to the demo database DSN explicitly", demoDSNEnv)
	}
	u.Path = "/" + name + demoDSNSuffix
	return u.String(), nil
}

func resolveDemoDSN() (string, string, error) {
	if d := os.Getenv(demoDSNEnv); d != "" {
		name, err := dsnDatabaseName(d)
		if err != nil {
			return "", "", fmt.Errorf("seed: %s: %w", demoDSNEnv, err)
		}
		return d, name, nil
	}
	demo, err := deriveDemoDSN(dsnFromEnv())
	if err != nil {
		return "", "", err
	}
	return demo, dsnDBName(demo), nil
}

func dsnDatabaseName(dsn string) (string, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "", err
	}
	if cfg.Database == "" {
		return "", errors.New("no database name")
	}
	return cfg.Database, nil
}

func dsnDBName(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}

type seedSummary struct {
	Status   string            `json:"status"`
	Database string            `json:"database"`
	Results  map[string]string `json:"results,omitempty"`
	Targets  int               `json:"targets,omitempty"`
	Deleted  int               `json:"deleted,omitempty"`
	Next     string            `json:"next,omitempty"`
}

func runSeed(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--demo"},
			{Name: "--reset"},
			{Name: "--yes"},
		},
		Usage: i18n.T("usage.seed"),
	})
	if perr != nil {
		fmt.Fprintln(stdout, wire.ErrLine(perr))
		return 3
	}
	if !res.Given("--demo") {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%s", i18n.T("err.demo_required")+" — "+i18n.T("usage.seed"))))
		return 3
	}
	demoDSN, dbName, err := resolveDemoDSN()
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf("%v", err)))
		return 3
	}

	overridden := os.Getenv(demoDSNEnv) != ""
	demoSt, err := newStore(ctx, demoDSN)
	if err != nil && !overridden {
		if ensureErr := seedEnsureDBFn(ctx, dsnFromEnv(), dbName); ensureErr == nil {
			demoSt, err = newStore(ctx, demoDSN)
		}
	}
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return store.MapExit(err)
	}
	defer demoSt.Close()
	if res.Given("--reset") {
		return runSeedReset(ctx, demoSt, res.Given("--yes"), dbName, stdout)
	}
	return runSeedDemo(ctx, demoSt, dbName, stdout)
}

func runSeedDemo(ctx context.Context, demoSt Store, dbName string, stdout io.Writer) int {
	lines := strings.Split(string(data.SeedJSONL()), "\n")
	byKind := map[string][]string{}
	var order []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("seed: embedded seed.jsonl parse failure: %w", err)))
			return 1
		}
		kind, _ := p["kind"].(string)
		switch kind {
		case "knowledge":
			p["topic"] = demoTopicPrefix + fmt.Sprint(p["topic"])
		case "project":
			p["name"] = demoTopicPrefix + fmt.Sprint(p["name"])
		default:
			fmt.Fprintln(stdout, wire.ErrLine(fmt.Errorf("seed: embedded kind %q not seedable", kind)))
			return 1
		}
		b, err := json.Marshal(p)
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 1
		}
		if _, ok := byKind[kind]; !ok {
			order = append(order, kind)
		}
		byKind[kind] = append(byKind[kind], string(b))
	}
	results := map[string]string{}
	for _, kind := range order {
		var buf strings.Builder
		code := seedWriteFn(ctx, demoSt, []string{kind},
			strings.NewReader(strings.Join(byKind[kind], "\n")+"\n"), &buf)
		results[kind] = writeStatus(buf.String())
		if code != 0 {

			fmt.Fprintln(stdout, buf.String())
			return code
		}
	}

	if err := setMetaKV(ctx, demoSt, demoOwnerKey, demoOwnerValue); err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	b, err := json.Marshal(seedSummary{Status: "ok", Database: dbName, Results: results})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

func writeStatus(line string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &m); err != nil {
		return ""
	}
	s, _ := m["status"].(string)
	return s
}

func runSeedReset(ctx context.Context, demoSt Store, yes bool, dbName string, stdout io.Writer) int {
	owner, ok := getMetaKV(ctx, demoSt, demoOwnerKey)
	if !ok || owner != demoOwnerValue {
		fmt.Fprintln(stdout, wire.ErrLine(wire.UsageErrorf(
			"seed --reset: demo_owner marker %s — refusing to reset %s (run flint seed --demo first, or point FLINT_DEMO_DSN at a demo database)",
			map[bool]string{true: "mismatch", false: "missing"}[ok], dbName)))
		return 3
	}
	n := 0
	for _, q := range []struct {
		sql  string
		like string
	}{
		{`SELECT count(*) FROM knowledge WHERE topic LIKE $1`, demoTopicPrefix + "%"},
		{`SELECT count(*) FROM projects WHERE name LIKE $1`, demoTopicPrefix + "%"},
	} {
		rows, err := wire.QueryOnce(ctx, demoSt, q.sql, []any{q.like})
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 1
		}
		if len(rows) > 0 {
			if v, ok := rows[0][0].(int64); ok {
				n += int(v)
			}
		}
	}
	if !yes {
		b, err := json.Marshal(seedSummary{
			Status: "dry_run", Database: dbName, Targets: n,
			Next: fmt.Sprintf("would delete %d demo rows — re-run with --yes to execute", n),
		})
		if err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 1
		}
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	deleted := 0
	for _, q := range []struct {
		sql  string
		like string
	}{
		{`DELETE FROM knowledge WHERE topic LIKE $1`, demoTopicPrefix + "%"},
		{`DELETE FROM projects WHERE name LIKE $1`, demoTopicPrefix + "%"},
		{`DELETE FROM meta WHERE key = $1`, demoOwnerKey},
	} {
		if _, err := wire.QueryOnce(ctx, demoSt, q.sql, []any{q.like}); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 1
		}
		if q.like != demoOwnerKey {
			deleted = n
		}
	}
	b, err := json.Marshal(seedSummary{Status: "ok", Database: dbName, Deleted: deleted})
	if err != nil {
		fmt.Fprintln(stdout, wire.ErrLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

func init() {
	Register("seed", runSeed)
}
