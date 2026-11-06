// report_v0190_test.go — v0.19.0 효과 계측 섹션(docs/METRICS.md §4): GSM
// 지표 산출·시간 창 페어링·셀 변환. fakeStore queryFn 패턴(report_v0140
// 승계).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestPairReadWrites — Review Focus(test_pair_window_boundary): read 뒤
// 정확히 창 끝의 write는 페어, 창 밖은 미페어. writes 입력 순서 무관
// (내부 정렬).
func TestPairReadWrites(t *testing.T) {
	base := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	reads := []time.Time{base, base.Add(2 * time.Hour)}
	writes := []time.Time{
		base.Add(31 * time.Minute),             // read1 창(30분) 밖
		base.Add(2*time.Hour + 30*time.Minute), // read2 창 끝(경계 포함) — 순서 뒤섞임
		base.Add(25 * time.Minute),             // read1 창 내
	}
	got := pairReadWrites(reads, writes, 30*time.Minute)
	if got != 2 {
		t.Errorf("pairs = %d, want 2(경계 포함·창 밖 제외)", got)
	}
	if pairReadWrites(reads, nil, 30*time.Minute) != 0 {
		t.Error("writes 공백 시 pairs ≠ 0")
	}
}

// TestCellToTime — pgx time.Time·RFC3339 문자열 수용, 그 외 불가.
func TestCellToTime(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	if got, ok := cellToTime(now); !ok || !got.Equal(now) {
		t.Errorf("time.Time 변환 실패: %v %v", got, ok)
	}
	if got, ok := cellToTime(now.Format(time.RFC3339)); !ok || !got.Equal(now) {
		t.Errorf("RFC3339 변환 실패: %v %v", got, ok)
	}
	if _, ok := cellToTime(7); ok {
		t.Error("int는 변환 불가여야 함")
	}
}

