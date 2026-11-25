package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/knowledge"
	"github.com/synuns/flint/lib/render"
	"github.com/synuns/flint/lib/wire"
)

const (
	smokeDemoDSNSuffix = "_demo"
	smokeDemoDSNEnv    = "FLINT_DEMO_DSN"
	smokeTopicPrefix   = "demo/smoke-"
	smokeOwnerKey      = "smoke_owner"
	smokeOwnerValue    = "flint-doctor-smoke"
	smokeCheckName     = "smoke"
	smokeSessionLabel  = "smoke-doctor"
)

type SmokeStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

var smokeHookDetectFn = checkHookFile

var smokeResolveDemoDSNFn = func() (string, error) {
	if d := os.Getenv(smokeDemoDSNEnv); d != "" {
		return d, nil
	}
	u, err := url.Parse(wire.ResolveDSN())
	if err != nil || u.Scheme == "" || strings.TrimPrefix(u.Path, "/") == "" {
		return "", fmt.Errorf("smoke: cannot derive a demo database from the DSN — set %s", smokeDemoDSNEnv)
	}
	u.Path = u.Path + smokeDemoDSNSuffix
	return u.String(), nil
}

var smokeDialFn = func(ctx context.Context) bool {
	cfg, err := pgx.ParseConfig(wire.ResolveDSN())
	if err != nil {
		return false
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port)))
	if err != nil {
		return false
	}
	return conn.Close() == nil
}

var smokeEnsureDBFn = func(ctx context.Context, adminDSN, dbName string) error {
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
	return err
}

var smokeNewStoreFn = func(ctx context.Context, dsn string) (wire.Store, error) {
	repo, err := db.NewRepo(ctx, dsn)
	if err != nil {
		return nil, err
	}
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		repo.Close()
		return nil, err
	}
	st := &smokeStore{Repo: repo, pool: pool}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

type smokeStore struct {
	*db.Repo
	pool *pgxpool.Pool
}

func (s smokeStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s smokeStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

func (s smokeStore) Close() {
	s.Repo.Close()
	s.pool.Close()
}

func dsnPathName(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}

func smokeSameEngineHost(a, b string) bool {
	ca, err := pgx.ParseConfig(a)
	if err != nil {
		return false
	}
	cb, err := pgx.ParseConfig(b)
	if err != nil {
		return false
	}
	return ca.Host == cb.Host && ca.Port == cb.Port
}

func openSmokeStore(ctx context.Context, dsn string) (wire.Store, error) {
	return smokeNewStoreFn(ctx, dsn)
}

func smokeCountRows(ctx context.Context, st wire.Store, dbName string) int {
	rows, err := wire.QueryOnce(ctx, st, `SELECT count(*) FROM knowledge`, nil)
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return -1
	}
	if v, ok := rows[0][0].(int64); ok {
		return int(v)
	}
	return -1
}

func smokeCountOwnRows(ctx context.Context, st wire.Store) int {
	rows, err := wire.QueryOnce(ctx, st,
		`SELECT count(*) FROM knowledge WHERE topic LIKE $1`, []any{smokeTopicPrefix + "%"})
	if err != nil || len(rows) == 0 || len(rows[0]) == 0 {
		return -1
	}
	if v, ok := rows[0][0].(int64); ok {
		return int(v)
	}
	return -1
}

func smokeRecs() []map[string]any {
	return []map[string]any{
		{"kind": "knowledge", "topic": smokeTopicPrefix + "onboarding-a", "summary": "smoke 온보딩 점검 행 a", "tags": ":smoke:", "confidence": 0.9, "content_hash": "smoke-rec-a"},
		{"kind": "knowledge", "topic": smokeTopicPrefix + "onboarding-b", "summary": "smoke 온보딩 점검 행 b", "tags": ":smoke:", "confidence": 0.9, "content_hash": "smoke-rec-b"},
		{"kind": "knowledge", "topic": smokeTopicPrefix + "onboarding-c", "summary": "smoke 온보딩 점검 행 c", "tags": ":smoke:", "confidence": 0.9, "content_hash": "smoke-rec-c"},
	}
}

