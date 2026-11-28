package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/cli"
	"github.com/synuns/flint/lib/db"
)

const v1420Session = "e2e-v1420"

type v1420SeedSpec struct {
	n          int
	zero       bool
	fallback   bool
	zeroReason string
	ageHours   int
}

func v1420SeedReadEvents(t *testing.T, ctx context.Context, pool *pgxpool.Pool, specs []v1420SeedSpec) {
	t.Helper()
	for _, s := range specs {
		hits := 1
		if s.zero {
			hits = 0
		}
		ts := time.Now().Add(-time.Duration(s.ageHours) * time.Hour)
		for i := 0; i < s.n; i++ {
			if _, err := pool.Exec(ctx,
				`INSERT INTO read_events (ts, hits, zero, near, fallback, session, zero_reason)
				 VALUES ($1, $2, $3, false, $4, $5, $6)`,
				ts, hits, s.zero, s.fallback, v1420Session, s.zeroReason); err != nil {
				t.Fatalf("read_events 시드 삽입: %v", err)
			}
		}
	}
}

func v1420RunReport(t *testing.T) map[string]any {
	t.Helper()
	var out bytes.Buffer
	code := cli.Run([]string{"report", "--days", "14"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("report exit=%d, want 0; out=%s", code, out.String())
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
		t.Fatalf("report 출력이 JSON 아님: %q: %v", out.String(), err)
	}
	return m
}

func v1420EffectInt(t *testing.T, rep map[string]any, key string) int {
	t.Helper()
	effect, ok := rep["effect"].(map[string]any)
	if !ok {
		t.Fatalf("report에 effect 섹션 없음: %v", rep)
	}
	v, ok := effect[key].(float64)
	if !ok {
		t.Fatalf("effect.%s 누락 또는 숫자 아님: %v", key, effect[key])
	}
	return int(v)
}

func v1420TopInt(t *testing.T, rep map[string]any, key string) int {
	t.Helper()
	v, ok := rep[key].(float64)
	if !ok {
		t.Fatalf("%s 누락 또는 숫자 아님: %v", key, rep[key])
	}
	return int(v)
}

func TestReadEventsNThresholdPaths(t *testing.T) {
	dsn := testengine.Scratch(t)
	ctx := context.Background()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("스크래치 DB 접속: %v", err)
	}
	t.Cleanup(pool.Close)

	repo, err := db.NewRepo(ctx, dsn)
	if err != nil {
		t.Fatalf("스크래치 스토어: %v", err)
	}
	if err := repo.Migrate(ctx); err != nil {
		t.Fatalf("스크래치 마이그레이션: %v", err)
	}
	for _, g := range db.SchemaGates() {
		if err := g.Ensure(ctx, repo); err != nil {
			t.Fatalf("스키마 게이트 v%d: %v", g.Version, err)
		}
	}
	repo.Close()

	t.Setenv("FLINT_DSN", dsn)
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())

	v1420SeedReadEvents(t, ctx, pool, []v1420SeedSpec{
		{n: 130},
		{n: 24, ageHours: 72},
		{n: 44, zero: true, zeroReason: "unowned"},
		{n: 22, fallback: true},
		{n: 18, ageHours: 16 * 24},
		{n: 12, zero: true, zeroReason: "unowned", ageHours: 16 * 24},
	})

	rep := v1420RunReport(t)
	if n := v1420EffectInt(t, rep, "read_events_n"); n != 220 {
		t.Errorf("read_events_n = %d, want 220(≥200 경로·창 내 합성 이벤트 전부)", n)
	}
	if p := v1420EffectInt(t, rep, "hit_pct"); p != 80 {
		t.Errorf("effect.hit_pct = %d, want 80(220 중 zero 44)", p)
	}
	if p := v1420EffectInt(t, rep, "fallback_pct"); p != 10 {
		t.Errorf("effect.fallback_pct = %d, want 10(220 중 fallback 22)", p)
	}
	if p := v1420EffectInt(t, rep, "zero_unowned_pct"); p != 20 {
		t.Errorf("effect.zero_unowned_pct = %d, want 20(zero 44 전부 unowned)", p)
	}
	if p := v1420EffectInt(t, rep, "near_pct"); p != 0 {
		t.Errorf("effect.near_pct = %d, want 0(near 시드 없음)", p)
	}
	if z := v1420TopInt(t, rep, "zero_pct"); z != 20 {
		t.Errorf("zero_pct = %d, want 20", z)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM read_events`); err != nil {
		t.Fatalf("read_events 재설정: %v", err)
	}

	v1420SeedReadEvents(t, ctx, pool, []v1420SeedSpec{
		{n: 80},
		{n: 20, ageHours: 48},
		{n: 45, zero: true, zeroReason: "owned_miss"},
		{n: 5, fallback: true},
		{n: 12, ageHours: 16 * 24},
	})

	rep = v1420RunReport(t)
	if n := v1420EffectInt(t, rep, "read_events_n"); n != 150 {
		t.Errorf("read_events_n = %d, want 150(<200 경로·창 외 30건 제외)", n)
	}
	if p := v1420EffectInt(t, rep, "hit_pct"); p != 70 {
		t.Errorf("effect.hit_pct = %d, want 70(150 중 zero 45)", p)
	}
	if p := v1420EffectInt(t, rep, "fallback_pct"); p != 3 {
		t.Errorf("effect.fallback_pct = %d, want 3(150 중 fallback 5)", p)
	}
	if p := v1420EffectInt(t, rep, "zero_owned_miss_pct"); p != 30 {
		t.Errorf("effect.zero_owned_miss_pct = %d, want 30(zero 45 전부 owned_miss)", p)
	}
	if z := v1420TopInt(t, rep, "zero_pct"); z != 30 {
		t.Errorf("zero_pct = %d, want 30", z)
	}
}
