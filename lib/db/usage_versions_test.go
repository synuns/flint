package db

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func slowRecordUsageCountDupSources(t *testing.T, r *Repo) {
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")

	for kind, n := range map[string]int{"dup_hook": 2, "dup_manual": 3, "dup_mcp": 1} {
		if err := r.RecordUsageCount(context.Background(), kind, "", n); err != nil {
			t.Fatalf("RecordUsageCount(%s,%d): %v", kind, n, err)
		}
	}
	want := map[string]int{"dup_hook": 2, "dup_manual": 3, "dup_mcp": 1}
	for k, n := range want {
		if got := flatDailyValue(t, r, k, today); got != n {
			t.Fatalf("daily:%s:%s = %d, want %d", today, k, got, n)
		}
	}

	if err := r.RecordUsageCount(context.Background(), "dup_webhook", "", 1); err == nil {
		t.Fatalf("무효 kind dup_webhook 수용 — 기존 거부 계약 위반")
	}

	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM meta WHERE key LIKE 'last_dup%'`).Scan(&n); err != nil {
		t.Fatalf("count last_dup%%: %v", err)
	}
	if n != 0 {
		t.Fatalf("last_dup* 키 %d건 — dup 계열은 시각 키 없음", n)
	}
}

func slowRecordReadEventAndPrune(t *testing.T, r *Repo) {
	ctx := context.Background()
	if err := r.EnsureSchemaV6(ctx); err != nil {
		t.Fatalf("EnsureSchemaV6: %v", err)
	}

	const label = "v0190-prune-probe"
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM read_events WHERE session = $1`, label); err != nil {
		t.Fatalf("사전 정리: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM read_events WHERE session = $1`, label)
	})

	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 3, Near: true, Fallback: true, Session: label}); err != nil {
		t.Fatalf("RecordReadEvent: %v", err)
	}
	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 0, Zero: true, Session: label}); err != nil {
		t.Fatalf("RecordReadEvent(zero): %v", err)
	}

	if _, err := r.pool.Exec(ctx,
		`UPDATE read_events SET ts = $1
		  WHERE session = $2 AND hits = 3`,
		time.Now().UTC().Add(-20*24*time.Hour), label); err != nil {
		t.Fatalf("과거 행 갱신: %v", err)
	}

	if err := r.PruneReadEvents(ctx); err != nil {
		t.Fatalf("PruneReadEvents: %v", err)
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM read_events WHERE session = $1`, label).Scan(&n); err != nil {
		t.Fatalf("잔여 행 수 조회: %v", err)
	}
	if n != 1 {
		t.Fatalf("prune 후 잔여 %d행, want 1(최근 행만 유지)", n)
	}
	var hits int
	var zero, near, fallback bool
	if err := r.pool.QueryRow(ctx,
		`SELECT hits, zero, near, fallback FROM read_events WHERE session = $1`, label).
		Scan(&hits, &zero, &near, &fallback); err != nil {
		t.Fatalf("잔여 행 가독: %v", err)
	}
	if hits != 0 || !zero || near || fallback {
		t.Errorf("잔여 행 = {%d %v %v %v}, want {0 true false false}", hits, zero, near, fallback)
	}
}

func slowPruneRetentionOverride(t *testing.T, r *Repo) {
	ctx := context.Background()
	if err := r.EnsureSchemaV7(ctx); err != nil {
		t.Fatalf("EnsureSchemaV7: %v", err)
	}
	const label = "v0204-retention-probe"
	cleanup := func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM session_starts WHERE session = $1`, label)
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := r.pool.Exec(ctx, `INSERT INTO session_starts (session) VALUES ($1)`, label); err != nil {
		t.Fatalf("세션 삽입: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE session_starts SET ts = $1 WHERE session = $2`,
		time.Now().UTC().Add(-20*24*time.Hour), label); err != nil {
		t.Fatalf("과거화: %v", err)
	}

	if _, err := r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('events_retention_days', '100')
		 ON CONFLICT (key) DO UPDATE SET value = '100'`); err != nil {
		t.Fatalf("오버라이드 설정: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM meta WHERE key = 'events_retention_days'`)
	})
	if err := r.PruneReadEvents(ctx); err != nil {
		t.Fatalf("PruneReadEvents: %v", err)
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM session_starts WHERE session = $1`, label).Scan(&n); err != nil {
		t.Fatalf("잔여 조회: %v", err)
	}
	if n != 1 {
		t.Fatalf("오버라이드 보존 실패 — 잔여 %d행, want 1(기본이었다면 0)", n)
	}
}

func slowSessionStartAndHotEventRecords(t *testing.T, r *Repo) {
	ctx := context.Background()
	if err := r.EnsureSchemaV7(ctx); err != nil {
		t.Fatalf("EnsureSchemaV7: %v", err)
	}
	const label = "v0200-prune-probe"
	cleanup := func() {
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM session_starts WHERE session = $1`, label)
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM hot_events WHERE ids = $1`, label)
	}
	cleanup()
	t.Cleanup(cleanup)

	if err := r.RecordSessionStart(ctx, label); err != nil {
		t.Fatalf("RecordSessionStart: %v", err)
	}
	if err := r.RecordHotEvent(ctx, label); err != nil {
		t.Fatalf("RecordHotEvent: %v", err)
	}

	if _, err := r.pool.Exec(ctx,
		`UPDATE session_starts SET ts = $1 WHERE session = $2`,
		time.Now().UTC().Add(-20*24*time.Hour), label); err != nil {
		t.Fatalf("과거 세션 갱신: %v", err)
	}

	if err := r.PruneReadEvents(ctx); err != nil {
		t.Fatalf("PruneReadEvents(3테이블 통합): %v", err)
	}
	var sessN, hotN int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM session_starts WHERE session = $1`, label).Scan(&sessN); err != nil {
		t.Fatalf("session_starts 잔여 조회: %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM hot_events WHERE ids = $1`, label).Scan(&hotN); err != nil {
		t.Fatalf("hot_events 잔여 조회: %v", err)
	}
	if sessN != 0 {
		t.Errorf("20일 경과 세션 시작 잔여 %d행, want 0(정리)", sessN)
	}
	if hotN != 1 {
		t.Errorf("hot 노출 잔여 %d행, want 1(최근 행 유지)", hotN)
	}
}

