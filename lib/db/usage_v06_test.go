// usage_v06_test.go — T60 RecordUsage: 유입 계측 meta 기록(실DB slow 테스트).
// 계약: last_<kind>_at RFC3339 upsert(dup 제외) + daily:YYYY-MM-DD JSON 증분,
// 14일 초과 daily 키 정리(PruneDaily — 오늘 키 신규 생성 시에만), 경합
// last-write-wins 수용.
package db

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// cleanUsageKeys — last_*·daily:* meta 키를 지워 테스트끼리 격리한다.
func cleanUsageKeys(t *testing.T, r *Repo) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(),
		`DELETE FROM meta WHERE key IN ('last_write_at','last_hot_at','last_read_at')
		    OR key LIKE 'daily:%'`); err != nil {
		t.Fatalf("clean usage keys: %v", err)
	}
}

// dailyValue — daily:<date> 값을 map으로 디코드한다(파손 시 nil).
func dailyValue(t *testing.T, r *Repo, date string) map[string]int {
	t.Helper()
	var val string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT value FROM meta WHERE key = $1`, "daily:"+date).Scan(&val); err != nil {
		t.Fatalf("read daily:%s: %v", date, err)
	}
	m := map[string]int{}
	if err := json.Unmarshal([]byte(val), &m); err != nil {
		t.Fatalf("daily:%s not JSON: %q", date, val)
	}
	return m
}

// TestRecordUsageDailyAndPrune — write·hot·read·dup 기록 → JSON 값·last_* 갱신
// ·15일치 심기 → 초과 키 삭제(플랜 T60 테스트 코드).
func TestRecordUsageDailyAndPrune(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")

	// write 2회(hook·mcp) + hot + read + dup.
	for _, call := range []struct{ kind, source string }{
		{"write", "hook"}, {"write", "mcp"}, {"hot", ""}, {"read", ""}, {"dup", ""},
	} {
		if err := r.RecordUsage(ctx, call.kind, call.source); err != nil {
			t.Fatalf("RecordUsage(%q,%q): %v", call.kind, call.source, err)
		}
	}
	got := dailyValue(t, r, today)
	want := map[string]int{"write": 2, "hook": 1, "mcp": 1, "hot": 1, "read": 1, "dup": 1}
	for k, n := range want {
		if got[k] != n {
			t.Fatalf("daily:%s[%q] = %d, want %d (full=%v)", today, k, got[k], n, got)
		}
	}

	// last_* 3키 RFC3339 파싱 가능·dup는 last_dup_at 미기록.
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

	// 15일치(어제~15일 전) 심기 + 오늘 키 삭제(롤오버 첫 이벤트 시뮬레이션)
	// → 다음 기록이 오늘 키를 신규 생성하며 PruneDaily(신규 생성 시에만
	// 실행)로 초과 키 삭제.
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
	if err := r.RecordUsage(ctx, "read", ""); err != nil {
		t.Fatalf("RecordUsage(read) after seeding: %v", err)
	}
	// 보존 14일 = 오늘 + 어제~13일 전. 14·15일 전 키는 삭제.
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
	// 어제~13일 전 키는 보존·오늘 키는 신규 생성 경로로 재기록(read=1).
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
	if got := dailyValue(t, r, today); got["read"] != 1 {
		t.Fatalf("daily:%s read = %d, want 1(신규 생성 재기록)", today, got["read"])
	}
}

// TestRecordUsagePrunesOnlyOnNewDayKey — PruneDaily는 오늘 daily 키가 신규
// 생성된 경우(롤오버 첫 이벤트)에만 실행된다(회귀: RecordUsage마다 DELETE
// 실행 — 핫패스 낭비·duplicate write 2회 정리).
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

	// 1회째: 오늘 키 신규 생성 → 정리 실행 → 20일 전 키 삭제.
	seed()
	if err := r.RecordUsage(ctx, "write", "hook"); err != nil {
		t.Fatalf("RecordUsage #1: %v", err)
	}
	if countOld() != 0 {
		t.Fatalf("신규 생성 시 20일 전 키 잔존 — PruneDaily 미실행")
	}
	// 2회째: 오늘 키 이미 존재 → 정리 건너뜀 → 재심은 20일 전 키 잔존.
	seed()
	if err := r.RecordUsage(ctx, "write", "hook"); err != nil {
		t.Fatalf("RecordUsage #2: %v", err)
	}
	if countOld() != 1 {
		t.Fatalf("기존 키 기록 시 PruneDaily 실행됨 — 롤오버 첫 이벤트에만 허용")
	}
}

// TestRecordUsageCorruptDailyRewrites — 파손된 daily JSON은 0에서 재기작
// (Review Focus encoding: 파손 daily JSON 0 재기작).
func TestRecordUsageCorruptDailyRewrites(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, '{broken-json')`, "daily:"+today); err != nil {
		t.Fatalf("seed corrupt daily: %v", err)
	}
	if err := r.RecordUsage(ctx, "write", "manual"); err != nil {
		t.Fatalf("RecordUsage on corrupt daily: %v", err)
	}
	got := dailyValue(t, r, today)
	if got["write"] != 1 || got["manual"] != 1 {
		t.Fatalf("corrupt daily rewrite = %v, want write=1 manual=1", got)
	}
}

