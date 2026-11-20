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
