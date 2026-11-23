package backup

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/db"
)

var probeSeqNameRe = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

func dropProbeSequences(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx,
		`SELECT sequencename FROM pg_sequences WHERE sequencename LIKE 'backup_probe%'`)
	if err != nil {
		return fmt.Errorf("list sequences: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var seq string
		if err := rows.Scan(&seq); err != nil {
			return fmt.Errorf("scan sequence: %w", err)
		}
		if !probeSeqNameRe.MatchString(seq) {
			return fmt.Errorf("sequence name charset violation: %q", seq)
		}
		if _, err := pool.Exec(ctx, "DROP SEQUENCE "+seq); err != nil {
			return fmt.Errorf("drop sequence %s: %w", seq, err)
		}
	}
	return rows.Err()
}

func testDSN() string {
	return testengine.ResolveDSN()
}

func TestSQLLiteral(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, "NULL"},
		{int(42), "42"},
		{3.5, "3.5"},
		{true, "TRUE"},
		{"plain", "'plain'"},
		{"it's", "'it''s'"},
		{"a\nb", "'a\nb'"},
		{"back\\slash", "'back\\slash'"},
	}
	for _, c := range cases {
		if got := sqlLit(c.in); got != c.want {
			t.Errorf("sqlLit(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSplitStatements(t *testing.T) {
	script := "INSERT INTO t VALUES ('a;b');\n-- comment; here\nUPDATE t SET v='x'';y';\n/* block; */ DELETE FROM t;\nSELECT $body$; inside$body$;"
	got := splitStatements(script)
	want := []string{
		"INSERT INTO t VALUES ('a;b')",
		"-- comment; here\nUPDATE t SET v='x'';y'",
		"/* block; */ DELETE FROM t",
		"SELECT $body$; inside$body$",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d statements %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if strings.TrimSpace(got[i]) != strings.TrimSpace(want[i]) {
			t.Errorf("stmt[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDumpRestoreRoundtrip(t *testing.T) {

	unlock := drillLock(t)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dsn := testDSN()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect doltgres: %v", err)
	}
	defer pool.Close()

	setup := []string{
		`DROP TABLE IF EXISTS backup_probe_child`,
		`DROP TABLE IF EXISTS backup_probe`,
		`CREATE TABLE backup_probe (
			id serial PRIMARY KEY,
			val text NOT NULL,
			num double precision,
			ts timestamptz)`,
		`CREATE TABLE backup_probe_child (
			id int PRIMARY KEY,
			pid int REFERENCES backup_probe(id),
			note text UNIQUE)`,
	}
	for _, q := range setup {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("setup %q: %v", q, err)
		}
	}

	defer func() {
		for _, q := range []string{
			`DROP TABLE IF EXISTS backup_probe_child`,
			`DROP TABLE IF EXISTS backup_probe`,
		} {
			if _, err := pool.Exec(ctx, q); err != nil {
				t.Errorf("cleanup %q: %v", q, err)
			}
		}
		if err := dropProbeSequences(ctx, pool); err != nil {
			t.Errorf("backup_probe 시퀀스 회수: %v", err)
		}
		var left int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM dolt_status WHERE table_name LIKE '%backup_probe%'`).Scan(&left); err != nil {
			t.Errorf("dolt_status 잔여 조회: %v", err)
			return
		}
		if left != 0 {
			t.Errorf("backup_probe* dolt_status 잔여 %d건, want 0(테이블·시퀀스 미회수)", left)
		}
	}()

	tricky := "it's a \"quoted\"\nline2 \\ end"
	probe := []string{
		`INSERT INTO backup_probe (id, val, num, ts) VALUES (1, 'first', 1.5, '2026-10-21 10:00:00+00')`,
		"INSERT INTO backup_probe (id, val, num, ts) VALUES (2, " + sqlLit(tricky) + ", 2.5, now())",
		`INSERT INTO backup_probe (id, val, num, ts) VALUES (3, 'third', NULL, NULL)`,
		`INSERT INTO backup_probe_child (id, pid, note) VALUES (10, 2, 'child;note')`,
	}
	for _, q := range probe {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}

	var buf bytes.Buffer
	if err := Dump(ctx, dsn, &buf); err != nil {
		t.Fatalf("dump: %v", err)
	}
	dump := buf.String()
	if !strings.Contains(dump, "BEGIN;") || !strings.Contains(dump, "DELETE FROM") || !strings.Contains(dump, "INSERT INTO") {
		t.Fatalf("dump missing BEGIN/DELETE/INSERT structure:\n%s", dump)
	}
	if !strings.Contains(dump, "'it''s") {
		t.Fatalf("dump does not escape single quote:\n%s", dump)
	}

	for _, q := range setup[:2] {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("wipe %q: %v", q, err)
		}
	}
	if err := Restore(ctx, dsn, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("restore: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM backup_probe`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Fatalf("roundtrip row count = %d, want 3", count)
	}
	var gotVal string
	if err := pool.QueryRow(ctx, `SELECT val FROM backup_probe WHERE id = 2`).Scan(&gotVal); err != nil {
		t.Fatalf("read tricky: %v", err)
	}
	if gotVal != tricky {
		t.Fatalf("tricky value not preserved: %q != %q", gotVal, tricky)
	}
	var childCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM backup_probe_child`).Scan(&childCount); err != nil {
		t.Fatalf("child count: %v", err)
	}
	if childCount != 1 {
		t.Fatalf("child rows = %d, want 1", childCount)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO backup_probe (val) VALUES ('post-restore')`); err != nil {
		t.Fatalf("serial not restored, insert failed: %v", err)
	}
}