func smokeCleanup(ctx context.Context, st wire.Store) (int, error) {
	rs, ok := st.(*smokeStore)
	if !ok {
		return 0, errors.New("smoke reset: store lacks the demo reset capability")
	}
	rows, err := wire.QueryOnce(ctx, st, `SELECT id FROM knowledge WHERE topic LIKE $1`, []any{smokeTopicPrefix + "%"})
	if err != nil {
		return 0, err
	}
	var ids []int
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		if id, ok := render.CellToInt(r[0]); ok {
			ids = append(ids, id)
		}
	}
	before := len(ids)
	if before > 0 {
		if _, err := rs.Invalidate(ctx, ids, "doctor-smoke-cleanup"); err != nil {
			return 0, err
		}
		if _, err := rs.PruneInvalidated(ctx, time.Now().UTC().Add(time.Minute)); err != nil {
			return 0, err
		}
	}
	if _, err := rs.MetaDelete(ctx, []string{smokeOwnerKey}); err != nil {
		return 0, err
	}
	return before, nil
}

func smokeWireHotFn() {
	if knowledge.SessionLabelFn == nil {
		knowledge.SessionLabelFn = func(args []string) string {
			for i := 0; i < len(args); i++ {
				if args[i] == "--session" && i+1 < len(args) {
					return args[i+1]
				}
			}
			return ""
		}
	}
	if knowledge.ExposureProfileFn == nil {
		knowledge.ExposureProfileFn = func(func(string) (string, error)) string { return "normal" }
	}
	if knowledge.RecordHotLenFn == nil {
		knowledge.RecordHotLenFn = func(int) {}
	}
	if knowledge.LoadHotStatsFromMetaFn == nil {
		knowledge.LoadHotStatsFromMetaFn = func(context.Context, wire.Store) {}
	}
	if knowledge.PersistHotStatsToMetaFn == nil {
		knowledge.PersistHotStatsToMetaFn = func(context.Context, wire.Store) {}
	}
	if knowledge.PersistScanStatsFn == nil {
		knowledge.PersistScanStatsFn = func(context.Context, wire.Store) {}
	}
}