// TestReportEffectSection — Review Focus(test_effect_section): 효과
// 섹션이 METRICS §4 정의대로 산출된다 — daily 합산(G1·G2)·knowledge 시점
// 집계(G3)·read_events 페어링(G4).
func TestReportEffectSection(t *testing.T) {
	now := time.Now().UTC()
	readA := now.Add(-2 * time.Hour) // 창 내(1일)
	readB := now.Add(-1 * time.Hour) // 이 read 뒤 10분에 write → 페어
	writeAt := readB.Add(10 * time.Minute)
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "IN ('read_stats','hot_stats')"):
			return [][]any{{"read_stats", `{"calls":10,"zero":4}`}}, nil
		case strings.Contains(sql, "daily:"):
			return [][]any{{"daily:" + now.Format("2006-01-02"),
				`{"read":10,"read_zero":4,"read_zero_near":3,"write":8,"dup":6}`}}, nil
		case strings.Contains(sql, "read_count > 0"):
			// G3 시점 집계 — active 100·회상 60·dead 20.
			return [][]any{{int64(100), int64(60), int64(20)}}, nil
		case strings.Contains(sql, "read_events"):
			// readA 폴백 경유·readB 정경로 — fallback_pct=50.
			return [][]any{
				{readA.Format(time.RFC3339), true},
				{readB.Format(time.RFC3339), false},
			}, nil
		case strings.Contains(sql, "last_read_at"):
			return [][]any{{"0", "0"}}, nil
		case strings.Contains(sql, "ingested_at"):
			return [][]any{{writeAt.Format(time.RFC3339)}}, nil
		case strings.Contains(sql, "invalid_at >="):
			return [][]any{{"0"}}, nil
		case strings.Contains(sql, "count"):
			return [][]any{{"0", "0", "0"}}, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := runReport(context.Background(), st, []string{"--days", "1"}, nil, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	var rep struct {
		Effect struct {
			HitPct         int `json:"hit_pct"`
			NearPct        int `json:"near_pct"`
			FallbackPct    int `json:"fallback_pct"`
			DupPrevented   int `json:"dup_prevented"`
			ReusePct       int `json:"reuse_pct"`
			DeadPct        int `json:"dead_pct"`
			ReadEventsN    int `json:"read_events_n"`
			ReadWritePairs int `json:"read_write_pairs"`
			PairPct        int `json:"pair_pct"`
		} `json:"effect"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &rep); err != nil {
		t.Fatalf("report JSON 파싱: %v\n%s", err, out.String())
	}
	e := rep.Effect
	// G1: 1−4/10=60·3/10=30·폴백 1/2=50 · G2: Σdup=6 · G3: 60/100·20/100
	// · G4: 2 read 중 1 페어=50.
	if e.HitPct != 60 || e.NearPct != 30 || e.FallbackPct != 50 ||
		e.DupPrevented != 6 || e.ReusePct != 60 || e.DeadPct != 20 ||
		e.ReadEventsN != 2 || e.ReadWritePairs != 1 || e.PairPct != 50 {
		t.Errorf("effect = %+v, want {60 30 50 6 60 20 2 1 50}", e)
	}
}

// TestReportEffectZeroReads — Review Focus: read_events 공백 시 G4는
// 전부 0(제로 분모 가드 — 페어링 쿼리도 미실행).
func TestReportEffectZeroReads(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "IN ('read_stats','hot_stats')"):
			return nil, nil
		case strings.Contains(sql, "daily:"):
			return nil, nil
		case strings.Contains(sql, "read_count > 0"):
			return [][]any{{"10", "0", "10"}}, nil // active 10·회상 0·dead 10
		case strings.Contains(sql, "read_events"):
			return nil, nil
		case strings.Contains(sql, "last_read_at"):
			return [][]any{{"0", "0"}}, nil
		case strings.Contains(sql, "invalid_at >="):
			return [][]any{{"0"}}, nil
		case strings.Contains(sql, "count"):
			return [][]any{{"0", "0", "0"}}, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := runReport(context.Background(), st, nil, nil, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	for _, want := range []string{
		`"hit_pct":0`, `"read_events_n":0`, `"read_write_pairs":0`, `"pair_pct":0`,
		`"fallback_pct":0`, `"reuse_pct":0`, `"dead_pct":100`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("%s 누락: %s", want, out.String())
		}
	}
	// 제로 가드 — G4 페어링 쿼리 미실행(stale 집계의 ingested_at < 과
	// 구분되는 페어링 고유 형태로 판정).
	for _, q := range st.queries {
		if strings.Contains(q, "ingested_at >=") {
			t.Errorf("read 0인데 ingested_at 페어링 쿼리 실행: %s", q)
		}
	}
}

// TestReportEffectDegradesOnEventLogError — Review Focus(리뷰 #2):
// read_events 조회 실패(테이블 부재 등)는 G4·fallback_pct를 0으로
// 강등할 뿐 report 전체를 깨지 않는다(계측 원칙 — 다른 지표는 그대로).
func TestReportEffectDegradesOnEventLogError(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "daily:"):
			return nil, nil
		case strings.Contains(sql, "read_count > 0"):
			return [][]any{{"50", "10", "5"}}, nil // G3 — 강등 대상 아님
		case strings.Contains(sql, "read_events"):
			return nil, fmt.Errorf("relation \"read_events\" does not exist (42P01)")
		case strings.Contains(sql, "last_read_at"):
			return [][]any{{"0", "0"}}, nil
		case strings.Contains(sql, "invalid_at >="):
			return [][]any{{"0"}}, nil
		case strings.Contains(sql, "count"):
			return [][]any{{"0", "0", "0"}}, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := runReport(context.Background(), st, nil, nil, &out); code != 0 {
		t.Fatalf("exit %d, want 0(강등 — 이벤트 로그 장애가 보고를 깨지 않음): %s", code, out.String())
	}
	for _, want := range []string{
		`"read_events_n":0`, `"read_write_pairs":0`, `"pair_pct":0`, `"fallback_pct":0`,
		`"reuse_pct":20`, `"dead_pct":10`, // G3은 강등 없이 산출
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("%s 누락: %s", want, out.String())
		}
	}
	// 강등 이후 ingested_at 페어링 쿼리도 미실행.
	for _, q := range st.queries {
		if strings.Contains(q, "ingested_at >=") {
			t.Errorf("이벤트 조회 실패 후 페어링 쿼리 실행: %s", q)
		}
	}
}
