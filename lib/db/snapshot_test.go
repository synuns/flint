package db

import (
	"context"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

func resetSnapshotGateCache(t *testing.T) {
	t.Helper()
	saved := snapshotGateCheckedAt.Load()
	snapshotGateCheckedAt.Store(0)
	t.Cleanup(func() { snapshotGateCheckedAt.Store(saved) })
}

func setSnapshotGate(t *testing.T, r *Repo, at time.Time) {
	t.Helper()
	if at.IsZero() {
		if _, err := r.pool.Exec(context.Background(),
			`DELETE FROM meta WHERE key = $1`, thresholdSnapshotGateKey); err != nil {
			t.Fatalf("게이트 키 삭제: %v", err)
		}
		return
	}
	if _, err := r.pool.Exec(context.Background(),
		`INSERT INTO meta (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		thresholdSnapshotGateKey, at.Format(time.RFC3339)); err != nil {
		t.Fatalf("게이트 키 설정: %v", err)
	}
}

func snapshotRows(t *testing.T, r *Repo) []struct{ key, oldV, newV string } {
	t.Helper()
	rows, err := r.pool.Query(context.Background(),
		`SELECT key, old_value, new_value FROM threshold_snapshots ORDER BY ts ASC, key ASC`)
	if err != nil {
		t.Fatalf("스냅숏 조회: %v", err)
	}
	defer rows.Close()
	var out []struct{ key, oldV, newV string }
	for rows.Next() {
		var s struct{ key, oldV, newV string }
		if err := rows.Scan(&s.key, &s.oldV, &s.newV); err != nil {
			t.Fatalf("스냅숏 scan: %v", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("스냅숏 rows: %v", err)
	}
	return out
}

func TestSnapshotGateDue(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		last string
		want bool
	}{
		{"미기록 — 만료", "", true},
		{"빈칸 — 만료", "  ", true},
		{"비RFC3339 — 만료", "yesterday", true},
		{"23h 경과 — 미만료", now.Add(-23 * time.Hour).Format(time.RFC3339), false},
		{"25h 경과 — 만료", now.Add(-25 * time.Hour).Format(time.RFC3339), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := snapshotGateDue(tc.last, now); got != tc.want {
				t.Fatalf("snapshotGateDue(%q) = %v, want %v", tc.last, got, tc.want)
			}
		})
	}
}

func TestSnapshotGateProbe(t *testing.T) {
	resetSnapshotGateCache(t)
	now := time.Now().UTC()
	if !snapshotGateProbe(now) {
		t.Fatal("첫 probe = false, want true(미확인 상태)")
	}
	if snapshotGateProbe(now.Add(30 * time.Minute)) {
		t.Fatal("TTL 내 재probe = true, want false(1h 프로세스 캐시)")
	}
	if !snapshotGateProbe(now.Add(2 * time.Hour)) {
		t.Fatal("TTL 경과 후 probe = false, want true(재평가)")
	}
}

func TestThresholdSnapshotSeeding(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	resetSnapshotGateCache(t)

	if err := r.RecordReadEvent(ctx, ReadEvent{Hits: 1, Zero: false, Near: false}); err != nil {
		t.Fatalf("RecordReadEvent: %v", err)
	}
	keys := wire.ThresholdsKeys()
	got := snapshotRows(t, r)
	if len(got) != len(keys) {
		t.Fatalf("최초 시딩 %d행, want %d행(전체 키 — 레지스트리 전수)", len(got), len(keys))
	}
	byKey := map[string]string{}
	for _, row := range got {
		if row.oldV != "" {
			t.Fatalf("기저선 행 %s old_value=%q, want 빈(이전 없음)", row.key, row.oldV)
		}
		byKey[row.key] = row.newV
	}

	if byKey["pending_max"] != "50" {
		t.Fatalf("시딩 pending_max=%q, want \"50\"(미설정 키 기본값 표기)", byKey["pending_max"])
	}

	var gate string
	if err := r.pool.QueryRow(ctx,
		`SELECT value FROM meta WHERE key = $1`, thresholdSnapshotGateKey).Scan(&gate); err != nil {
		t.Fatalf("게이트 키 조회: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, gate); err != nil {
		t.Fatalf("게이트 스탬프 %q — RFC3339 아님", gate)
	}

	resetSnapshotGateCache(t)
	r.maybeSnapshotThresholds(ctx)
	if n := len(snapshotRows(t, r)); n != len(keys) {
		t.Fatalf("게이트 미만료 경과 행 수 %d, want %d(불변)", n, len(keys))
	}

	setSnapshotGate(t, r, time.Now().UTC().Add(-25*time.Hour))
	resetSnapshotGateCache(t)
	r.maybeSnapshotThresholds(ctx)
	if n := len(snapshotRows(t, r)); n != len(keys) {
		t.Fatalf("무변경 경과 후 %d행, want %d(0행 append)", n, len(keys))
	}

	if _, err := r.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('pending_max', '60')
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
		t.Fatalf("pending_max 오버라이드: %v", err)
	}
	setSnapshotGate(t, r, time.Now().UTC().Add(-25*time.Hour))
	resetSnapshotGateCache(t)
	r.maybeSnapshotThresholds(ctx)
	got = snapshotRows(t, r)
	if len(got) != len(keys)+1 {
		t.Fatalf("변경 경과 후 %d행, want %d(변경 1행만 append)", len(got), len(keys)+1)
	}
	last := got[len(got)-1]
	if last.key != "pending_max" || last.oldV != "50" || last.newV != "60" {
		t.Fatalf("변경 행 = %v, want pending_max 50→60", last)
	}

	setSnapshotGate(t, r, time.Now().UTC().Add(-25*time.Hour))
	resetSnapshotGateCache(t)
	r.maybeSnapshotThresholds(ctx)
	if n := len(snapshotRows(t, r)); n != len(keys)+1 {
		t.Fatalf("재경과 후 %d행, want %d(멱등)", n, len(keys)+1)
	}
}

func TestThresholdSnapshotRetention(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	resetSnapshotGateCache(t)
	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO threshold_snapshots (ts, key, old_value, new_value)
		 VALUES ($1, 'pending_max', '10', '20')`, old); err != nil {
		t.Fatalf("만료 행 적립: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO threshold_snapshots (ts, key, old_value, new_value)
		 VALUES (now(), 'dup_susp_max', '1', '2')`); err != nil {
		t.Fatalf("최근 행 적립: %v", err)
	}

	r.maybeSnapshotThresholds(ctx)
	var expired int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM threshold_snapshots WHERE key = 'pending_max' AND old_value = '10'`).
		Scan(&expired); err != nil {
		t.Fatalf("만료 행 카운트: %v", err)
	}
	if expired != 0 {
		t.Fatalf("만료 행 잔존 %d건, want 0(retention 삭제)", expired)
	}
	var fresh int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM threshold_snapshots WHERE key = 'dup_susp_max' AND old_value = '1'`).
		Scan(&fresh); err != nil {
		t.Fatalf("최근 행 카운트: %v", err)
	}
	if fresh != 1 {
		t.Fatalf("최근 행 = %d건, want 1(보존)", fresh)
	}
}

func TestThresholdSnapshotQuietFailure(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	resetSnapshotGateCache(t)
	if _, err := r.pool.Exec(ctx, `DROP TABLE threshold_snapshots`); err != nil {
		t.Fatalf("테이블 삭제: %v", err)
	}
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("패닉 발생: %v", rec)
		}
	}()
	r.maybeSnapshotThresholds(ctx)
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM meta WHERE key = $1 AND value <> ''`, thresholdSnapshotGateKey).
		Scan(&n); err != nil {
		t.Fatalf("게이트 키 조회: %v", err)
	}
	if n != 0 {
		t.Fatal("실패 후 게이트 스탬프 — 재시도 하루 차단(stamp-then-fail 위반)")
	}
}
