// profile_v0350_test.go — v0.35.0 프로파일 판정: 추천 규칙·근거·전환·next 표기.
package cli

import (
	"context"
	"strings"
	"testing"
	"time"
)

// profFake — 판정 경로 fake: pending_max 질의로 표본 제어.
func profFake(events int, recallLed int) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil // 기본 임계(zero_near_min_read 등)
		}
		if strings.Contains(sql, "exposure_profile") {
			return nil, nil // 기본 normal
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

// TestProfileRecommendsBySample — 표본 부족은 보류(유지)·충족 시 판정.
func TestProfileRecommendsBySample(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	// 표본 0 → 보류(유지) — next 없음.
	var out strings.Builder
	if code := runProfile(context.Background(), profFake(0, 0), nil, nil, &out); code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"recommend":"유지"`) || strings.Contains(out.String(), `"next"`) {
		t.Errorf("표본 부족 판정: %s", out.String())
	}
	// 표본 충족·회상 선도 0 → minimal 추천 + next(전환 필요).
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

// TestProfileApplyInvalidRejected — 무효 값 exit 1.
// v1.0.1(F3): 무효 --apply 값·unknown flag는 무효 인자 exit 3(setup
// --exposure와 정합).
func TestProfileApplyInvalidRejected(t *testing.T) {
	var out strings.Builder
	if code := runProfile(context.Background(), profFake(0, 0), []string{"--apply", "bogus"}, nil, &out); code != 3 {
		t.Errorf("무효 apply exit=%d: %s", code, out.String())
	}
	if code := runProfile(context.Background(), profFake(0, 0), []string{"--verbose"}, nil, &out); code != 3 {
		t.Errorf("무효 플래그 exit=%d", code)
	}
}
