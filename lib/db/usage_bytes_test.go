// usage_bytes_test.go — v1.1.0 T1 RecordBytes: G5 바이트 계측 daily 증분
// (실DB slow — fast에서는 FLINT_DSN 부재로 스킵). 계약: kind=read|hot|brief
// 만 daily:<kind>_bytes:<date> 플랫 키 증분(v1.2.0 T1 플랫 전환), last_ 시각
// 키 없음(횟수 last_<kind>_at 준용), 파손 기존 값은 0에서 재기작, 무효
// kind·음수 거부.
package db

import (
	"context"
	"testing"
	"time"
)

// TestRecordBytesDaily — Review Focus: 증분(연속 가산)·파손 재기작·무효 kind
// 거부. e.g. RecordBytes("hot", 1180) → daily:hot_bytes:<오늘> +1180.
func TestRecordBytesDaily(t *testing.T) {
	r := newTestRepo(t)
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")

	// 증분 — 같은 kind 연속 기록은 가산된다(1180 + 820 = 2000).
	if err := r.RecordBytes(context.Background(), "hot", 1180); err != nil {
		t.Fatalf("RecordBytes(hot,1180): %v", err)
	}
	if err := r.RecordBytes(context.Background(), "hot", 820); err != nil {
		t.Fatalf("RecordBytes(hot,820): %v", err)
	}
	if got := flatDailyValue(t, r, "hot_bytes", today); got != 2000 {
		t.Fatalf("daily:hot_bytes:%s = %d, want 2000(1180+820 증분)", today, got)
	}

	// 다른 kind는 별도 키 — read·brief 0바이트 포함(제로히트 분모 정합).
	if err := r.RecordBytes(context.Background(), "read", 0); err != nil {
		t.Fatalf("RecordBytes(read,0): %v — 0바이트도 기록(호출 수 분모 정합)", err)
	}
	if err := r.RecordBytes(context.Background(), "brief", 42); err != nil {
		t.Fatalf("RecordBytes(brief,42): %v", err)
	}
	if got := flatDailyValue(t, r, "read_bytes", today); got != 0 {
		t.Fatalf("daily:read_bytes:%s = %d, want 0(제로히트 기록)", today, got)
	}
	if got := flatDailyValue(t, r, "brief_bytes", today); got != 42 {
		t.Fatalf("daily:brief_bytes:%s = %d, want 42(키 kind별 분리)", today, got)
	}

	// last_ 시각 키 없음 — 바이트는 횟수 계측의 last_<kind>_at을 준용한다.
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM meta WHERE key IN ('last_read_bytes_at','last_hot_bytes_at','last_brief_bytes_at')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("last_*_bytes_at 키 %d건 — 바이트 계측은 시각 키 없음(계약)", n)
	}

	// 무효 kind 거부 — 유입 계측 화이트리스트(read|hot|brief)와 별도 판정.
	for _, bad := range []string{"write", "hot_bytes", ""} {
		if err := r.RecordBytes(context.Background(), bad, 1); err == nil {
			t.Errorf("무효 kind %q 수용 — 거부 계약 위반", bad)
		}
	}
	// 음수 거부 — 0은 유효(제로히트), 음수는 카운터 감소라 무효.
	if err := r.RecordBytes(context.Background(), "read", -1); err == nil {
		t.Errorf("음수 n 수용 — 거부 계약 위반")
	}
}

// TestRecordBytesCorruptRewrite — 파손된 기존 플랫 값은 0에서 재기작한다
// (bumpDailyFlat 계약 승계 — non-정수 전부).
func TestRecordBytesCorruptRewrite(t *testing.T) {
	r := newTestRepo(t)
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")

	if _, err := r.pool.Exec(context.Background(),
		`INSERT INTO meta (key, value) VALUES ($1, $2)`,
		"daily:brief_bytes:"+today, "{oops not json"); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordBytes(context.Background(), "brief", 7); err != nil {
		t.Fatalf("RecordBytes(brief,7) 파손 키 위에서: %v", err)
	}
	if got := flatDailyValue(t, r, "brief_bytes", today); got != 7 {
		t.Fatalf("파손 재기작 후 brief_bytes = %d, want 7(0에서 재기작)", got)
	}
}
