// usage_flatkey_test.go — the daily flat-key switch (real-DB slow —
// skipped in fast mode without FLINT_TEST_DSN). Contract: single-UPSERT
// atomic increments of the daily:<kind>:<date> integer values (the
// bumpMeta pattern — abolishing read-modify-write last-write-wins)·
// corrupted values rewritten from 0·readDailyFlat prefers the flat key +
// inherits the legacy daily:<date> JSON fallback (preventing loss of
// existing window statistics — read-only)·PruneDaily cleans both key
// forms.
package db

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"
)

// flatDailyValue — reads the daily:<kind>:<date> value as an integer
// (a test helper).
func flatDailyValue(t *testing.T, r *Repo, kind, date string) int {
	t.Helper()
	var val string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT value FROM meta WHERE key = $1`, "daily:"+kind+":"+date).Scan(&val); err != nil {
		t.Fatalf("read daily:%s:%s: %v", kind, date, err)
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		t.Fatalf("daily:%s:%s = %q, want 정수(파손 재기작 후여야 함)", kind, date, val)
	}
	return n
}

// TestFlatKeyAtomicIncrement — increments (consecutive additions)·
// corrupt rewrite-from-0·parallel atomic increments (abolishing race
// loss — the motivation of the flat switch).
// e.g. bumpDailyFlat("2026-11-13","hot_bytes",1180) → existing+1180 in
// one UPSERT.
func TestFlatKeyAtomicIncrement(t *testing.T) {
	r := newTestRepo(t)
	cleanUsageKeys(t, r)
	ctx := context.Background()
	today := time.Now().UTC().Format("2006-01-02")

	// Increments — consecutive records add up (1180 + 820 = 2000, 2
	// single UPSERTs).
	if _, err := r.bumpDailyFlat(ctx, today, "hot_bytes", 1180); err != nil {
		t.Fatalf("bumpDailyFlat(hot_bytes,1180): %v", err)
	}
	if _, err := r.bumpDailyFlat(ctx, today, "hot_bytes", 820); err != nil {
		t.Fatalf("bumpDailyFlat(hot_bytes,820): %v", err)
	}
	if got := flatDailyValue(t, r, "hot_bytes", today); got != 2000 {
		t.Fatalf("daily:hot_bytes:%s = %d, want 2000(1180+820 증분)", today, got)
	}

	// Per-kind key separation — a different kind gets a separate key even
	// on the same date.
	if _, err := r.bumpDailyFlat(ctx, today, "read_bytes", 42); err != nil {
		t.Fatalf("bumpDailyFlat(read_bytes,42): %v", err)
	}
	if got := flatDailyValue(t, r, "read_bytes", today); got != 42 {
		t.Fatalf("daily:read_bytes:%s = %d, want 42(키 kind별 분리)", today, got)
	}

	// Corrupt rewrite-from-0 — a non-integer existing value recounts from
	// 0 (5).
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		"daily:brief:"+today, "{oops not json"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.bumpDailyFlat(ctx, today, "brief", 5); err != nil {
		t.Fatalf("bumpDailyFlat(brief,5) 파손 키 위에서: %v", err)
	}
	if got := flatDailyValue(t, r, "brief", today); got != 5 {
		t.Fatalf("파손 재기작 후 daily:brief:%s = %d, want 5(0에서 재기작)", today, got)
	}

	// Parallel records — accepted without error, and the counter is at
	// least 1. Doltgres quietly accepts concurrent UPSERTs of the same
	// row as last-write-wins (demonstrated 2026-11-13 — some increments
	// lost without error, the same level as the legacy JSON race
	// contract. Single-statement atomicity is the Postgres baseline; on
	// Doltgres it is the relaxed form with no error·no retry).
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM meta WHERE key = $1`, "daily:write:"+today); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.bumpDailyFlat(ctx, today, "write", 1)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("병렬 bumpDailyFlat: %v — 경합 시에도 기록 자체는 에러 없어야 한다", err)
		}
	}
	if got := flatDailyValue(t, r, "write", today); got < 1 {
		t.Fatalf("병렬 8회 증분 daily:write:%s = %d, want ≥1(최후 승자라도 기록)", today, got)
	}
}

