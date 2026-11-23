package store

import "testing"

func TestThresholdsV0163Defaults(t *testing.T) {
	th := DefaultThresholds()
	want := []struct {
		name string
		got  any
		want any
	}{
		{"SuggestMinScore", th.SuggestMinScore, 0.15},
		{"SuggestScanCap", th.SuggestScanCap, 5000},
		{"ConflictDupThreshold", th.ConflictDupThreshold, 0.6},
		{"BackupAgeWarnH", th.BackupAgeWarnH, 72},
		{"ZeroNearMinRead", th.ZeroNearMinRead, 10},
		{"ZeroNearWarnPct", th.ZeroNearWarnPct, 30},
		{"PendingAgeDays", th.PendingAgeDays, 7},
	}
	for _, w := range want {
		if w.got != w.want {
			t.Errorf("%s = %v, want %v", w.name, w.got, w.want)
		}
	}
}

func TestThresholdsV0163ParseOverride(t *testing.T) {
	th, err := ParseThresholds([][2]any{
		{"suggest_min_score", "0.4"},
		{"suggest_scan_cap", "300"},
		{"conflict_dup_threshold", "0.9"},
		{"backup_age_warn_h", "24"},
		{"zero_near_min_read", "50"},
		{"zero_near_warn_pct", "10"},
		{"pending_age_days", "3"},
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if th.SuggestMinScore != 0.4 || th.ConflictDupThreshold != 0.9 {
		t.Errorf("float 오버라이드 미반영: %+v", th)
	}
	if th.SuggestScanCap != 300 || th.BackupAgeWarnH != 24 ||
		th.ZeroNearMinRead != 50 || th.ZeroNearWarnPct != 10 || th.PendingAgeDays != 3 {
		t.Errorf("int 오버라이드 미반영: %+v", th)
	}
}

func TestThresholdsV0163InvalidFallback(t *testing.T) {
	th, err := ParseThresholds([][2]any{
		{"suggest_min_score", "abc"},
		{"suggest_scan_cap", "-1"},
		{"conflict_dup_threshold", "1.5"},
		{"backup_age_warn_h", "x"},
		{"zero_near_min_read", "-5"},
		{"zero_near_warn_pct", "0.7"},
		{"pending_age_days", ""},
	})
	if err != nil {
		t.Fatalf("무효값은 에러가 아니다: %v", err)
	}
	if th.SuggestMinScore != 0.15 || th.ConflictDupThreshold != 0.6 {
		t.Errorf("float 폴백 실패: %+v", th)
	}
	if th.SuggestScanCap != 5000 || th.BackupAgeWarnH != 72 ||
		th.ZeroNearMinRead != 10 || th.ZeroNearWarnPct != 30 || th.PendingAgeDays != 7 {
		t.Errorf("int 폴백 실패: %+v", th)
	}
}

func TestThresholdsV0163UnsetStaysZero(t *testing.T) {
	th, err := ParseThresholds(nil)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if th.SuggestMinScore != 0 || th.SuggestScanCap != 0 || th.ConflictDupThreshold != 0 ||
		th.BackupAgeWarnH != 0 || th.ZeroNearMinRead != 0 ||
		th.ZeroNearWarnPct != 0 || th.PendingAgeDays != 0 {
		t.Errorf("미설정 신규 키는 0이어야 함: %+v", th)
	}
}
