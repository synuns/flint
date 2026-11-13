// usage_flatkey_test.go — v1.2.0 T1 daily 플랫 키 전환(실DB slow — fast에서는
// FLINT_TEST_DSN 부재로 스킵). 계약: daily:<kind>:<date> 정수 값 단일 UPSERT
// 원자 증분(bumpMeta 패턴 — 읽기-수정-쓰기 last-write-wins 폐지)·파손 값은
// 0에서 재기작·readDailyFlat은 플랫 키 우선 + 구 daily:<date> JSON 폴백 승계
// (기존 창 통계 손실 방지 — 읽기 전용)·PruneDaily는 양쪽 키 형식 정리.
package db

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"
)

// flatDailyValue — daily:<kind>:<date> 값을 정수로 읽는다(테스트 헬퍼).
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

// TestFlatKeyAtomicIncrement — Review Focus: 증분(연속 가산)·파손 0 재기작·
// 병렬 원자 증분(경합 손실 폐지 — 플랫 전환의 동기).
// e.g. bumpDailyFlat("2026-11-13","hot_bytes",1180) → 기존+1180 단일 UPSERT.
func TestFlatKeyAtomicIncrement(t *testing.T) {
	r := newTestRepo(t)
	cleanUsageKeys(t, r)
	ctx := context.Background()
	today := time.Now().UTC().Format("2006-01-02")

	// 증분 — 연속 기록은 가산된다(1180 + 820 = 2000, 단일 UPSERT 2회).
	if _, err := r.bumpDailyFlat(ctx, today, "hot_bytes", 1180); err != nil {
		t.Fatalf("bumpDailyFlat(hot_bytes,1180): %v", err)
	}
	if _, err := r.bumpDailyFlat(ctx, today, "hot_bytes", 820); err != nil {
		t.Fatalf("bumpDailyFlat(hot_bytes,820): %v", err)
	}
	if got := flatDailyValue(t, r, "hot_bytes", today); got != 2000 {
		t.Fatalf("daily:hot_bytes:%s = %d, want 2000(1180+820 증분)", today, got)
	}

	// kind별 키 분리 — 다른 kind는 같은 날짜라도 별도 키.
	if _, err := r.bumpDailyFlat(ctx, today, "read_bytes", 42); err != nil {
		t.Fatalf("bumpDailyFlat(read_bytes,42): %v", err)
	}
	if got := flatDailyValue(t, r, "read_bytes", today); got != 42 {
		t.Fatalf("daily:read_bytes:%s = %d, want 42(키 kind별 분리)", today, got)
	}

	// 파손 0 재기작 — 정수 아닌 기존 값은 0에서 다시 센다(5).
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

	// 병렬 기록 — 에러 없이 수용되고 카운터는 1 이상. Doltgres는 같은 행의
	// 동시 UPSERT를 조용히 last-write-wins 수용한다(2026-11-13 실증 — 에러
	// 없이 일부 증분 손실, 구 JSON 경합 계약과 동일 수준. 단일 문장 원자성은
	// Postgres 기준이며 Doltgres에서는 오류·재시기 없는 완화형).
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

// TestReadDailyFlatLegacyFallback — Review Focus: 플랫 키 우선·구
// daily:<date> JSON 폴백 승계. 플랫 키가 없으면 구 JSON 값을 읽고(마이그레이션
// 읽기 승계), 플랫 키가 생기면 그쪽이 이긴다. 폴백 JSON 파손·부재·null은 0.
func TestReadDailyFlatLegacyFallback(t *testing.T) {
	r := newTestRepo(t)
	cleanUsageKeys(t, r)
	ctx := context.Background()
	today := time.Now().UTC().Format("2006-01-02")

	// 구 JSON 승계 — 플랫 키 부재 시 폴백 읽기(쓰기 마이그레이션 없음).
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

	// 플랫 우선 — 플랫 키가 생기면 구 JSON과 무관하게 그 값.
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

	// 폴백 JSON 파손·null·부재 키 — 0(재기작 계약과 정합, panic 없음).
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

// TestPruneDailyBothKeyForms — Review Focus: PruneDaily는 구
// daily:<date> JSON 키와 신규 daily:<kind>:<date> 플랫 키를 모두 정리한다
// (보존 14일 불변 — 오늘+직전 13일).
func TestPruneDailyBothKeyForms(t *testing.T) {
	r := newTestRepo(t)
	cleanUsageKeys(t, r)
	ctx := context.Background()
	day := func(age int) string {
		return time.Now().UTC().AddDate(0, 0, -age).Format("2006-01-02")
	}
	// 15일 전(삭제 대상) 양쪽 형식 + 어제(보존) 플랫 키.
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