// TestReadDailyFlatLegacyFallback — flat key preferred·the legacy
// daily:<date> JSON fallback inherited. When the flat key is absent,
// the legacy JSON value is read (a migration-read inheritance); once
// the flat key exists, it wins. Corrupt·absent·null fallback JSON is 0.
func TestReadDailyFlatLegacyFallback(t *testing.T) {
	r := newTestRepo(t)
	cleanUsageKeys(t, r)
	ctx := context.Background()
	today := time.Now().UTC().Format("2006-01-02")

	// Legacy-JSON inheritance — the fallback read when the flat key is
	// absent (no write migration).
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, $2)`,
		"daily:"+today, `{"write":4,"hot":7}`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind string
		want int
	}{
		{"hot", 7}, {"write", 4}, {"read", 0},
	} {
		got, err := r.readDailyFlat(ctx, tc.kind, today)
		if err != nil {
			t.Fatalf("readDailyFlat(%q): %v", tc.kind, err)
		}
		if got != tc.want {
			t.Fatalf("readDailyFlat(%q) = %d, want %d(구 JSON 폴백)", tc.kind, got, tc.want)
		}
	}

	// Flat priority — once the flat key exists, its value regardless of
	// the legacy JSON.
	if _, err := r.bumpDailyFlat(ctx, today, "write", 1); err != nil {
		t.Fatal(err)
	}
	got, err := r.readDailyFlat(ctx, "write", today)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("readDailyFlat(write) = %d, want 1(플랫 키 우선 — 폴백값 4 아님)", got)
	}

	// Corrupt·null·absent-key fallback JSON — 0 (aligned with the
	// rewrite contract, no panic).
	for _, corrupt := range []string{"{broken", "null", `{"write":"x"}`} {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO meta (key, value) VALUES ($1, $2)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
			"daily:"+today, corrupt); err != nil {
			t.Fatal(err)
		}
		got, err := r.readDailyFlat(ctx, "hot", today)
		if err != nil {
			t.Fatalf("readDailyFlat 파손 폴백 %q: %v", corrupt, err)
		}
		if got != 0 {
			t.Fatalf("파손 폴백 %q → %d, want 0", corrupt, got)
		}
	}
}

// TestPruneDailyBothKeyForms — PruneDaily cleans both the legacy
// daily:<date> JSON keys and the new daily:<kind>:<date> flat keys (the
// 14-day retention unchanged — today + the prior 13 days).
func TestPruneDailyBothKeyForms(t *testing.T) {
	r := newTestRepo(t)
	cleanUsageKeys(t, r)
	ctx := context.Background()
	day := func(age int) string {
		return time.Now().UTC().AddDate(0, 0, -age).Format("2006-01-02")
	}
	// Both forms 15 days old (deletion targets) + a yesterday flat key
	// (retained).
	for _, stmt := range []struct{ key, val string }{
		{"daily:" + day(15), `{"write":9}`},
		{"daily:write:" + day(15), "9"},
		{"daily:hot_bytes:" + day(14), "100"},
		{"daily:write:" + day(1), "3"},
	} {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO meta (key, value) VALUES ($1, $2)`, stmt.key, stmt.val); err != nil {
			t.Fatalf("seed %s: %v", stmt.key, err)
		}
	}
	if err := r.PruneDaily(ctx); err != nil {
		t.Fatalf("PruneDaily: %v", err)
	}
	var n int
	for _, gone := range []string{"daily:" + day(15), "daily:write:" + day(15), "daily:hot_bytes:" + day(14)} {
		if err := r.pool.QueryRow(ctx,
			`SELECT count(*) FROM meta WHERE key = $1`, gone).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("14일 초과 키 %s 잔존 — 양쪽 형식 정리 계약 위반", gone)
		}
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM meta WHERE key = $1`, "daily:write:"+day(1)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("보존 범위(어제) 플랫 키 삭제됨 — 14일 불변 위반")
	}
}
