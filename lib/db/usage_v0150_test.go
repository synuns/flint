// usage_v0150_test.go — v0.15.0 R2 RecordReadUsage: read 1회 + 제로·근접
// 분류 daily 증분·last_read_at 병합 upsert(실DB slow 테스트).
package db

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestRecordReadUsageDaily — zero+near 기록 후 daily 3키(read·read_zero·
// read_zero_near), 이어 일반 read는 read만 +1(플랜 T3 테스트 코드 —
// testPool 헬퍼 미존재로 slow 스위트 풀 획득 패턴 newTestRepo 승계).
func TestRecordReadUsageDaily(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	r := newTestRepo(t)
	pool := r.pool
	ctx := context.Background()
	day := time.Now().UTC().Format("2006-01-02")
	pool.Exec(ctx, `DELETE FROM meta WHERE key IN ($1,'last_read_at')`, "daily:"+day)
	if err := r.RecordReadUsage(ctx, true, true); err != nil {
		t.Fatal(err)
	}
	var v string
	if err := pool.QueryRow(ctx, `SELECT value FROM meta WHERE key=$1`, "daily:"+day).Scan(&v); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"read":1`, `"read_zero":1`, `"read_zero_near":1`} {
		if !strings.Contains(v, want) {
			t.Errorf("%s 누락: %s", want, v)
		}
	}
	if err := r.RecordReadUsage(ctx, false, false); err != nil {
		t.Fatal(err)
	}
	pool.QueryRow(ctx, `SELECT value FROM meta WHERE key=$1`, "daily:"+day).Scan(&v)
	var d map[string]int
	json.Unmarshal([]byte(v), &d)
	if d["read"] != 2 || d["read_zero"] != 1 {
		t.Errorf("증분 부정합: %s", v)
	}
}
