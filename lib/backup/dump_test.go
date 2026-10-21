package backup

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"flint/lib/db"
)

func testDSN() string {
	if dsn := os.Getenv("FLINT_DSN"); dsn != "" {
		return dsn
	}
	return "postgresql://flint:flint-dev@localhost:5433/flint"
}

// Review Focus(file): 작은따옴표·줄바꿈·백슬래시 이스케이프.
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

// 세미콜론이 문자열·주석·달러인용 안에 있으면 문장 분리하지 않는다.
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

// Review Focus(state): 라운드트립 row 수 일치 + 특수문자 값 보존.
func TestDumpRestoreRoundtrip(t *testing.T) {
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
	defer pool.Exec(ctx, `DROP TABLE IF EXISTS backup_probe_child`)
	defer pool.Exec(ctx, `DROP TABLE IF EXISTS backup_probe`)

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

	// 완전히 날린 뒤 복구 — DDL 재생성 포함.
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
	// serial 복구 확인: id 미지정 삽입이 충돌 없이 성공해야 한다.
	if _, err := pool.Exec(ctx, `INSERT INTO backup_probe (val) VALUES ('post-restore')`); err != nil {
		t.Fatalf("serial not restored, insert failed: %v", err)
	}
}
