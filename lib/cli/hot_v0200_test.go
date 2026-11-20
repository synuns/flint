// hot_v0200_test.go — hot --full records the actually injected knowledge ids into
// hot_events (the dead_pct correction source). The headline path performs no injection,
// so it records nothing (inheriting the hot.go N3 contract).
package cli

import (
	"context"
	"strings"
	"testing"
)

// hotEventRecorder — a fake recording the HotEventRecorder seam (the headline
// fixtures reuse hotProfileQueryFn).
type hotEventRecorder struct {
	*fakeStore
	events []string
}

func (h *hotEventRecorder) RecordHotEvent(ctx context.Context, ids string) error {
	h.events = append(h.events, ids)
	return nil
}

// TestHotFullRecordsExposure — the --full injection path records the surviving
// knowledge id CSV. The record comes from the same id set as instrumentReads.
func TestHotFullRecordsExposure(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{
		{"pending", "토픽", "요약", "2026-10-22", int32(3), nil, nil},
		{"decision", "토픽2", "요약2", "2026-10-21", int32(7), nil, nil},
	}
	st := &hotEventRecorder{fakeStore: &fakeStore{queryFn: hotProfileQueryFn("minimal", hotRows)}}
	var out strings.Builder
	if code := runHot(ctx, st, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit=%d, want 0; out=%s", code, out.String())
	}
	if len(st.events) == 0 {
		t.Fatal("--full 경로가 hot_events를 기록하지 않음")
	}
	// the recorded CSV is a parsable id list (the survivors after injection truncation — the values
	// themselves depend on the budget and survival judgment, so only format and non-emptiness are asserted).
	for _, ev := range st.events {
		if ids := parseIDsCSV(ev); len(ids) == 0 {
			t.Errorf("노출 기록 %q가 id로 파싱 불가", ev)
		}
	}
}

// TestHotHeadlineNoExposureRecord — the headline (counts-only) path performs no
// injection, hence no exposure record.
func TestHotHeadlineNoExposureRecord(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{{"pending", "토픽", "요약", "2026-10-22"}}
	st := &hotEventRecorder{fakeStore: &fakeStore{queryFn: hotProfileQueryFn("normal", hotRows)}}
	var out strings.Builder
	if code := runHot(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot exit=%d, want 0; out=%s", code, out.String())
	}
	if len(st.events) != 0 {
		t.Errorf("헤드라인 경로 노출 기록 = %v, want 없음(주입 없음)", st.events)
	}
}

// TestHotHeadlineLimitWidened — the headline (counts)
// path uses the widened 50-query cap; the --full injection path keeps the budget cap (10) contract.
func TestHotHeadlineLimitWidened(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{{"pending", "토픽", "요약", "2026-10-22"}}
	st := &hotEventRecorder{fakeStore: &fakeStore{queryFn: hotProfileQueryFn("normal", hotRows)}}
	var out strings.Builder
	if code := runHot(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot exit=%d, want 0; out=%s", code, out.String())
	}
	found := false
	for _, q := range st.queries {
		if strings.Contains(q, "FROM hot_view") {
			found = true
			if !strings.Contains(q, "LIMIT 50") {
				t.Errorf("헤드라인 쿼리 상한 = %q, want LIMIT 50(카운트 왜곡 방지)", q)
			}
		}
	}
	if !found {
		t.Fatal("hot_view 조회 미관측")
	}

	st2 := &hotEventRecorder{fakeStore: &fakeStore{queryFn: hotProfileQueryFn("normal", hotRows)}}
	var out2 strings.Builder
	if code := runHot(ctx, st2, []string{"--full"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("runHot --full exit=%d, want 0; out=%s", code, out2.String())
	}
	for _, q := range st2.queries {
		if strings.Contains(q, "FROM hot_view") && strings.Contains(q, "LIMIT 50") {
			t.Errorf("--full 주입 경로가 헤드라인 상한 사용: %q(예산 계약 위반)", q)
		}
	}
}
