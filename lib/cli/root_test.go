// root_test.go — v1.7.2 T4: the schemaVersionFn probe 42P01 heal. When the probe
// fails with 42P01 (missing table), run SELECT 1 once on the st pool to resynchronize the session
// catalog (R38-B probeheal) and retry the probe — if it still fails
// (a genuinely fresh DB), fall back to the existing sequential promotion path.
package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/synuns/flint/lib/db"
)

// probeHealFake — a call-order-recording fake: first probe 42P01 -> SELECT 1 succeeds ->
// the retry answers from retryRows.
func probeHealFake(t *testing.T, retryRows [][]any, retryErr error) (*fakeStore, *[]string) {
	t.Helper()
	seq := &[]string{}
	f := &fakeStore{}
	f.queryFn = func(sql string, args []any) ([][]any, error) {
		*seq = append(*seq, sql)
		switch len(*seq) {
		case 1:
			return nil, &pgconn.PgError{Code: "42P01", Message: `relation "meta" does not exist`}
		case 2:
			return [][]any{{int32(1)}}, nil
		default:
			return retryRows, retryErr
		}
	}
	return f, seq
}

// TestSchemaVersionProbeHealsUndefinedTable — Review Focus: 42P01 → SELECT 1
// a heal-once -> probe-retry-success sequence (including query-order assertions).
func TestSchemaVersionProbeHealsUndefinedTable(t *testing.T) {
	f, seq := probeHealFake(t, [][]any{{fmt.Sprint(db.LatestSchemaVersion)}}, nil)
	v, ok := schemaVersionFn(context.Background(), f)
	if !ok || v != db.LatestSchemaVersion {
		t.Fatalf("(%d,%v), want (%d,true) — heal 후 재시도 성공", v, ok, db.LatestSchemaVersion)
	}
	if len(*seq) != 3 {
		t.Fatalf("쿼리 %d건, want 3(probe→SELECT 1→재시도): %v", len(*seq), *seq)
	}
	if !strings.Contains((*seq)[0], "schema_version") || (*seq)[1] != "SELECT 1" ||
		!strings.Contains((*seq)[2], "schema_version") {
		t.Fatalf("쿼리 순서 위반: %v", *seq)
	}
}

// TestSchemaVersionProbeHealFallsBackOnFreshDB — if it is still 42P01 after the heal
// (a genuinely fresh DB), only 3 queries are consumed and it falls back to (0,false) — delegating to the sequential promotion path.
func TestSchemaVersionProbeHealFallsBackOnFreshDB(t *testing.T) {
	f, seq := probeHealFake(t, nil, &pgconn.PgError{Code: "42P01", Message: `relation "meta" does not exist`})
	v, ok := schemaVersionFn(context.Background(), f)
	if ok || v != 0 {
		t.Fatalf("(%d,%v), want (0,false) — 신규 DB 폴백", v, ok)
	}
	if len(*seq) != 3 {
		t.Fatalf("쿼리 %d건, want 3(재시도 무한루프 금지): %v", len(*seq), *seq)
	}
}

// TestSchemaVersionProbeNoHealOnOtherErrors — errors other than 42P01 (server down etc.)
// fall back immediately without healing — no unnecessary round trips.
func TestSchemaVersionProbeNoHealOnOtherErrors(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, errors.New("connection refused")
	}}
	v, ok := schemaVersionFn(context.Background(), f)
	if ok || v != 0 {
		t.Fatalf("(%d,%v), want (0,false)", v, ok)
	}
	if n := len(f.queries); n != 1 {
		t.Fatalf("쿼리 %d건, want 1(42P01 아닌 오류는 heal 없음)", n)
	}
}

// TestFreshDBFirstCommandExit0RealSlow — slow(FLINT_TEST_DSN + SUPERLITE_REAL=1):
// the first command (status) right after creating a fresh DB exits 0 — the probe 42P01 -> heal ->
// sequential promotion path demonstrated end-to-end on a real DB (L1 regression).
func TestFreshDBFirstCommandExit0RealSlow(t *testing.T) {
	dsn, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	base, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	defer base.Close()

	name := fmt.Sprintf("flint_t4_probe_%d", time.Now().UnixNano()%1e9)
	if _, err := base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}
	t.Cleanup(func() {
		_, _ = base.Exec(ctx, "DROP DATABASE IF EXISTS "+name)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN 파싱: %v", err)
	}
	u.Path = "/" + name
	freshDSN := u.String()

	prev := newStore
	newStore = func(ctx context.Context, dsn string) (Store, error) {
		return newRealStore(ctx, freshDSN)
	}
	t.Cleanup(func() { newStore = prev })

	var out bytes.Buffer
	if code := Run([]string{"status"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("fresh DB 첫 커맨드 exit=%d, want 0; out=%s", code, out.String())
	}
}
