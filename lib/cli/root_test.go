// root_test.go — v1.7.2 T4: schemaVersionFn probe 42P01 heal. probe가
// 42P01(테이블 부재)로 실패하면 st 풀에 SELECT 1을 1회 실행해 세션
// 카탈로그를 재동기(R38-B probeheal)하고 probe를 재시도한다 — 여전히
// 실패하면(진짜 신규 DB) 기존 순차 승격 경로로 폴백.
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

// probeHealFake — 호출 순서 기록형 fake: 첫 probe 42P01 → SELECT 1 성공 →
// 재시도 결과는 retryRows로 응답.
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
// heal 1회 → probe 재시도 성공 시퀀스(쿼리 순서 단언 포함).
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

// TestSchemaVersionProbeHealFallsBackOnFreshDB — heal 후에도 42P01이면
// (진짜 신규 DB) 3쿼리만 소비하고 (0,false) 폴백 — 순차 승격 경로 위임.
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

// TestSchemaVersionProbeNoHealOnOtherErrors — 42P01 외 오류(서버 다운 등)는
// heal 없이 즉시 폴백 — 불필요한 왕복 금지.
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
// 신규 DB 생성 직후 첫 커맨드(status)가 exit 0 — probe 42P01 → heal → 순차
// 승격 경로 전체를 실DB로 실증(L1 회귀).
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