// TestRecordUsageWriteEmptySource — write에 source=""면 write 카운터만.
func TestRecordUsageWriteEmptySource(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")
	if err := r.RecordUsage(ctx, "write", ""); err != nil {
		t.Fatalf("RecordUsage(write,\"\"): %v", err)
	}
	got := dailyValue(t, r, today)
	if got["write"] != 1 || len(got) != 1 {
		t.Fatalf("daily = %v, want {write:1} only", got)
	}
}

// TestRecordUsageNullDailyRewrites — 기존 값이 JSON null이면 nil map 증분
// panic 없이 0에서 재기작한다(회귀: cur[k]+=n on nil map).
func TestRecordUsageNullDailyRewrites(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, 'null')`, "daily:"+today); err != nil {
		t.Fatalf("seed null daily: %v", err)
	}
	if err := r.RecordUsage(ctx, "write", "hook"); err != nil {
		t.Fatalf("RecordUsage on null daily: %v", err)
	}
	got := dailyValue(t, r, today)
	if got["write"] != 1 || got["hook"] != 1 {
		t.Fatalf("null daily rewrite = %v, want write=1 hook=1", got)
	}
}

// TestRecordUsagePartialCorruptDailyRewrites — 부분 파손 JSON(일부 키만 타입
// 불일치)도 통째로 파손으로 간주해 0에서 재기작한다(부분 디코드 누출 방지).
func TestRecordUsagePartialCorruptDailyRewrites(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ($1, '{"write":5,"hot":"x"}')`,
		"daily:"+today); err != nil {
		t.Fatalf("seed partial-corrupt daily: %v", err)
	}
	if err := r.RecordUsage(ctx, "write", "hook"); err != nil {
		t.Fatalf("RecordUsage on partial-corrupt daily: %v", err)
	}
	got := dailyValue(t, r, today)
	if got["write"] != 1 || got["hook"] != 1 || got["hot"] != 0 {
		t.Fatalf("partial-corrupt daily rewrite = %v, want write=1 hook=1 hot=0", got)
	}
}

// TestRecordUsageInvalidKind — 계약 밖 kind는 에러(기록 없음).
func TestRecordUsageInvalidKind(t *testing.T) {
	r := newTestRepo(t)
	cleanUsageKeys(t, r)
	if err := r.RecordUsage(context.Background(), "bogus", ""); err == nil {
		t.Fatalf("invalid kind — want error")
	}
}

// TestRecordUsageConcurrentLastWriteWins — 병렬 기록은 에러 없이 last-write-wins
// 로 수용(플랜 T60: 경합 수용 계약 — 총계 불일치 가능, 손실 없는 것은 보장하지 않음).
func TestRecordUsageConcurrentLastWriteWins(t *testing.T) {
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
	if got := dailyValue(t, r, today); got["write"] < 1 || got["hook"] < 1 {
		t.Fatalf("daily = %v, want write≥1 hook≥1 (last-write-wins)", got)
	}
}