func slowRecordUsageCountBrief(t *testing.T, r *Repo) {
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")

	if err := r.RecordUsageCount(context.Background(), "brief", "", 2); err != nil {
		t.Fatalf("RecordUsageCount(brief,2): %v", err)
	}
	if got := flatDailyValue(t, r, "brief", today); got != 2 {
		t.Fatalf("daily:%s:brief = %d, want 2", today, got)
	}
	var last string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT value FROM meta WHERE key = 'last_brief_at'`).Scan(&last); err != nil {
		t.Fatalf("last_brief_at 부재 — brief는 시각 키 계약 대상: %v", err)
	}

	if err := r.RecordUsageCount(context.Background(), "briefing", "", 1); err == nil {
		t.Fatalf("무효 kind briefing 수용 — 기존 거부 계약 위반")
	}
}

func cleanUsageKeys(t *testing.T, r *Repo) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(),
		`DELETE FROM meta WHERE key IN ('last_write_at','last_hot_at','last_read_at')
		    OR key LIKE 'daily:%'`); err != nil {
		t.Fatalf("clean usage keys: %v", err)
	}
}

func slowRecordUsageDailyAndPrune(t *testing.T, r *Repo) {
	ctx := context.Background()
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")

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

	if _, err := r.pool.Exec(ctx, `DELETE FROM meta WHERE key = $1`, "daily:read:"+today); err != nil {
		t.Fatalf("delete today flat key: %v", err)
	}
	if err := r.RecordUsage(ctx, "read", ""); err != nil {
		t.Fatalf("RecordUsage(read) after seeding: %v", err)
	}

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

func slowRecordUsagePrunesOnlyOnNewDayKey(t *testing.T, r *Repo) {
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

	seed()
	if err := r.RecordUsage(ctx, "write", "hook"); err != nil {
		t.Fatalf("RecordUsage #1: %v", err)
	}
	if countOld() != 0 {
		t.Fatalf("신규 생성 시 20일 전 키 잔존 — PruneDaily 미실행")
	}

	seed()
	if err := r.RecordUsage(ctx, "write", "hook"); err != nil {
		t.Fatalf("RecordUsage #2: %v", err)
	}
	if countOld() != 1 {
		t.Fatalf("기존 키 기록 시 PruneDaily 실행됨 — 롤오버 첫 이벤트에만 허용")
	}
}

func slowRecordUsageCorruptDailyRewrites(t *testing.T, r *Repo) {
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

func slowRecordUsageWriteEmptySource(t *testing.T, r *Repo) {
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

func slowRecordUsageLegacyJSONIgnoredByWrite(t *testing.T, r *Repo) {
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

func slowRecordUsageInvalidKind(t *testing.T, r *Repo) {
	cleanUsageKeys(t, r)
	if err := r.RecordUsage(context.Background(), "bogus", ""); err == nil {
		t.Fatalf("invalid kind — want error")
	}
}

func slowRecordUsageConcurrentNoError(t *testing.T, r *Repo) {
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

func slowRecordUsageCountBatches(t *testing.T, r *Repo) {
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")

	if err := r.RecordUsageCount(context.Background(), "dup", "", 3); err != nil {
		t.Fatalf("RecordUsageCount(dup,3): %v", err)
	}
	if err := r.RecordUsageCount(context.Background(), "write", "hook", 2); err != nil {
		t.Fatalf("RecordUsageCount(write,hook,2): %v", err)
	}

	want := map[string]int{"dup": 3, "write": 2, "hook": 2}
	for k, n := range want {
		if got := flatDailyValue(t, r, k, today); got != n {
			t.Fatalf("daily:%s:%s = %d, want %d", today, k, got, n)
		}
	}
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM meta WHERE key = 'last_dup_at'`).Scan(&n); err != nil {
		t.Fatalf("count last_dup_at: %v", err)
	}
	if n != 0 {
		t.Fatalf("last_dup_at 존재 — dup은 last_ 시각을 기록하지 않는다")
	}
	if err := r.RecordUsageCount(context.Background(), "dup", "", 0); err == nil {
		t.Fatalf("count=0은 에러")
	}
}

