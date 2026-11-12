// usage_v1010_test.go — v1.0.1 F2: read 경로 정리 훅(maybePruneAfterRead).
// v0.42.0 T2가 RecordReadUsage를 폐지하면서 read가 recordWithRetry(롤오버
// 훅)를 지나지 않게 됐다 — read 전용 세션의 read_events 무한 증가 회귀.
// 게이트 판정은 순수 함수 단위(패스트), 정리·게이트 보유는 실DB 슬로우 라인.
package db

import (
	"context"
	"testing"
	"time"
)

// TestPruneGateDueOpensAfter24h — 게이트 판정: 부재·공란·파손·24h 경과는
// 열림(정리 실행), 24h 이내 정상 RFC3339는 닫힘(스킵 — 매 read 정리 방지).
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

// TestPruneGateProbeCachesWithinTTL — Review Focus(v1.0.2 R2 성능): 프로세스
// 내 게이트 캐시. probe 참 = maybePruneAfterRead가 게이트 meta SELECT를
// 실행한다 — 최초 확인 1회(참), TTL 내 재확인 0회(거짓 — 매 read SELECT
// 잔존 회귀 방어), TTL 경과 후 재확인 1회(참 — 게이트가 1h 넘게 얼어붙지
// 않음).
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

// TestRecordReadEventPrunesStaleRows — Review Focus(v1.0.1 F2): read만
// 반복하는 세션(write 계측 없음 — recordWithRetry 롤오버 훅 미발화)에서도
// RecordReadEvent 성공 경로의 24h 게이트 훅이 보존 한게 넘은 read_events
// 행을 정리한다. 게이트가 닫힌(직전 정리 성공, 24h 이내) 경우에는 정리를
// 스킵한다(매 read DELETE는 핫패스 낭비).
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
		// 프로세스 내 게이트 캐시(v1.0.2 R2)도 함께 리셋 — 같은 패키지의
		// 다른 테스트가 스탬프한 시각이 이 테스트의 DB 게이트 판정을
		// 우연히 덮어쓰지 않게 한다.
		pruneGateCheckedAt.Store(0)
	}
	cleanup()
	t.Cleanup(cleanup)

	// 20일 전 행을 직접 심는다 — write 없이 read만 하던 세션의 잔여 재현.
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

	// read 1회 — 게이트 열림(키 부재) → 훅이 14일 창 밖 행을 정리한다.
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

	// 게이트 닫힘(직전 정리 성공 — 24h 이내 스탬프): 다시 심은 과거 행은
	// 정리되지 않고 살아남는다. 캐시를 리셋하고 DB 게이트(24h 스탬프)의
	// 닫힘을 직접 검증한다 — 프로세스 캐시 스킵과 결과가 같아 우연
	// 통과가 아니어야 한다.
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
