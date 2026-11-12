// doctor_v0163_test.go — v0.16.3: doctor 임계값 4종의 meta 오버라이드
// 우선(backup-age·zero-near 2종·pending-age). 패턴은
// TestDoctorLastSeenThresholdOverride(stall_hot_h) 승계.
package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

// thresholdsQueryFn — meta 임계값 키-값 행을 반환하는 queryFn 래퍼.
// 임계값 질의(key IN (...))에만 응답하고 나머지는 fallback으로 넘긴다.
func thresholdsQueryFn(kv [][]any, fallback func(string, []any) ([][]any, error)) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key IN") {
			return kv, nil
		}
		return fallback(sql, args)
	}
}

// backup_age_warn_h=1 오버라이드 — 2h 경과 백업이 warn(기본 72h였다면 ok).
func TestDoctorBackupAgeThresholdOverride(t *testing.T) {
	twoHourOld := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	f := &fakeStore{queryFn: thresholdsQueryFn(
		[][]any{{"backup_age_warn_h", "1"}},
		func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "last_backup") {
				return [][]any{{"last_backup", twoHourOld}}, nil
			}
			return nil, nil
		})}
	c := checkBackupAge(context.Background(), f)
	if c.Status != "warn" {
		t.Errorf("오버라이드 1h 초과 2h 경과 = %s, want warn(기본 72h 잔존 의심): %q", c.Status, c.Detail)
	}
}

// zero_near_warn_pct=5 오버라이드 — 10% 근접비율이 warn(기본 30이면 ok).
func TestDoctorZeroNearWarnPctOverride(t *testing.T) {
	f := &fakeStore{queryFn: thresholdsQueryFn(
		[][]any{{"zero_near_warn_pct", "5"}},
		func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "daily:") {
				// v0.42.0 T2: 창(오늘) 내 키만 합산된다.
				return [][]any{{"daily:" + time.Now().UTC().Format("2006-01-02"), `{"read":20,"read_zero_near":2}`}}, nil
			}
			return nil, nil
		})}
	c := checkZeroNear(context.Background(), f)
	if c.Status != "warn" {
		t.Errorf("오버라이드 5%% 초과 10%% = %s, want warn(기본 30 잔존 의심): %q", c.Status, c.Detail)
	}
}

// zero_near_min_read=100 오버라이드 — read 20 표본이 판정 보류 ok(기본 10이면 판정).
func TestDoctorZeroNearMinReadOverride(t *testing.T) {
	f := &fakeStore{queryFn: thresholdsQueryFn(
		[][]any{{"zero_near_min_read", "100"}},
		func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "daily:") {
				// v0.42.0 T2: 창(오늘) 내 키만 합산된다.
				return [][]any{{"daily:" + time.Now().UTC().Format("2006-01-02"), `{"read":20,"read_zero_near":2}`}}, nil
			}
			return nil, nil
		})}
	c := checkZeroNear(context.Background(), f)
	if c.Status != "ok" || !strings.Contains(c.Detail, "표본 부족") {
		t.Errorf("오버라이드 표본 하한 100 = %s(%q), want ok+표본 부족", c.Status, c.Detail)
	}
}

// pending_age_days=1 오버라이드 — 2일 경과 pending이 warn(기본 7이면 ok).
func TestDoctorPendingAgeThresholdOverride(t *testing.T) {
	twoDayOld := time.Now().Add(-2 * 24 * time.Hour)
	f := &fakeStore{queryFn: thresholdsQueryFn(
		[][]any{{"pending_age_days", "1"}},
		func(sql string, args []any) ([][]any, error) {
			return [][]any{{twoDayOld}}, nil
		})}
	c := checkPendingAge(context.Background(), f)
	if c.Status != "warn" {
		t.Errorf("오버라이드 1일 초과 2일 경과 = %s, want warn(기본 7일 잔존 의심): %q", c.Status, c.Detail)
	}
}
