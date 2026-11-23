// seed command — `flint seed --demo [--reset] [--yes]` (v1.26.0 T2).
// Loads the embedded synthetic seed dataset (data.SeedJSONL) into an isolated
// demo database derived from the running DSN (database name + `_demo`;
// FLINT_DEMO_DSN overrides verbatim), going through the single RunWrite path
// (validation, hash dedup, one commit per kind batch — a re-run is
// all-duplicate exit 0). Every seeded row carries the fixed demo/ identifier
// prefix; --reset deletes ONLY rows with that prefix and ONLY after the
// demo_owner ownership marker matches (mismatch is exit 3; --yes required).
// Derivation failure (no database name / unparseable DSN / keyword-form DSN
// that cannot be URL-derived) is invalid-arg exit 3 with FLINT_DEMO_DSN
// guidance (v1.26.1). The Makefile `seed` target (disk file,
// production DSN) is unaffected — this is the binary-user route.
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
	"github.com/synuns/flint/lib/store"
	"github.com/synuns/flint/lib/wire"
)

// Demo-seed constants — the fixed identifiers the --reset guard keys on.
const (
	demoDSNSuffix   = "_demo"           // derived database-name suffix
	demoTopicPrefix = "demo/"           // seeded topic/name identifier prefix
	demoOwnerKey    = "demo_owner"      // meta ownership marker key
	demoOwnerValue  = "flint-seed-demo" // the one accepted marker value
	demoDSNEnv      = "FLINT_DEMO_DSN"  // the isolation escape hatch
)

// seedWriteFn — the per-kind write seam (the single RunWrite path; tests
// swap in a recorder).
var seedWriteFn = runWrite

// seedEnsureDBFn — the demo-database creation seam: connects the admin DSN
// and creates dbName when missing (the drill.go CREATE DATABASE convention).
// A concurrent creator winning the check-then-create race surfaces as 42P04
// duplicate_database — tolerated as success (the database exists either way;
// v1.26.1, observed with parallel first-run seeds). Tests swap in a no-op
// recorder.
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
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P04" {
			return nil // lost the race — the winner created it
		}
	}
	return err
}

// deriveDemoDSN — the isolated-DB derivation: the base DSN's database name
// plus the fixed `_demo` suffix (host, credentials, and query params are kept
// verbatim). An unparseable DSN, a keyword-form DSN (pgx accepts
// "host=... dbname=..." — url.Parse mangles it into a garbage path), or a
// missing database name is an error — the command layer turns it into exit 3
// with FLINT_DEMO_DSN guidance (v1.26.1: keyword forms must use the explicit
// override instead of a silently corrupted derivation).
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

// resolveDemoDSN — FLINT_DEMO_DSN wins verbatim; otherwise derived from the
// running DSN. Returns the demo DSN and its database name (for CREATE DATABASE).
func resolveDemoDSN() (string, string, error) {
	if d := os.Getenv(demoDSNEnv); d != "" {
		u, err := url.Parse(d)
		if err != nil {
			return "", "", fmt.Errorf("seed: %s parse failed: %w", demoDSNEnv, err)
		}
		name := strings.TrimPrefix(u.Path, "/")
		if name == "" {
			return "", "", fmt.Errorf("seed: %s has no database name", demoDSNEnv)
		}
		return d, name, nil
	}
	demo, err := deriveDemoDSN(dsnFromEnv())
	if err != nil {
		return "", "", err
	}
	return demo, dsnDBName(demo), nil
}

// dsnDBName — the database-name segment of a URL DSN.
func dsnDBName(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}

// seedSummary — the one-line JSON result shape.
type seedSummary struct {
	Status   string            `json:"status"`
	Database string            `json:"database"`
	Results  map[string]string `json:"results,omitempty"`
	Targets  int               `json:"targets,omitempty"`
	Deleted  int               `json:"deleted,omitempty"`
	Next     string            `json:"next,omitempty"`
}

