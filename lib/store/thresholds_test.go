package store

import (
	"math"
	"testing"
)

// TestThresholdsDefaults — unset keys give the defaults 50, 20, 0.8, 3.
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

// TestThresholdsPartialSet — with only some keys set, the rest get defaults.
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

// TestThresholdsAllSet — every key's set value is reflected.
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

// TestThresholdsConfFloorBoundaries — conf_floor 0, 1, and string rejection give defaults.
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

// TestThresholdsInvalidFallback — an integer key's invalid value gives defaults + a warning (not an error).
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

// TestThresholdsNonStringValues — values arriving as numeric types still parse.
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

// TestThresholdsUnknownKeyIgnored — unknown keys are ignored.
func TestThresholdsUnknownKeyIgnored(t *testing.T) {
	got, err := ParseThresholds([][2]any{{"unknown_key", "999"}})
	if err != nil {
		t.Fatalf("ParseThresholds 에러: %v", err)
	}
	if got.PendingMax != 50 || got.DupSuspMax != 20 || got.StaleSessions != 3 {
		t.Errorf("unknown 키가 폴백 영향: %+v", got)
	}
}

// TestVaultThresholdKeys — the v1.21.0 vault-bridge keys: defaults
// (10000 · 200 · ".obsidian,.trash"), meta override reflection, invalid
// value fallback (the parseIntOrDefault family), the non-string type
// fallback of the new parseStringOrDefault helper (vault_skip_dirs is
// the first string-valued key), and the unset-keeps-zero convention
// ("" for the string field — the consumer falls back to
// DefaultThresholds, the unset-keeps-0 analog).
func TestVaultThresholdKeys(t *testing.T) {
	// defaults.
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
	// override.
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
	// invalid numeric values fall back (the parseIntOrDefault family).
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
	// a non-string value type (toString-unsupported) falls back — the
	// parseStringOrDefault contract.
	mism, err := ParseThresholds([][2]any{{"vault_skip_dirs", true}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if mism.VaultSkipDirs != ".obsidian,.trash" {
		t.Errorf("타입 불일치 폴백=%q, want \".obsidian,.trash\"", mism.VaultSkipDirs)
	}
	// unset keeps the zero value (0·0·"" — the consumer fallback basis).
	unset, err := ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.VaultScanMaxFiles != 0 || unset.VaultSummaryMaxRunes != 0 || unset.VaultSkipDirs != "" {
		t.Errorf("unset 폴백: scan=%d runes=%d skip=%q, want 0·0·\"\"",
			unset.VaultScanMaxFiles, unset.VaultSummaryMaxRunes, unset.VaultSkipDirs)
	}
}

// TestDistillThresholdKeys — the v1.22.0 distill key: default 1.0 (a
// raw recall count of at least 1 — the unbounded count-scale score,
// so values above 1 are normal), meta override reflection, negative
// rejection (the non-negative parser — the archive_below convention),
// and the unset-keeps-0 convention (the consumer falls back to
// DefaultThresholds).
func TestDistillThresholdKeys(t *testing.T) {
	// default.
	def := DefaultThresholds()
	if def.DistillMinScore != 1.0 {
		t.Errorf("DistillMinScore 기본값=%v, want 1.0", def.DistillMinScore)
	}
	// override — above 1 is normal (the effective score is a count-scale
	// score, not a 0..1 ratio).
	th, err := ParseThresholds([][2]any{
		{"distill_min_score", "5.5"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.DistillMinScore != 5.5 {
		t.Errorf("distill_min_score 오버라이드=%v, want 5.5", th.DistillMinScore)
	}
	// zero is a stored-legal value but the UNSET sentinel on the consumer
	// side (only --min-score 0 means "no floor" — the v1.22.1 r97-2
	// correction); negatives are rejected and fall back.
	neg, err := ParseThresholds([][2]any{
		{"distill_min_score", "-0.5"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if neg.DistillMinScore != 1.0 {
		t.Errorf("음수 거부=%v, want 1.0(폴백)", neg.DistillMinScore)
	}
	// unset keeps 0 (the consumer fallback basis — DefaultThresholds).
	unset, err := ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.DistillMinScore != 0 {
		t.Errorf("unset 폴백: distill_min_score=%v, want 0", unset.DistillMinScore)
	}
}

// TestDistillMaxRunesKeys — the v1.22.1 r97 distill file-cap key:
// default 20000, meta override reflection, negative rejection, and the
// unset-keeps-0 convention (the consumer falls back to DefaultThresholds
// — 0 cannot be a meaningful cap, so only values > 0 apply).
func TestDistillMaxRunesKeys(t *testing.T) {
	// default.
	def := DefaultThresholds()
	if def.DistillMaxRunes != 20000 {
		t.Errorf("DistillMaxRunes 기본값=%d, want 20000", def.DistillMaxRunes)
	}
	// override.
	th, err := ParseThresholds([][2]any{
		{"distill_max_runes", "1500"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if th.DistillMaxRunes != 1500 {
		t.Errorf("distill_max_runes 오버라이드=%d, want 1500", th.DistillMaxRunes)
	}
	// negatives are rejected and fall back (the parseInt lower-bound 0).
	neg, err := ParseThresholds([][2]any{
		{"distill_max_runes", "-5"},
	})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if neg.DistillMaxRunes != 20000 {
		t.Errorf("음수 거부=%d, want 20000(폴백)", neg.DistillMaxRunes)
	}
	// unset keeps 0 (the consumer fallback basis — DefaultThresholds).
	unset, err := ParseThresholds([][2]any{{"pending_max", "50"}})
	if err != nil {
		t.Fatalf("ParseThresholds err: %v", err)
	}
	if unset.DistillMaxRunes != 0 {
		t.Errorf("unset 폴백: distill_max_runes=%d, want 0", unset.DistillMaxRunes)
	}
}