func TestPruneGateDueOpensAfter24h(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		last string
		want bool
	}{
		{"키 부재(공란) — 정리", "", true},
		{"파손값 — 파싱 실패 시 정리", "not-a-timestamp", true},
		{"23시간 전 — 스킵", now.Add(-23 * time.Hour).Format(time.RFC3339), false},
		{"24시간 전 — 정리", now.Add(-24 * time.Hour).Format(time.RFC3339), true},
		{"25시간 전 — 정리", now.Add(-25 * time.Hour).Format(time.RFC3339), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pruneGateDue(tc.last, now); got != tc.want {
				t.Errorf("pruneGateDue(%q) = %v, want %v", tc.last, got, tc.want)
			}
		})
	}
}

func TestPruneGateProbeCachesWithinTTL(t *testing.T) {
	pruneGateCheckedAt.Store(0)
	t.Cleanup(func() { pruneGateCheckedAt.Store(0) })
	base := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	if !pruneGateProbe(base) {
		t.Fatal("최초 확인 미발화 — 캐시 스탬프 오염(초기화 누락)")
	}
	if pruneGateProbe(base.Add(30 * time.Minute)) {
		t.Fatal("TTL 내 재확인 발화 — read 핫패스 게이트 SELECT 잔존")
	}
	if pruneGateProbe(base.Add(59 * time.Minute)) {
		t.Fatal("TTL 직전 재확인 발화")
	}
	if !pruneGateProbe(base.Add(time.Hour + time.Minute)) {
		t.Fatal("TTL 경과 후 재확인 미발화 — 게이트가 캐시에 얼어붙음")
	}
}

