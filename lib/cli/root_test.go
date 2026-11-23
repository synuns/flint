package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/db"
)

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

func TestFreshDBFirstCommandExit0RealSlow(t *testing.T) {
	_, real := seamRealDSN()
	if !real {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	freshDSN := testengine.Scratch(t)

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
