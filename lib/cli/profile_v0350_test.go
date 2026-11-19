// profile_v0350_test.go — profile determination: recommendation rules, evidence, switching, and next notation.
package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

// profFake — a determination-path fake: controls the sample via the pending_max query.
func profFake(events int, recallLed int) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil // default thresholds (zero_near_min_read etc.)
		}
		if strings.Contains(sql, "exposure_profile") {
			return nil, nil // default normal
		}
		if strings.Contains(sql, "FROM read_events") {
			rows := make([][]any, events)
			for i := range rows {
				rows[i] = []any{time.Now().Add(-24 * time.Hour), false, false, "", 0}
			}
			return rows, nil
		}
		if strings.Contains(sql, "FROM session_starts") {
			starts := recallLed + 1
			out := make([][]any, starts)
			for i := range out {
				out[i] = []any{time.Now().Add(time.Duration(-(starts - i)) * time.Hour)}
			}
			return out, nil
		}
		if strings.Contains(sql, "FROM knowledge") {
			return nil, nil
		}
		return nil, nil
	}}
}

// TestProfileRecommendsBySample — insufficient sample defers (keep); sufficient sample determines.
func TestProfileRecommendsBySample(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	// sample 0 -> defers (keep) — no next.
	var out strings.Builder
	if code := runProfile(context.Background(), profFake(0, 0), nil, nil, &out); code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"recommend":"keep"`) || strings.Contains(out.String(), `"next"`) {
		t.Errorf("insufficient-sample verdict: %s", out.String())
	}
	// sample met + recall lead 0 -> recommends minimal + next (switch needed).
	out.Reset()
	if code := runProfile(context.Background(), profFake(20, 0), nil, nil, &out); code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	s := out.String()
	if !strings.Contains(s, `"recommend":"minimal"`) || !strings.Contains(s, `"next":"flint profile --apply minimal"`) {
		t.Errorf("minimal 추천+next: %s", s)
	}
	if !strings.Contains(s, `"evidence"`) || !strings.Contains(s, `"read_events_n":20`) {
		t.Errorf("근거 지표 누락: %s", s)
	}
}

// TestProfileApplyInvalidRejected — invalid value exits 1.
// Invalid --apply values and unknown flags are invalid-arg exit 3
// (consistent with setup --exposure).
func TestProfileApplyInvalidRejected(t *testing.T) {
	var out strings.Builder
	if code := runProfile(context.Background(), profFake(0, 0), []string{"--apply", "bogus"}, nil, &out); code != 3 {
		t.Errorf("무효 apply exit=%d: %s", code, out.String())
	}
	if code := runProfile(context.Background(), profFake(0, 0), []string{"--verbose"}, nil, &out); code != 3 {
		t.Errorf("무효 플래그 exit=%d", code)
	}
}
