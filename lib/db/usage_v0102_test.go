// usage_v0102_test.go — v0.10.2 R2 RecordUsageCount 소스별 dup 키: 화이트리스트에
// dup_hook·dup_manual·dup_mcp 추가(additive — 총계 "dup" 불변). 무효 kind 거부는
// 기존대로. 실DB slow — fast에서는 FLINT_DSN 부재로 스킵.
package db

import (
	"context"
	"testing"
	"time"
)

// TestRecordUsageCountDupSources — 신규 3종 kind가 daily 카운터에 각 키로
// 증분되고(룰링: 화이트리스트 추가만), 무효 kind는 여전히 거부된다.
func TestRecordUsageCountDupSources(t *testing.T) {
	r := newTestRepo(t)
	cleanUsageKeys(t, r)
	today := time.Now().UTC().Format("2006-01-02")

	for kind, n := range map[string]int{"dup_hook": 2, "dup_manual": 3, "dup_mcp": 1} {
		if err := r.RecordUsageCount(context.Background(), kind, "", n); err != nil {
			t.Fatalf("RecordUsageCount(%s,%d): %v", kind, n, err)
		}
	}
	got := dailyValue(t, r, today)
	want := map[string]int{"dup_hook": 2, "dup_manual": 3, "dup_mcp": 1}
	for k, n := range want {
		if got[k] != n {
			t.Fatalf("daily:%s[%q] = %d, want %d (full=%v)", today, k, got[k], n, got)
		}
	}
	// 무효 kind 거부는 기존 계약 그대로(화이트리스트 검증 구조 불변).
	if err := r.RecordUsageCount(context.Background(), "dup_webhook", "", 1); err == nil {
		t.Fatalf("무효 kind dup_webhook 수용 — 기존 거부 계약 위반")
	}
	// dup 계열(총계·소스)은 last_ 시각 키를 기록하지 않는다(총계 dup 계약
	// 승계 — 룰링: last_dup_hook_at 등 미사용 메타 키 누적 방지).
	var n int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM meta WHERE key LIKE 'last_dup%'`).Scan(&n); err != nil {
		t.Fatalf("count last_dup%%: %v", err)
	}
	if n != 0 {
		t.Fatalf("last_dup* 키 %d건 — dup 계열은 시각 키 없음", n)
	}
}