func TestRecordReadEventPrunesStaleRows(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.EnsureSchemaV9(ctx); err != nil {
		t.Fatalf("EnsureSchemaV9: %v", err)
	}
	const label = "v0101-prune-gate-probe"
	cleanup := func() {
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM read_events WHERE session = $1`, label)
		_, _ = r.pool.Exec(context.Background(),
			`DELETE FROM meta WHERE key = $1`, pruneReadGateKey)

		pruneGateCheckedAt.Store(0)
	}
	cleanup()
	t.Cleanup(cleanup)

	if _, err := r.pool.Exec(ctx,
		`INSERT INTO read_events (hits, zero, near, fallback, session, ids, first_rank)
		 VALUES (1, false, false, false, $1, '', 0)`, label); err != nil {
		t.Fatalf("과거 행 삽입: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE read_events SET ts = $1 WHERE session = $2`,
		time.Now().UTC().Add(-20*24*time.Hour), label); err != nil {
		t.Fatalf("과거화: %v", err)
	}

	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 2, Session: label}); err != nil {
		t.Fatalf("RecordReadEvent: %v", err)
	}
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM read_events WHERE session = $1`, label).Scan(&n); err != nil {
		t.Fatalf("잔여 조회: %v", err)
	}
	if n != 1 {
		t.Fatalf("게이트 열림 정리 실패 — 잔여 %d행, want 1(20일 전 행 삭제·신규 1행 유지)", n)
	}

	pruneGateCheckedAt.Store(0)
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO read_events (hits, zero, near, fallback, session, ids, first_rank)
		 VALUES (3, false, false, false, $1, '', 0)`, label); err != nil {
		t.Fatalf("과거 행 재삽입: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE read_events SET ts = $1 WHERE session = $2 AND hits = 3`,
		time.Now().UTC().Add(-20*24*time.Hour), label); err != nil {
		t.Fatalf("과거화(2): %v", err)
	}
	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 4, Session: label}); err != nil {
		t.Fatalf("RecordReadEvent(2): %v", err)
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM read_events WHERE session = $1 AND hits = 3`, label).Scan(&n); err != nil {
		t.Fatalf("게이트 닫힘 잔여 조회: %v", err)
	}
	if n != 1 {
		t.Fatalf("게이트 닫힘 오작동 — 24h 이내 재정리 잔여 %d행, want 1(스킵 계약)", n)
	}
}

func TestRecordUsageCountDupCorrectionKind(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	if err := r.RecordUsageCount(ctx, "dup_correction", "correction", 1); err != nil {
		t.Fatalf("RecordUsageCount(dup_correction): %v", err)
	}
	today := time.Now().UTC().Format("2006-01-02")
	if got := flatDailyValue(t, r, "dup_correction", today); got != 1 {
		t.Fatalf("daily:dup_correction:%s = %d, want 1 (meta flat-key proof)", today, got)
	}
}

func TestRecordUsageCountDupGapKind(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	cleanUsageKeys(t, r)
	if err := r.RecordUsageCount(ctx, "dup_gap", "gap", 1); err != nil {
		t.Fatalf("RecordUsageCount(dup_gap): %v", err)
	}
	today := time.Now().UTC().Format("2006-01-02")
	if got := flatDailyValue(t, r, "dup_gap", today); got != 1 {
		t.Fatalf("daily:dup_gap:%s = %d, want 1 (meta flat-key proof)", today, got)
	}
	if err := r.RecordUsageCount(ctx, "dup_gaps", "", 1); err == nil {
		t.Fatal("RecordUsageCount(dup_gaps) 성공 — 미등록 kind 거부 없음")
	}
}

func TestUsageSuggestCountInvariant(t *testing.T) {
	want := map[string]bool{
		"TestPruneGateDueOpensAfter24h":         true,
		"TestPruneGateProbeCachesWithinTTL":     true,
		"TestRecordReadEventPrunesStaleRows":    true,
		"TestRecordUsageCountDupCorrectionKind": true,
		"TestRecordUsageCountDupGapKind":        true,
	}
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("테스트 파일 조사 실패: %v", err)
	}
	fset := token.NewFileSet()
	got := 0
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("%s 파싱 실패: %v", name, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil {
				continue
			}
			if want[fd.Name.Name] {
				got++
			}
		}
	}
	if got != len(want) {
		t.Fatalf("usage_v 계열 카운트 = %d, want %d(통합 전후 불변)", got, len(want))
	}
	leftover, err := filepath.Glob("usage_v[0-9]*_test.go")
	if err != nil {
		t.Fatalf("접미 잔여 조사 실패: %v", err)
	}
	if len(leftover) != 0 {
		t.Fatalf("usage_v 접미 파일 잔여 %d건: %v", len(leftover), leftover)
	}
}
