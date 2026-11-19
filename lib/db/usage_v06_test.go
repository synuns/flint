// usage_v06_test.go — RecordUsage: intake-metering meta recording
// (real-DB slow tests). Contract: last_<kind>_at RFC3339 upsert (dup
// excluded) + daily:YYYY-MM-DD JSON increments, cleanup of daily keys
// beyond 14 days (PruneDaily — only when today's key is newly created),
// race last-write-wins accepted.
package db

import (
	"context"
	"sync"
	"testing"
	"time"
)

// cleanUsageKeys — wipes the last_*·daily:* meta keys to isolate tests.
func cleanUsageKeys(t *testing.T, r *Repo) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(),
		`DELETE FROM meta WHERE key IN ('last_write_at','last_hot_at','last_read_at')
		    OR key LIKE 'daily:%'`); err != nil {
		t.Fatalf("clean usage keys: %v", err)
	}
}

// dailyValue (the legacy daily:<date> JSON decoder) was abolished by
// the flat-key switch — flat reads belong to flatDailyValue
// (usage_flatkey_test.go)·the legacy JSON fallback to readDailyFlat.

// TestRecordUsageDailyAndPrune — write·hot·read·dup records → flat-key
// values·last_* renewal·seeding 15 days' worth → excess keys deleted
// (updated to flat-key assertions).
func TestRecordUsageDailyAndPrune(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")

	// write ×2 (hook·mcp) + hot + read + dup.
	for _, call := range []struct{ kind, source string }{
		{"write", "hook"}, {"write", "mcp"}, {"hot", ""}, {"read", ""}, {"dup", ""},
	} {
		if err := r.RecordUsage(ctx, call.kind, call.source); err != nil {
			t.Fatalf("RecordUsage(%q,%q): %v", call.kind, call.source, err)
		}
	}
	want := map[string]int{"write": 2, "hook": 1, "mcp": 1, "hot": 1, "read": 1, "dup": 1}
	for k, n := range want {
		if got := flatDailyValue(t, r, k, today); got != n {
			t.Fatalf("daily:%s:%s = %d, want %d", today, k, got, n)
		}
	}

	// The 3 last_* keys parse as RFC3339; dup records no last_dup_at.
	for _, key := range []string{"last_write_at", "last_hot_at", "last_read_at"} {
		var val string
		if err := r.pool.QueryRow(ctx,
			`SELECT value FROM meta WHERE key = $1`, key).Scan(&val); err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		if _, err := time.Parse(time.RFC3339, val); err != nil {
			t.Fatalf("%s = %q, want RFC3339", key, val)
		}
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM meta WHERE key = 'last_dup_at'`).Scan(&n); err != nil {
		t.Fatalf("count last_dup_at: %v", err)
	}
	if n != 0 {
		t.Fatalf("last_dup_at exists — dup은 last_ 시각을 기록하지 않는다")
	}

	// Seed 15 days (yesterday~15 days ago) + delete today's key
	// (simulating the rollover's first event) → the next record newly
	// creates today's key and PruneDaily (run only on new creation)
	// deletes the excess keys.
	for i := 1; i <= 15; i++ {
		d := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO meta (key, value) VALUES ($1, '{"write":9}')`,
			"daily:"+d); err != nil {
			t.Fatalf("seed daily:%s: %v", d, err)
		}
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM meta WHERE key = $1`, "daily:"+today); err != nil {
		t.Fatalf("delete today key: %v", err)
	}
	// Delete the flat key too (the read kind's flat key
	// daily:read:<today> is the rollover created-verdict target).
	if _, err := r.pool.Exec(ctx, `DELETE FROM meta WHERE key = $1`, "daily:read:"+today); err != nil {
		t.Fatalf("delete today flat key: %v", err)
	}
	if err := r.RecordUsage(ctx, "read", ""); err != nil {
		t.Fatalf("RecordUsage(read) after seeding: %v", err)
	}
	// The 14-day retention = today + yesterday~13 days ago. Keys 14·15
	// days old are deleted.
	for _, age := range []int{14, 15} {
		d := time.Now().UTC().AddDate(0, 0, -age).Format("2006-01-02")
		if err := r.pool.QueryRow(ctx,
			`SELECT count(*) FROM meta WHERE key = $1`, "daily:"+d).Scan(&n); err != nil {
			t.Fatalf("count daily:%s: %v", d, err)
		}
		if n != 0 {
			t.Fatalf("daily key %d일 전 잔존 — 14일 초과 미삭제", age)
		}
	}
	// Keys yesterday~13 days ago are retained; today's key is re-recorded
	// via the new-creation path (read=1).
	for _, age := range []int{1, 13} {
		d := time.Now().UTC().AddDate(0, 0, -age).Format("2006-01-02")
		if err := r.pool.QueryRow(ctx,
			`SELECT count(*) FROM meta WHERE key = $1`, "daily:"+d).Scan(&n); err != nil {
			t.Fatalf("count daily:%s: %v", d, err)
		}
		if n != 1 {
			t.Fatalf("보존 범위 키 daily:%s 삭제됨", d)
		}
	}
	if got := flatDailyValue(t, r, "read", today); got != 1 {
		t.Fatalf("daily:%s read = %d, want 1(신규 생성 재기록)", today, got)
	}
}

// TestRecordUsagePrunesOnlyOnNewDayKey — PruneDaily runs only when
// today's daily key is newly created (the rollover's first event)
// (regression: a DELETE on every RecordUsage — hot-path waste·duplicate
// writes cleaning twice).
func TestRecordUsagePrunesOnlyOnNewDayKey(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	old := time.Now().UTC().AddDate(0, 0, -20).Format("2006-01-02")
	seed := func() {
		t.Helper()
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO meta (key, value) VALUES ($1, '{"write":9}')`,
			"daily:"+old); err != nil {
			t.Fatalf("seed old daily: %v", err)
		}
	}
	var n int
	countOld := func() int {
		t.Helper()
		if err := r.pool.QueryRow(ctx,
			`SELECT count(*) FROM meta WHERE key = $1`, "daily:"+old).Scan(&n); err != nil {
			t.Fatalf("count old daily: %v", err)
		}
		return n
	}

	// 1st: today's key newly created → cleanup runs → the 20-day-old key
	// deleted.
	seed()
	if err := r.RecordUsage(ctx, "write", "hook"); err != nil {
		t.Fatalf("RecordUsage #1: %v", err)
	}
	if countOld() != 0 {
		t.Fatalf("신규 생성 시 20일 전 키 잔존 — PruneDaily 미실행")
	}
	// 2nd: today's key already exists → cleanup skipped → the re-seeded
	// 20-day-old key survives.
	seed()
	if err := r.RecordUsage(ctx, "write", "hook"); err != nil {
		t.Fatalf("RecordUsage #2: %v", err)
	}
	if countOld() != 1 {
		t.Fatalf("기존 키 기록 시 PruneDaily 실행됨 — 롤오버 첫 이벤트에만 허용")
	}
}