// runSeed — command entry point (store-free at dispatch: it opens its own
// store on the derived demo DSN; the passed-in store is never used).
func runSeed(ctx context.Context, st Store, args []string, stdin io.Reader, stdout io.Writer) int {
	res, perr := wire.ParseArgs(args, wire.ArgSpec{
		Flags: []wire.FlagSpec{
			{Name: "--demo"},
			{Name: "--reset"},
			{Name: "--yes"},
		},
		Usage: "usage: flint seed --demo [--reset] [--yes]",
	})
	if perr != nil {
		fmt.Fprintln(stdout, errLine(perr))
		return 3
	}
	if !res.Given("--demo") {
		fmt.Fprintln(stdout, errLine(wire.UsageErrorf("seed: --demo required — usage: flint seed --demo [--reset] [--yes]")))
		return 3
	}
	demoDSN, dbName, err := resolveDemoDSN()
	if err != nil {
		fmt.Fprintln(stdout, errLine(wire.UsageErrorf("%v", err)))
		return 3
	}
	// connect the demo database; on failure (typically: not created yet),
	// create it from the base DSN once and retry — only when the demo DSN was
	// DERIVED from the base (same server). With an explicit FLINT_DEMO_DSN the
	// database is the operator's to create: creating it on the base server
	// would at best be a cross-server no-op (v1.26.1).
	overridden := os.Getenv(demoDSNEnv) != ""
	demoSt, err := newStore(ctx, demoDSN)
	if err != nil && !overridden {
		if ensureErr := seedEnsureDBFn(ctx, dsnFromEnv(), dbName); ensureErr == nil {
			demoSt, err = newStore(ctx, demoDSN)
		}
	}
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return store.MapExit(err)
	}
	defer demoSt.Close()
	if res.Given("--reset") {
		return runSeedReset(ctx, demoSt, res.Given("--yes"), dbName, stdout)
	}
	return runSeedDemo(ctx, demoSt, dbName, stdout)
}

// runSeedDemo — seeds the embedded dataset through the single RunWrite path,
// kind-grouped in embed order, with the demo/ prefix applied to every topic
// and project name (the --reset identifier). Idempotent: a re-run writes
// duplicates through the normal dedup and still exits 0.
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
			fmt.Fprintln(stdout, errLine(fmt.Errorf("seed: embedded seed.jsonl parse failure: %w", err)))
			return 1
		}
		kind, _ := p["kind"].(string)
		switch kind {
		case "knowledge":
			p["topic"] = demoTopicPrefix + fmt.Sprint(p["topic"])
		case "project":
			p["name"] = demoTopicPrefix + fmt.Sprint(p["name"])
		default:
			fmt.Fprintln(stdout, errLine(fmt.Errorf("seed: embedded kind %q not seedable", kind)))
			return 1
		}
		b, err := json.Marshal(p)
		if err != nil {
			fmt.Fprintln(stdout, errLine(err))
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
			// the write layer already printed its one-line JSON — surface it
			// verbatim and keep its exit code (validation 3, server 2, ...).
			fmt.Fprintln(stdout, buf.String())
			return code
		}
	}
	// the ownership marker — --reset refuses to run without it.
	if err := setMetaKV(ctx, demoSt, demoOwnerKey, demoOwnerValue); err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	b, err := json.Marshal(seedSummary{Status: "ok", Database: dbName, Results: results})
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

// writeStatus — the status field of a write output line ("" when unparseable;
// the raw line was already surfaced by the caller on failure).
func writeStatus(line string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &m); err != nil {
		return ""
	}
	s, _ := m["status"].(string)
	return s
}

// runSeedReset — deletes ONLY the fixed demo identifiers: knowledge topics
// and project names carrying the demo/ prefix, in a database whose
// demo_owner marker matches. A missing or foreign marker is exit 3 (this
// database is not ours to wipe); --yes is required for execution.
func runSeedReset(ctx context.Context, demoSt Store, yes bool, dbName string, stdout io.Writer) int {
	owner, ok := getMetaKV(ctx, demoSt, demoOwnerKey)
	if !ok || owner != demoOwnerValue {
		fmt.Fprintln(stdout, errLine(wire.UsageErrorf(
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
			fmt.Fprintln(stdout, errLine(err))
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
			fmt.Fprintln(stdout, errLine(err))
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
			fmt.Fprintln(stdout, errLine(err))
			return 1
		}
		if q.like != demoOwnerKey {
			deleted = n
		}
	}
	b, err := json.Marshal(seedSummary{Status: "ok", Database: dbName, Deleted: deleted})
	if err != nil {
		fmt.Fprintln(stdout, errLine(err))
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

func init() {
	Register("seed", runSeed)
}
