// autotune_shadow_real_test.go — the seam-check conformance run: the
// same shadow-round contract as autotune_shadow_test.go, but with the
// REAL lib/ops registry (ops.AutoRules + ops.AutoTrigger + the
// ops.AutoVerdictQuery interpolation) registered through the
// db.RegisterShadowCircuit seam. External package (db_test) because
// lib/db can never import lib/ops (ops imports db — compile-verified);
// the in-package default fixture is the swappable fake, this file is
// the real implementation under SUPERLITE_REAL=1. The production
// registration lives in lib/ops/shadow_register.go (init) — this file
// registers explicitly so the run survives in-package tests' seam
// restores.
package db_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/store"
)

// realShadowRules — the ops registry projected onto db.ShadowRule (the
// adapter the production composition root installs).
func realShadowRules() []db.ShadowRule {
	out := make([]db.ShadowRule, 0, len(ops.AutoRules()))
	for _, r := range ops.AutoRules() {
		out = append(out, db.ShadowRule{
			Signal:      r.Signal,
			Query:       ops.AutoVerdictQuery(r),
			Key:         r.Key,
			Direction:   r.Direction,
			SafeRange:   r.SafeRange,
			Auto:        r.Auto,
			SuccessPair: r.SuccessPair,
			ObsWindowD:  r.ObsWindowD,
		})
	}
	return out
}

// realShadowVerdict — the ops.AutoTrigger adapter (the injected verdict
// single source — db never re-implements the trigger).
func realShadowVerdict(rule db.ShadowRule, obs db.ShadowObs, th store.Thresholds) db.ShadowVerdict {
	v := ops.AutoTrigger(ops.AutoRule{Signal: rule.Signal, Key: rule.Key}, ops.AutoTriggerObs{
		ReadEventsN: obs.ReadEventsN, ZeroN: obs.ZeroN, ReclassN: obs.ReclassN,
	}, th)
	return db.ShadowVerdict{Fired: v.Fired, SampleOk: v.SampleOk, Ratio: v.Ratio, Detail: v.Detail}
}

// dsnForDB — the DSN with the database path swapped.
func dsnForDB(u *url.URL, name string) string {
	c := *u
	c.Path = "/" + name
	return c.String()
}

// realScratch — a dedicated scratch DB via the exported surface only
// (the in-package newScratchTestRepo is unreachable from db_test):
// admin connect on the postgres catalog, CREATE DATABASE, NewRepo +
// Migrate. Returns the repo and the scratch DSN. LIFO cleanup: repo
// close → DROP DATABASE → admin close.
func realScratch(t *testing.T) (*db.Repo, string) {
	t.Helper()
	dsn := os.Getenv("FLINT_TEST_DSN")
	if dsn == "" {
		t.Skip("no test DSN: set FLINT_TEST_DSN")
	}
	ctx := context.Background()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN parse: %v", err)
	}
	name := fmt.Sprintf("flint_shreal_%d", time.Now().UnixNano())
	// The in-package adminConnectForScratch convention: the postgres
	// catalog first, the original path as fallback (servers without a
	// postgres database — this Doltgres).
	admin, err := db.Connect(ctx, dsnForDB(u, "postgres"))
	if err != nil {
		if admin, err = db.Connect(ctx, dsn); err != nil {
			t.Skipf("admin connect unavailable: %v", err)
		}
	}
	t.Cleanup(admin.Close)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name)
	})
	r, err := db.NewRepo(ctx, dsnForDB(u, name))
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	t.Cleanup(r.Close)
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return r, dsnForDB(u, name)
}

// realExec — raw SQL on the scratch (seeding; Repo exposes no generic
// exec).
func realExec(t *testing.T, scratchDsn, sql string) {
	t.Helper()
	pool, err := db.Connect(context.Background(), scratchDsn)
	if err != nil {
		t.Fatalf("connect scratch: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// realString — one scalar text ("" when no row or NULL).
func realString(t *testing.T, scratchDsn, sql string) string {
	t.Helper()
	pool, err := db.Connect(context.Background(), scratchDsn)
	if err != nil {
		t.Fatalf("connect scratch: %v", err)
	}
	defer pool.Close()
	var val *string
	if err := pool.QueryRow(context.Background(), sql).Scan(&val); err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return ""
		}
		t.Fatalf("query %q: %v", sql, err)
	}
	if val == nil {
		return ""
	}
	return *val
}

// TestShadowRealRegistryConformance — SUPERLITE_REAL=1: the real
// registry's verdict query executes on Doltgres, a fired round inserts
// the real-signature prediction row, and the counters land.
func TestShadowRealRegistryConformance(t *testing.T) {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		t.Skip("seam check: set SUPERLITE_REAL=1")
	}
	db.ResetShadowGateCacheForTest()
	r, scratch := realScratch(t)

	db.RegisterShadowCircuit(realShadowRules, realShadowVerdict)
	t.Cleanup(func() { db.RegisterShadowCircuit(nil, nil) })

	// Fire-shaped synthetic window: 12 reads, all 5 zero rows above the
	// 0.05 floor → ratio 100% ≥ zero_near_warn_pct 30 (threshold defaults).
	for i := 0; i < 7; i++ {
		realExec(t, scratch, `INSERT INTO read_events (hits, zero, near, fallback) VALUES (2, false, false, false)`)
	}
	for i := 0; i < 5; i++ {
		realExec(t, scratch, `INSERT INTO read_events (hits, zero, near, fallback, near_score) VALUES (0, true, false, false, 0.5)`)
	}
	realExec(t, scratch, `INSERT INTO meta (key, value) VALUES ('auto_mode', 'shadow')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`)

	// The real read-path hook (RecordReadEvent tail).
	db.ResetShadowGateCacheForTest()
	if err := r.RecordReadEvent(context.Background(), db.ReadEvent{Hits: 1}); err != nil {
		t.Fatalf("RecordReadEvent: %v", err)
	}

	got := realString(t, scratch,
		`SELECT trigger || '|' || key || '|' || cur || '|' || new FROM tuning_predictions ORDER BY id DESC LIMIT 1`)
	parts := strings.Split(got, "|")
	if len(parts) != 4 {
		t.Fatalf("예측 행 미발생 또는 형식 불일치: %q — 실 등록표 쿼리가 Doltgres에서 실행되는지 확인(FILTER 미지원 인시던트)", got)
	}
	if parts[0] != "auto_zero_near_high" || parts[1] != "suggest_min_score" {
		t.Fatalf("실 등록표 예측 행 trigger/key = %s/%s", parts[0], parts[1])
	}
	if parts[2] != "0.15" || parts[3] != "0.05" {
		t.Fatalf("실 등록표 cur/new = %s/%s, want 0.15/0.05(방향 등록표 하향)", parts[2], parts[3])
	}
	if v := realString(t, scratch, `SELECT value FROM meta WHERE key = 'fired_auto_auto_zero_near_high'`); v != "1" {
		t.Fatalf("fired 카운터 = %q, want 1", v)
	}
	if v := realString(t, scratch, `SELECT value FROM meta WHERE key = 'auto_streak_suggest_min_score'`); v != "1" {
		t.Fatalf("첫 발화 streak = %q, want 1", v)
	}
	if v := realString(t, scratch, `SELECT value FROM meta WHERE key = 'auto_rounds_suggest_min_score'`); v != "1" {
		t.Fatalf("라운드 카운터 = %q, want 1(r100-1 — 발화 라운드도 라운드로 집계)", v)
	}
}
