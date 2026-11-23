package store

import (
	"math"
	"testing"
)

func TestThresholdsDefaults(t *testing.T) {
	got, err := ParseThresholds(nil)
	if err != nil {
		t.Fatalf("ParseThresholds(nil) 에러: %v", err)
	}
	want := Thresholds{PendingMax: 50, DupSuspMax: 20, ConfFloor: 0.8, StaleSessions: 3}
	if got != want {
		t.Errorf("ParseThresholds(nil) = %+v, want %+v", got, want)
	}
}

func TestThresholdsPartialSet(t *testing.T) {
	got, err := ParseThresholds([][2]any{
		{"pending_max", "50"},
		{"conf_floor", "0.8"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds 에러: %v", err)
	}
	want := Thresholds{PendingMax: 50, DupSuspMax: 20, ConfFloor: 0.8, StaleSessions: 3}
	if got != want {
		t.Errorf("ParseThresholds = %+v, want %+v", got, want)
	}
}

func TestThresholdsAllSet(t *testing.T) {
	got, err := ParseThresholds([][2]any{
		{"pending_max", "10"},
		{"dup_susp_max", "5"},
		{"conf_floor", "0.5"},
		{"stale_sessions", "7"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds 에러: %v", err)
	}
	want := Thresholds{PendingMax: 10, DupSuspMax: 5, ConfFloor: 0.5, StaleSessions: 7}
	if got != want {
		t.Errorf("ParseThresholds = %+v, want %+v", got, want)
	}
}

func TestThresholdsConfFloorBoundaries(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want float64
	}{
		{"zero", "0", 0},
		{"one", "1", 1},
		{"string", "high", 0.8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseThresholds([][2]any{{"conf_floor", tc.val}})
			if err != nil {
				t.Fatalf("ParseThresholds(%s) 에러: %v", tc.name, err)
			}
			if math.Abs(got.ConfFloor-tc.want) > 1e-9 {
				t.Errorf("conf_floor %v = %v, want %v", tc.val, got.ConfFloor, tc.want)
			}
		})
	}
}

func TestThresholdsInvalidFallback(t *testing.T) {
	got, err := ParseThresholds([][2]any{
		{"pending_max", "abc"},
		{"dup_susp_max", -1},
		{"stale_sessions", ""},
	})
	if err != nil {
		t.Fatalf("잘못된 값에도 에러 반환하면 안 됨(폴백 원칙): %v", err)
	}
	if got.PendingMax != 50 {
		t.Errorf("pending_max 폴백 = %d, want 50", got.PendingMax)
	}
	if got.DupSuspMax != 20 {
		t.Errorf("dup_susp_max 폴백 = %d, want 20", got.DupSuspMax)
	}
	if got.StaleSessions != 3 {
		t.Errorf("stale_sessions 폴백 = %d, want 3", got.StaleSessions)
	}
}

func TestThresholdsNonStringValues(t *testing.T) {
	got, err := ParseThresholds([][2]any{
		{"pending_max", 30},
		{"conf_floor", 0.9},
	})
	if err != nil {
		t.Fatalf("ParseThresholds 에러: %v", err)
	}
	if got.PendingMax != 30 {
		t.Errorf("pending_max = %d, want 30", got.PendingMax)
	}
	if math.Abs(got.ConfFloor-0.9) > 1e-9 {
		t.Errorf("conf_floor = %v, want 0.9", got.ConfFloor)
	}
}

func TestThresholdsUnknownKeyIgnored(t *testing.T) {
	got, err := ParseThresholds([][2]any{{"unknown_key", "999"}})
	if err != nil {
		t.Fatalf("ParseThresholds 에러: %v", err)
	}
	if got.PendingMax != 50 || got.DupSuspMax != 20 || got.StaleSessions != 3 {
		t.Errorf("unknown 키가 폴백 영향: %+v", got)
	}
}

func TestVaultThresholdKeys(t *testing.T) {

	def := DefaultThresholds()
	if def.VaultScanMaxFiles != 10000 {
		t.Errorf("VaultScanMaxFiles 기본값=%d, want 10000", def.VaultScanMaxFiles)
	}
	if def.VaultSummaryMaxRunes != 200 {
		t.Errorf("VaultSummaryMaxRunes 기본값=%d, want 200", def.VaultSummaryMaxRunes)
	}
	if def.VaultSkipDirs != ".obsidian,.trash" {
		t.Errorf("VaultSkipDirs 기본값=%q, want \".obsidian,.trash\"", def.VaultSkipDirs)
	}

	th, err := ParseThresholds([][2]any{
		{"vault_scan_max_files", "500"},
		{"vault_summary_max_runes", "80"},
		{"vault_skip_dirs", ".git,node_modules"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.VaultScanMaxFiles != 500 {
		t.Errorf("vault_scan_max_files 오버라이드=%d, want 500", th.VaultScanMaxFiles)
	}
	if th.VaultSummaryMaxRunes != 80 {
		t.Errorf("vault_summary_max_runes 오버라이드=%d, want 80", th.VaultSummaryMaxRunes)
	}
	if th.VaultSkipDirs != ".git,node_modules" {
		t.Errorf("vault_skip_dirs 오버라이드=%q, want \".git,node_modules\"", th.VaultSkipDirs)
	}

	bad, err := ParseThresholds([][2]any{
		{"vault_scan_max_files", "abc"},
		{"vault_summary_max_runes", "-1"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if bad.VaultScanMaxFiles != 10000 || bad.VaultSummaryMaxRunes != 200 {
		t.Errorf("무효값 폴백: scan=%d runes=%d, want 10000·200",
			bad.VaultScanMaxFiles, bad.VaultSummaryMaxRunes)
	}

	mism, err := ParseThresholds([][2]any{{"vault_skip_dirs", true}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if mism.VaultSkipDirs != ".obsidian,.trash" {
		t.Errorf("타입 불일치 폴백=%q, want \".obsidian,.trash\"", mism.VaultSkipDirs)
	}

	unset, err := ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.VaultScanMaxFiles != 0 || unset.VaultSummaryMaxRunes != 0 || unset.VaultSkipDirs != "" {
		t.Errorf("unset 폴백: scan=%d runes=%d skip=%q, want 0·0·\"\"",
			unset.VaultScanMaxFiles, unset.VaultSummaryMaxRunes, unset.VaultSkipDirs)
	}
}

func TestDistillThresholdKeys(t *testing.T) {

	def := DefaultThresholds()
	if def.DistillMinScore != 1.0 {
		t.Errorf("DistillMinScore 기본값=%v, want 1.0", def.DistillMinScore)
	}

	th, err := ParseThresholds([][2]any{
		{"distill_min_score", "5.5"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.DistillMinScore != 5.5 {
		t.Errorf("distill_min_score 오버라이드=%v, want 5.5", th.DistillMinScore)
	}

	neg, err := ParseThresholds([][2]any{
		{"distill_min_score", "-0.5"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if neg.DistillMinScore != 1.0 {
		t.Errorf("음수 거부=%v, want 1.0(폴백)", neg.DistillMinScore)
	}

	unset, err := ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.DistillMinScore != 0 {
		t.Errorf("unset 폴백: distill_min_score=%v, want 0", unset.DistillMinScore)
	}
}

func TestDistillMaxRunesKeys(t *testing.T) {

	def := DefaultThresholds()
	if def.DistillMaxRunes != 20000 {
		t.Errorf("DistillMaxRunes 기본값=%d, want 20000", def.DistillMaxRunes)
	}

	th, err := ParseThresholds([][2]any{
		{"distill_max_runes", "1500"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.DistillMaxRunes != 1500 {
		t.Errorf("distill_max_runes 오버라이드=%d, want 1500", th.DistillMaxRunes)
	}

	neg, err := ParseThresholds([][2]any{
		{"distill_max_runes", "-5"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if neg.DistillMaxRunes != 20000 {
		t.Errorf("음수 거부=%d, want 20000(폴백)", neg.DistillMaxRunes)
	}

	unset, err := ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.DistillMaxRunes != 0 {
		t.Errorf("unset 폴백: distill_max_runes=%d, want 0", unset.DistillMaxRunes)
	}
}

func TestRecallAuditThresholdKeys(t *testing.T) {

	def := DefaultThresholds()
	if def.RecallAuditMinBad != 2 {
		t.Errorf("RecallAuditMinBad 기본값=%d, want 2", def.RecallAuditMinBad)
	}

	th, err := ParseThresholds([][2]any{
		{"recall_audit_min_bad", "4"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.RecallAuditMinBad != 4 {
		t.Errorf("recall_audit_min_bad 오버라이드=%d, want 4", th.RecallAuditMinBad)
	}

	neg, err := ParseThresholds([][2]any{
		{"recall_audit_min_bad", "-1"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if neg.RecallAuditMinBad != 2 {
		t.Errorf("음수 거부=%d, want 2(폴백)", neg.RecallAuditMinBad)
	}

	unset, err := ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.RecallAuditMinBad != 0 {
		t.Errorf("unset 폴백: recall_audit_min_bad=%d, want 0", unset.RecallAuditMinBad)
	}
}

func TestEngineWarnThresholdKeys(t *testing.T) {
	def := DefaultThresholds()
	if def.DoltLogDepthWarn != 10000 {
		t.Errorf("DoltLogDepthWarn 기본값=%d, want 10000", def.DoltLogDepthWarn)
	}
	if def.DbSizeWarnMB != 2048 {
		t.Errorf("DbSizeWarnMB 기본값=%d, want 2048", def.DbSizeWarnMB)
	}
	th, err := ParseThresholds([][2]any{
		{"dolt_" + "log_depth_warn", "5000"},
		{"db_size_warn_mb", "1024"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.DoltLogDepthWarn != 5000 {
		t.Errorf("depth warn 오버라이드=%d, want 5000", th.DoltLogDepthWarn)
	}
	if th.DbSizeWarnMB != 1024 {
		t.Errorf("db_size_warn_mb 오버라이드=%d, want 1024", th.DbSizeWarnMB)
	}

	unset, err := ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.DoltLogDepthWarn != 0 || unset.DbSizeWarnMB != 0 {
		t.Errorf("unset 폴백: depth=%d size=%d, want 0·0",
			unset.DoltLogDepthWarn, unset.DbSizeWarnMB)
	}
}
