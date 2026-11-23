package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

func thresholdsQueryFn(kv [][]any, fallback func(string, []any) ([][]any, error)) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM meta WHERE key IN") {
			return kv, nil
		}
		return fallback(sql, args)
	}
}

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

func TestDoctorZeroNearWarnPctOverride(t *testing.T) {
	f := &fakeStore{queryFn: thresholdsQueryFn(
		[][]any{{"zero_near_warn_pct", "5"}},
		func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "daily:") {

				return [][]any{{"daily:" + time.Now().UTC().Format("2006-01-02"), `{"read":20,"read_zero_near":2}`}}, nil
			}
			return nil, nil
		})}
	c := checkZeroNear(context.Background(), f)
	if c.Status != "warn" {
		t.Errorf("오버라이드 5%% 초과 10%% = %s, want warn(기본 30 잔존 의심): %q", c.Status, c.Detail)
	}
}

func TestDoctorZeroNearMinReadOverride(t *testing.T) {
	f := &fakeStore{queryFn: thresholdsQueryFn(
		[][]any{{"zero_near_min_read", "100"}},
		func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "daily:") {

				return [][]any{{"daily:" + time.Now().UTC().Format("2006-01-02"), `{"read":20,"read_zero_near":2}`}}, nil
			}
			return nil, nil
		})}
	c := checkZeroNear(context.Background(), f)
	if c.Status != "ok" || !strings.Contains(c.Detail, "insufficient samples") {
		t.Errorf("오버라이드 표본 하한 100 = %s(%q), want ok+표본 부족", c.Status, c.Detail)
	}
}

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
