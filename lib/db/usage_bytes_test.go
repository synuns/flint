// usage_bytes_test.go — RecordBytes: byte-metering daily increments
// (real-DB slow — skipped in fast mode without FLINT_DSN). Contract:
// only kind=read|hot|brief increments the daily:<kind>_bytes:<date>
// flat key; no last_ time key (reuses the count last_<kind>_at);
// corrupted existing values rewrite from 0; invalid kinds·negatives
// rejected.
package db

import (
	"context"
	"testing"
	"time"
)

// TestRecordBytesDaily — increments (consecutive additions)·corrupt
// rewrite·invalid-kind rejection. e.g. RecordBytes("hot", 1180) →
// daily:hot_bytes:<today> +1180.
func slowRecordBytesDaily(t *testing.T, r *Repo) {
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")

	// Increments — consecutive records of the same kind add up (1180 +
	// 820 = 2000).
	if err := r.RecordBytes(context.Background(), "hot", 1180); err != nil {
		t.Fatalf("RecordBytes(hot,1180): %v", err)
	}
	if err := r.RecordBytes(context.Background(), "hot", 820); err != nil {
		t.Fatalf("RecordBytes(hot,820): %v", err)
	}
	if got := flatDailyValue(t, r, "hot_bytes", today); got != 2000 {
		t.Fatalf("daily:hot_bytes:%s = %d, want 2000(1180+820 증분)", today, got)
	}

	// Different kinds get separate keys — read·brief including 0 bytes
	// (zero-hit denominator consistency).
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

	// No last_ time key — bytes reuse the count metering's
	// last_<kind>_at.
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM meta WHERE key IN ('last_read_bytes_at','last_hot_bytes_at','last_brief_bytes_at')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("last_*_bytes_at 키 %d건 — 바이트 계측은 시각 키 없음(계약)", n)
	}

	// Invalid-kind rejection — a separate verdict from the intake-metering
	// whitelist (read|hot|brief).
	for _, bad := range []string{"write", "hot_bytes", ""} {
		if err := r.RecordBytes(context.Background(), bad, 1); err == nil {
			t.Errorf("무효 kind %q 수용 — 거부 계약 위반", bad)
		}
	}
	// Negative rejection — 0 is valid (zero-hit); a negative would
	// decrease the counter and is invalid.
	if err := r.RecordBytes(context.Background(), "read", -1); err == nil {
		t.Errorf("음수 n 수용 — 거부 계약 위반")
	}
}

// TestRecordBytesCorruptRewrite — a corrupted existing flat value is
// rewritten from 0 (inheriting the bumpDailyFlat contract — any
// non-integer).
func slowRecordBytesCorruptRewrite(t *testing.T, r *Repo) {
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
