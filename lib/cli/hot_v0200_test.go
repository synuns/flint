// hot_v0200_test.go — v0.20.0 E: hot --full이 실제 주입된 지식 id를
// hot_events에 노출 기록한다(dead_pct 보정 원천). 헤드라인 경로는
// 주입이 없으므로 기록하지 않는다(hot.go N3 계약 승계).
package cli

import (
	"context"
	"strings"
	"testing"
)

// hotEventRecorder — HotEventRecorder seam을 녹음하는 fake(헤드라인
// 픽스처는 hotProfileQueryFn 재사).
type hotEventRecorder struct {
	*fakeStore
	events []string
}

func (h *hotEventRecorder) RecordHotEvent(ctx context.Context, ids string) error {
	h.events = append(h.events, ids)
	return nil
}

// TestHotFullRecordsExposure — Review Focus: --full 주입 경로가 생존
// 지식 id CSV를 기록한다. 기록은 instrumentReads와 같은 id 집합에서.
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
	// 기록된 CSV는 파싱 가능한 id 목록(주입 절단 후 생존분 — 값 자체는
	// 예산·생존 판정에 의존하므로 형식·비어있지 않음만 단언).
	for _, ev := range st.events {
		if ids := parseIDsCSV(ev); len(ids) == 0 {
			t.Errorf("노출 기록 %q가 id로 파싱 불가", ev)
		}
	}
}

// TestHotHeadlineNoExposureRecord — 헤드라인(카운트만) 경로는 주입이
// 없으니 노출 기록도 없다.
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

// TestHotHeadlineLimitWidened — Review Focus(v0.20.1 F3): 헤드라인(카운트)
// 경로는 상한 50 쿼리를, --full 주입 경로는 예산 상한(10) 계약 불변.
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
