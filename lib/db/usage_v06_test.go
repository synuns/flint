// usage_v06_test.go — T60 RecordUsage: 유입 계측 meta 기록(실DB slow 테스트).
// 계약: last_<kind>_at RFC3339 upsert(dup 제외) + daily:YYYY-MM-DD JSON 증분,
// 14일 초과 daily 키 정리(PruneDaily — 오늘 키 신규 생성 시에만), 경합
// last-write-wins 수용.
package db

import (
	"context"
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

// dailyValue(구 daily:<date> JSON 디코더)는 v1.2.0 T1 플랫 키 전환으로
// 폐지 — 플랫 읽기는 flatDailyValue(usage_flatkey_test.go)·구 JSON 폴백은
// readDailyFlat이 담당한다.

// TestRecordUsageDailyAndPrune — write·hot·read·dup 기록 → 플랫 키 값·last_*
// 갱신·15일치 심기 → 초과 키 삭제(플랜 T60. v1.2.0 T1: 플랫 키 단언으로 갱신).
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
	want := map[string]int{"write": 2, "hook": 1, "mcp": 1, "hot": 1, "read": 1, "dup": 1}
	for k, n := range want {
		if got := flatDailyValue(t, r, k, today); got != n {
			t.Fatalf("daily:%s:%s = %d, want %d", today, k, got, n)
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
	// 플랫 키도 함께 지운다(v1.2.0 T1 — read kind는 플랫 키 daily:read:<오늘>이
	// 롤오버 created 판정의 대상이다).
	if _, err := r.pool.Exec(ctx, `DELETE FROM meta WHERE key = $1`, "daily:read:"+today); err != nil {
		t.Fatalf("delete today flat key: %v", err)
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
	if got := flatDailyValue(t, r, "read", today); got != 1 {
		t.Fatalf("daily:%s read = %d, want 1(신규 생성 재기록)", today, got)
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

// TestRecordUsageCorruptDailyRewrites — 파손된 플랫 키 값은 0에서 재기작
// (v1.2.0 T1: 구 JSON 파손 재기작 계약의 플랫 승계 — 정수 아닌 기존 값).
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

// TestRecordUsageWriteEmptySource — write에 source=""면 write 카운터만.
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

// TestRecordUsageLegacyJSONIgnoredByWrite — 구 daily:<date> JSON은 쓰기
// 경로가 건드리지 않는다(v1.2.0 T1 읽기 폴백 전용 승계 — null·부분
// 파손 파상 재기작 회귀는 readDailyFlat 폴백 계약으로 이전,
// usage_flatkey_test.go TestReadDailyFlatLegacyFallback 참조).
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

// TestRecordUsageInvalidKind — 계약 밖 kind는 에러(기록 없음).
func TestRecordUsageInvalidKind(t *testing.T) {
	r := newTestRepo(t)
	cleanUsageKeys(t, r)
	if err := r.RecordUsage(context.Background(), "bogus", ""); err == nil {
		t.Fatalf("invalid kind — want error")
	}
}

// TestRecordUsageConcurrentNoError — 병렬 기록은 에러 없이 수용된다(v1.2.0
// T1: 구 last-write-wins JSON 경합 계약의 플랫 승계. Doltgres는 같은 행
// 동시 UPSERT를 에러 없이 last-write-wins 수용 — 총계 정합은 보장하지
// 않음, 유실 < 호출 실패 계측 원칙. 원자성은 Postgres 기준).
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