func smokeSequence(ctx context.Context) ([]SmokeStep, int) {
	var steps []SmokeStep
	fail := func(step, detail string) ([]SmokeStep, int) {
		steps = append(steps, SmokeStep{Step: step, Status: "fail", Detail: detail})
		return steps, 1
	}
	ok := func(step, detail string) {
		steps = append(steps, SmokeStep{Step: step, Status: "ok", Detail: detail})
	}

	if !smokeDialFn(ctx) {
		return fail("connect", "engine endpoint unreachable — run flint setup")
	}
	ok("connect", "engine endpoint reachable")

	hc := smokeHookDetectFn()
	status := hc.Status
	if status == "pass" {
		status = "ok"
	}
	if status == "fail" {
		return fail("hook", hc.Detail)
	}
	steps = append(steps, SmokeStep{Step: "hook", Status: status, Detail: hc.Detail})

	demoDSN, err := smokeResolveDemoDSNFn()
	if err != nil {
		return fail("demo-db", err.Error())
	}
	prodDB := dsnPathName(wire.ResolveDSN())
	demoDB := dsnPathName(demoDSN)
	if demoDB == prodDB {
		return fail("demo-db", "demo DSN equals the production database — refusing to touch the operational DB (set "+smokeDemoDSNEnv+")")
	}
	if smokeSameEngineHost(demoDSN, wire.ResolveDSN()) && !strings.HasSuffix(demoDB, smokeDemoDSNSuffix) {
		return fail("demo-db", "demo DSN targets the production engine with a non-"+smokeDemoDSNSuffix+" database — refusing to migrate or write next to the operational DB (set "+smokeDemoDSNEnv+")")
	}
	if err := smokeEnsureDBFn(ctx, wire.ResolveDSN(), dsnPathName(demoDSN)); err != nil {
		return fail("demo-db", "ensure demo database: "+err.Error())
	}
	st, err := smokeNewStoreFn(ctx, demoDSN)
	if err != nil {
		return fail("demo-db", "connect demo database: "+err.Error())
	}
	defer st.Close()
	ok("demo-db", dsnPathName(demoDSN)+" (isolated from "+prodDB+")")

	if _, err := smokeCleanup(ctx, st); err != nil {
		return fail("write", "pre-clean: "+err.Error())
	}
	ids, err := st.InsertBatch(ctx, smokeRecs())
	if err != nil || len(ids) != 3 {
		return fail("write", fmt.Sprintf("synthetic write: %d rows (%v)", len(ids), err))
	}
	if err := wire.SetMetaKV(ctx, st, smokeOwnerKey, smokeOwnerValue); err != nil {
		return fail("write", "smoke owner marker: "+err.Error())
	}
	ok("write", "3 synthetic rows in the isolated demo database")

	var rbuf bytes.Buffer
	if code := knowledge.RunRead(ctx, st, []string{"smoke"}, bytes.NewReader(nil), &rbuf); code != 0 || !strings.Contains(rbuf.String(), smokeTopicPrefix) {
		return fail("read", fmt.Sprintf("read did not reach the smoke rows (exit %d)", code))
	}
	ok("read", "search reached the demo rows")

	var hbuf bytes.Buffer
	smokeWireHotFn()
	if code := knowledge.RunHot(ctx, st, []string{"--session", smokeSessionLabel}, bytes.NewReader(nil), &hbuf); code != 0 || !strings.Contains(hbuf.String(), "[flint]") {
		return fail("inject", fmt.Sprintf("hot injection produced no [flint] boundary output (exit %d)", code))
	}
	ok("inject", "[flint] boundary output confirmed")

	before, err := smokeCleanup(ctx, st)
	if err != nil {
		return fail("cleanup", err.Error())
	}
	after := smokeCountOwnRows(ctx, st)
	if after != 0 {
		return fail("cleanup", fmt.Sprintf("%d smoke rows remain after reset", after))
	}
	ok("cleanup", fmt.Sprintf("reset removed %d smoke rows — demo database left to its own content", before))
	return steps, 0
}

func RunSmokeCheck(ctx context.Context, stdout io.Writer) int {
	steps, code := smokeSequence(ctx)
	enc := json.NewEncoder(stdout)
	for _, s := range steps {
		if err := enc.Encode(s); err != nil {
			fmt.Fprintln(stdout, wire.ErrLine(err))
			return 1
		}
	}
	return code
}

func runSmokeDoctorCheck(ctx context.Context) DoctorCheck {
	c := DoctorCheck{Name: smokeCheckName}
	var buf bytes.Buffer
	code := RunSmokeCheck(ctx, &buf)
	if code == 0 {
		c.Status = "ok"
		c.Detail = "smoke ok — connect·hook·demo write/read/inject·cleanup all green (isolated _demo database)"
		return c
	}
	bad := ""
	for _, ln := range strings.Split(buf.String(), "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "{") {
			continue
		}
		var s SmokeStep
		if err := json.Unmarshal([]byte(ln), &s); err != nil {
			continue
		}
		if s.Status == "fail" {
			bad = s.Step + ": " + s.Detail
			break
		}
	}
	c.Status = "fail"
	c.Detail = "smoke failed — " + bad
	c.Fix = "fix: resolve the failed step above, then re-run flint doctor --check " + smokeCheckName
	return c
}

func SmokeChecks() []CheckSpec {
	return []CheckSpec{{
		Name: smokeCheckName, Chain: "tail", OnStoreNil: "",
		Fn: func(ctx context.Context, st wire.Store) DoctorCheck { return runSmokeDoctorCheck(ctx) },
	}}
}

func smokeAddressable(name string) bool {
	for _, s := range SmokeChecks() {
		if s.Name == name {
			return true
		}
	}
	return false
}
