// usage_v0341_test.go — v0.34.1 RecordUsageCount 화이트리스트에 "brief"
// 추가(additive). lib/cli/brief.go의 recordUsage(ctx, st, "brief", "")가
// invalid kind로 거부되어 brief 유입 계측이 기록되지 않던 결함의 회귀 테스트.
// 실DB slow — fast에서는 FLINT_DSN 부재로 스킵.
package db

import (
	"context"
	"testing"
	"time"
)

// TestRecordUsageCountBrief — kind "brief"가 daily 카운터에 증분되고
// last_brief_at이 기록된다(dup 계열이 아니므로 시각 키 계약 승계).
// 무효 kind 거부는 기존 계약 그대로.
func TestRecordUsageCountBrief(t *testing.T) {
	r := newTestRepo(t)
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
	// 무효 kind 거부는 기존 계약 그대로(화이트리스트 검증 구조 불변).
	if err := r.RecordUsageCount(context.Background(), "briefing", "", 1); err == nil {
		t.Fatalf("무효 kind briefing 수용 — 기존 거부 계약 위반")
	}
}
