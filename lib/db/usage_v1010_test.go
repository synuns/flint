// usage_v1010_test.go — the read-path cleanup hook
// (maybePruneAfterRead). With RecordReadUsage abolished, the read path
// no longer passes through recordWithRetry (the rollover hook) — a
// regression of unbounded read_events growth in read-only sessions.
// The gate verdict is a pure-function unit (fast); the cleanup·gate
// holding is the real-DB slow line.
package db

import (
	"context"
	"testing"
	"time"
)

// TestPruneGateDueOpensAfter24h — the gate verdict: absent·blank·
// corrupt·24h-elapsed opens (cleanup runs); a valid RFC3339 within 24h
// closes (skipped — preventing cleanup on every read).
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

// TestPruneGateProbeCachesWithinTTL — the in-process gate cache. probe
// true = maybePruneAfterRead runs the gate meta SELECT — the first
// check once (true), 0 rechecks within the TTL (false — defending
// against the residual per-read SELECT regression), 1 recheck after TTL
// expiry (true — the gate never freezes beyond 1h).
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

// TestRecordReadEventPrunesStaleRows — even in a session doing reads
// only (no write metering — the recordWithRetry rollover hook never
// fires), the 24h-gated hook on the RecordReadEvent success path cleans
// read_events rows past the retention horizon. When the gate is closed
// (a recent successful cleanup, within 24h), the cleanup is skipped (a
// DELETE on every read wastes the hot path).
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
		// The in-process gate cache resets too — so a timestamp stamped by
		// another test in the same package never accidentally overrides
		// this test's DB-gate verdict.
		pruneGateCheckedAt.Store(0)
	}
	cleanup()
	t.Cleanup(cleanup)

	// Seed a 20-day-old row directly — reproducing the residue of a
	// read-only session without writes.
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

	// One read — gate open (key absent) → the hook cleans the
	// out-of-14-day-window row.
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

	// Gate closed (a recent successful cleanup — a stamp within 24h): a
	// re-seeded past row survives uncleaned. The cache is reset and the
	// DB gate's (24h stamp) closure verified directly — the outcome must
	// not be an accidental pass indistinguishable from a process-cache
	// skip.
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