// TestRecordUsageCorruptDailyRewrites — a corrupted flat-key value is
// rewritten from 0 (the flat inheritance of the legacy JSON
// corrupt-rewrite contract — any non-integer existing value).
func TestRecordUsageCorruptDailyRewrites(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, '{broken-json')`,
		"daily:write:"+today); err != nil {
		t.Fatalf("seed corrupt flat daily: %v", err)
	}
	if err := r.RecordUsage(ctx, "write", "manual"); err != nil {
		t.Fatalf("RecordUsage on corrupt daily: %v", err)
	}
	if got := flatDailyValue(t, r, "write", today); got != 1 {
		t.Fatalf("corrupt daily rewrite write = %d, want 1", got)
	}
	if got := flatDailyValue(t, r, "manual", today); got != 1 {
		t.Fatalf("corrupt daily rewrite manual = %d, want 1", got)
	}
}

// TestRecordUsageWriteEmptySource — write with source="" updates only
// the write counter.
func TestRecordUsageWriteEmptySource(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")
	if err := r.RecordUsage(ctx, "write", ""); err != nil {
		t.Fatalf("RecordUsage(write,\"\"): %v", err)
	}
	if got := flatDailyValue(t, r, "write", today); got != 1 {
		t.Fatalf("daily:write:%s = %d, want 1", today, got)
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM meta WHERE key LIKE 'daily:%:' || $1`, today).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("오늘 플랫 키 %d건, want 1(write만 — source 부재)", n)
	}
}

// TestRecordUsageLegacyJSONIgnoredByWrite — the legacy daily:<date> JSON
// is untouched by the write path (a read-fallback-only inheritance — the
// null·partial-corruption rewrite regression moved to the readDailyFlat
// fallback contract; see usage_flatkey_test.go
// TestReadDailyFlatLegacyFallback).
func TestRecordUsageLegacyJSONIgnoredByWrite(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")
	for i, legacy := range []string{"null", `{"write":5,"hot":"x"}`} {
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO meta (key, value) VALUES ($1, $2)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
			"daily:"+today, legacy); err != nil {
			t.Fatalf("seed legacy %q: %v", legacy, err)
		}
		if err := r.RecordUsage(ctx, "write", "hook"); err != nil {
			t.Fatalf("RecordUsage on legacy %q: %v", legacy, err)
		}
		var keep string
		if err := r.pool.QueryRow(ctx,
			`SELECT value FROM meta WHERE key = $1`, "daily:"+today).Scan(&keep); err != nil {
			t.Fatalf("read legacy: %v", err)
		}
		if keep != legacy {
			t.Fatalf("구 JSON 키가 쓰기에 의해 변경됨: %q → %q(폴백 원본 보존)", legacy, keep)
		}
		if got := flatDailyValue(t, r, "write", today); got != i+1 {
			t.Fatalf("플랫 write = %d, want %d(legacy %q 무관 증분)", got, i+1, legacy)
		}
	}
}

// TestRecordUsageInvalidKind — an out-of-contract kind is an error
// (nothing recorded).
func TestRecordUsageInvalidKind(t *testing.T) {
	r := newTestRepo(t)
	cleanUsageKeys(t, r)
	if err := r.RecordUsage(context.Background(), "bogus", ""); err == nil {
		t.Fatalf("invalid kind — want error")
	}
}

// TestRecordUsageConcurrentNoError — parallel records are accepted
// without error (the flat inheritance of the legacy last-write-wins JSON
// race contract. Doltgres accepts concurrent UPSERTs of the same row as
// last-write-wins without error — total-count consistency not
// guaranteed; the loss < call-failure metering principle. Atomicity is
// the Postgres baseline).
func TestRecordUsageConcurrentNoError(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- r.RecordUsage(ctx, "write", "hook")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent RecordUsage: %v", err)
		}
	}
	today := time.Now().UTC().Format("2006-01-02")
	if got := flatDailyValue(t, r, "write", today); got < 1 {
		t.Fatalf("daily:write:%s = %d, want ≥1(최후 승자라도 기록)", today, got)
	}
}
