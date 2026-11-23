package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTimeWindowHit(t *testing.T) {
	base := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	times := []time.Time{
		base.Add(-time.Minute),
		base.Add(25 * time.Minute),
	}
	if !timeWindowHit(times, base, 30*time.Minute) {
		t.Error("창 내(+25m) 히트 실패")
	}
	edge := []time.Time{base, base.Add(30 * time.Minute)}
	if !timeWindowHit(edge, base, 30*time.Minute) {
		t.Error("Δ=0 스킵 후 창 끝(+30m) 히트 실패")
	}
	miss := []time.Time{base, base.Add(-time.Minute), base.Add(31 * time.Minute)}
	if timeWindowHit(miss, base, 30*time.Minute) {
		t.Error("동시·직전·창 밖만 있을 때 히트 오판")
	}
}

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

func TestReportEffectSection(t *testing.T) {
	now := time.Now().UTC()
	sessStart := now.Add(-3 * time.Hour)
	readA := now.Add(-2 * time.Hour)
	readB := now.Add(-1 * time.Hour)
	writeAt := readB.Add(10 * time.Minute)
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "knowledge_id, ts, kind"),
			strings.Contains(sql, "id, length(summary) FROM knowledge"),
			strings.Contains(sql, "id, invalid_at, superseded_by FROM knowledge"):
			return nil, nil
		case strings.Contains(sql, "IN ('hot_stats')"):
			return [][]any{{"read_stats", `{"calls":10,"zero":4}`}}, nil
		case strings.Contains(sql, "SELECT value FROM meta WHERE key = $1"):
			return nil, nil
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil
		case strings.Contains(sql, "daily:"):
			return [][]any{{"daily:" + now.Format("2006-01-02"),
				`{"read":10,"read_zero":4,"read_zero_near":3,"write":8,"dup":6}`}}, nil
		case strings.Contains(sql, "read_count > 0"):

			return [][]any{{int64(100), int64(60)}}, nil
		case strings.Contains(sql, "id FROM knowledge"):

			return [][]any{{int64(20)}, {int64(30)}}, nil
		case strings.Contains(sql, "hot_events"):
			return [][]any{{"20,99"}}, nil
		case strings.Contains(sql, "read_events"):

			return [][]any{
				{readA.Format(time.RFC3339), false, true, "7", 0},
				{readB.Format(time.RFC3339), false, false, "", 1},
			}, nil
		case strings.Contains(sql, "session_starts"):
			return [][]any{{sessStart.Format(time.RFC3339)}}, nil
		case strings.Contains(sql, "last_read_at"):
			return [][]any{{"0", "0"}}, nil
		case strings.Contains(sql, "id, topic FROM knowledge"):

			return [][]any{{int64(7), "chain-topic"}}, nil
		case strings.Contains(sql, "ingested_at"):
			return [][]any{{writeAt.Format(time.RFC3339), "chain-topic"}}, nil
		case strings.Contains(sql, "invalid_at >="):
			return [][]any{{"0"}}, nil
		case strings.Contains(sql, "count"):
			return [][]any{{"0", "0", "0"}}, nil
		}
		t.Fatalf("미매칭 쿼리 — fixture 라우팅 계약 위반(새 쿼리 추가 시 이 테스트 갱신): %s", sql)
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
			RecallLedPct   int `json:"recall_led_pct"`
			ChainPct       int `json:"chain_pct"`
		} `json:"effect"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &rep); err != nil {
		t.Fatalf("report JSON 파싱: %v\n%s", err, out.String())
	}
	e := rep.Effect

	if e.HitPct != 60 || e.NearPct != 30 || e.FallbackPct != 50 ||
		e.DupPrevented != 6 || e.ReusePct != 60 || e.DeadPct != 1 ||
		e.ReadEventsN != 2 || e.ReadWritePairs != 1 || e.PairPct != 50 ||
		e.RecallLedPct != 100 || e.ChainPct != 100 {
		t.Errorf("effect = %+v, want {60 30 50 6 60 1 2 1 50 100 100}", e)
	}
}

func TestReportEffectZeroReads(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "knowledge_id, ts, kind"),
			strings.Contains(sql, "id, length(summary) FROM knowledge"),
			strings.Contains(sql, "id, invalid_at, superseded_by FROM knowledge"):
			return nil, nil
		case strings.Contains(sql, "IN ('hot_stats')"):
			return nil, nil
		case strings.Contains(sql, "knowledge_id, ts, kind"),
			strings.Contains(sql, "id, length(summary) FROM knowledge"),
			strings.Contains(sql, "id, invalid_at, superseded_by FROM knowledge"):
			return nil, nil
		case strings.Contains(sql, "IN ('hot_stats')"):
			return nil, nil
		case strings.Contains(sql, "daily:"):
			return nil, nil
		case strings.Contains(sql, "read_count > 0"):
			return [][]any{{"10", "0"}}, nil
		case strings.Contains(sql, "id FROM knowledge"):

			return [][]any{{int64(4)}, {int64(6)}}, nil
		case strings.Contains(sql, "hot_events"):
			return nil, nil
		case strings.Contains(sql, "SELECT value FROM meta WHERE key = $1"):
			return nil, nil
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil
		case strings.Contains(sql, "read_events"):
			return nil, nil
		case strings.Contains(sql, "last_read_at"):
			return [][]any{{"0", "0"}}, nil
		case strings.Contains(sql, "ingested_at"):
			return nil, nil
		case strings.Contains(sql, "invalid_at >="):
			return [][]any{{"0"}}, nil
		case strings.Contains(sql, "count"):
			return [][]any{{"0", "0", "0"}}, nil
		}
		t.Fatalf("미매칭 쿼리 — fixture 라우팅 계약 위반(새 쿼리 추가 시 이 테스트 갱신): %s", sql)
		return nil, nil
	}}
	var out strings.Builder
	if code := runReport(context.Background(), st, nil, nil, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	for _, want := range []string{
		`"hit_pct":0`, `"read_events_n":0`, `"read_write_pairs":0`, `"pair_pct":0`,
		`"fallback_pct":0`, `"reuse_pct":0`, `"dead_pct":20`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("%s 누락: %s", want, out.String())
		}
	}

	for _, q := range st.queries {
		if strings.Contains(q, "ingested_at >=") {
			t.Errorf("read 0인데 ingested_at 페어링 쿼리 실행: %s", q)
		}
	}
}

func TestReportEffectSessionPairing(t *testing.T) {
	now := time.Now().UTC()
	start1 := now.Add(-3 * time.Hour)
	readA := now.Add(-90 * time.Minute)
	start2 := now.Add(-80 * time.Minute)
	writeB := now.Add(-70 * time.Minute)
	sessionsUp := true
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "knowledge_id, ts, kind"),
			strings.Contains(sql, "id, length(summary) FROM knowledge"),
			strings.Contains(sql, "id, invalid_at, superseded_by FROM knowledge"):
			return nil, nil
		case strings.Contains(sql, "IN ('hot_stats')"):
			return nil, nil
		case strings.Contains(sql, "daily:"):
			return nil, nil
		case strings.Contains(sql, "read_count > 0"):
			return [][]any{{"10", "0"}}, nil
		case strings.Contains(sql, "id FROM knowledge"):
			return nil, nil
		case strings.Contains(sql, "hot_events"):
			return nil, nil
		case strings.Contains(sql, "SELECT value FROM meta WHERE key = $1"):
			return nil, nil
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil
		case strings.Contains(sql, "read_events"):
			return [][]any{{readA.Format(time.RFC3339), false, ""}}, nil
		case strings.Contains(sql, "session_starts"):
			if !sessionsUp {
				return nil, fmt.Errorf("relation missing")
			}
			return [][]any{
				{start1.Format(time.RFC3339)},
				{start2.Format(time.RFC3339)},
			}, nil
		case strings.Contains(sql, "last_read_at"):
			return [][]any{{"0", "0"}}, nil
		case strings.Contains(sql, "id, topic FROM knowledge"):
			return nil, nil
		case strings.Contains(sql, "ingested_at"):
			return [][]any{{writeB.Format(time.RFC3339), "other"}}, nil
		case strings.Contains(sql, "invalid_at >="):
			return [][]any{{"0"}}, nil
		case strings.Contains(sql, "count"):
			return [][]any{{"0", "0", "0"}}, nil
		}
		t.Fatalf("미매칭 쿼리 — fixture 라우팅 계약 위반(새 쿼리 추가 시 이 테스트 갱신): %s", sql)
		return nil, nil
	}}
	run := func(t *testing.T) (pairs, recallLed int) {
		t.Helper()
		var out strings.Builder
		if code := runReport(context.Background(), st, []string{"--days", "1"}, nil, &out); code != 0 {
			t.Fatalf("exit %d: %s", code, out.String())
		}
		var rep struct {
			Effect struct {
				ReadWritePairs int `json:"read_write_pairs"`
				RecallLedPct   int `json:"recall_led_pct"`
			} `json:"effect"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &rep); err != nil {
			t.Fatalf("report JSON 파싱: %v\n%s", err, out.String())
		}
		return rep.Effect.ReadWritePairs, rep.Effect.RecallLedPct
	}

	if pairs, led := run(t); pairs != 0 || led != 0 {
		t.Errorf("세션 판별: pairs=%d led=%d, want 0/0(write-only 세션)", pairs, led)
	}

	sessionsUp = false
	if pairs, _ := run(t); pairs != 1 {
		t.Errorf("폴백: pairs=%d, want 1(시간창 규칙)", pairs)
	}

	for _, q := range st.queries {
		if strings.Contains(q, "id, topic FROM knowledge") {
			t.Errorf("ids 없는 이벤트만 있는데 topics 조회 실행: %s", q)
		}
	}
}

func TestChainPct(t *testing.T) {
	base := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	evs := []readEventRow{
		{Ts: base, Ids: []int{7}},
		{Ts: base, Ids: []int{7}},
		{Ts: base.Add(2 * time.Hour), Ids: []int{7}},
		{Ts: base.Add(3 * time.Hour), Ids: []int{9}},
		{Ts: base.Add(4 * time.Hour), Ids: []int{11}},
	}
	writes := []writeRow{
		{Ts: base.Add(3*time.Hour + 5*time.Minute), Topic: "t9"},
		{Ts: base.Add(30 * time.Hour), Topic: "t7"},
	}
	topics := map[int]string{7: "t7", 9: "t9", 11: "t11"}

	if got := chainPct(evs, writes, topics, 24*time.Hour); got != 60 {
		t.Errorf("chainPct = %d, want 60(3/5)", got)
	}

	sameTS := []readEventRow{{Ts: base, Ids: []int{7}}, {Ts: base, Ids: []int{7}}}
	if got := chainPct(sameTS, nil, topics, 24*time.Hour); got != 0 {
		t.Errorf("Δ=0만 있을 때 chainPct = %d, want 0", got)
	}

	if got := chainPct([]readEventRow{{Ts: base}}, writes, topics, 24*time.Hour); got != 0 {
		t.Errorf("ids 없는 read 단독 chainPct = %d, want 0", got)
	}
}

func TestReportEffectDegradesOnEventLogError(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "knowledge_id, ts, kind"),
			strings.Contains(sql, "id, length(summary) FROM knowledge"),
			strings.Contains(sql, "id, invalid_at, superseded_by FROM knowledge"):
			return nil, nil
		case strings.Contains(sql, "IN ('hot_stats')"):
			return nil, nil
		case strings.Contains(sql, "daily:"):
			return nil, nil
		case strings.Contains(sql, "read_count > 0"):
			return [][]any{{"10", "1"}}, nil
		case strings.Contains(sql, "id FROM knowledge"):
			return [][]any{{int64(5)}}, nil
		case strings.Contains(sql, "hot_events"):
			return nil, nil
		case strings.Contains(sql, "SELECT value FROM meta WHERE key = $1"):
			return nil, nil
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil
		case strings.Contains(sql, "read_events"):
			return nil, fmt.Errorf("relation \"read_events\" does not exist (42P01)")
		case strings.Contains(sql, "last_read_at"):
			return [][]any{{"0", "0"}}, nil
		case strings.Contains(sql, "ingested_at"):
			return nil, nil
		case strings.Contains(sql, "invalid_at >="):
			return [][]any{{"0"}}, nil
		case strings.Contains(sql, "count"):
			return [][]any{{"0", "0", "0"}}, nil
		}
		t.Fatalf("미매칭 쿼리 — fixture 라우팅 계약 위반(새 쿼리 추가 시 이 테스트 갱신): %s", sql)
		return nil, nil
	}}
	var out strings.Builder
	if code := runReport(context.Background(), st, nil, nil, &out); code != 0 {
		t.Fatalf("exit %d, want 0(강등 — 이벤트 로그 장애가 보고를 깨지 않음): %s", code, out.String())
	}
	for _, want := range []string{
		`"read_events_n":0`, `"read_write_pairs":0`, `"pair_pct":0`, `"fallback_pct":0`,
		`"reuse_pct":10`, `"dead_pct":10`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("%s 누락: %s", want, out.String())
		}
	}

	for _, q := range st.queries {
		if strings.Contains(q, "ingested_at >=") {
			t.Errorf("이벤트 조회 실패 후 페어링 쿼리 실행: %s", q)
		}
	}
}

func TestReportProfileHintWithoutIDs(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "knowledge_id, ts, kind"),
			strings.Contains(sql, "id, length(summary) FROM knowledge"),
			strings.Contains(sql, "id, invalid_at, superseded_by FROM knowledge"):
			return nil, nil
		case strings.Contains(sql, "IN ('hot_stats')"):
			return nil, nil
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil
		case strings.Contains(sql, "daily:"):
			return nil, nil
		case strings.Contains(sql, "read_count > 0"):
			return [][]any{{"100", "60"}}, nil
		case strings.Contains(sql, "id FROM knowledge"):
			return nil, nil
		case strings.Contains(sql, "read_events"):

			return [][]any{
				{now.Add(-2 * time.Hour).Format(time.RFC3339), false, ""},
				{now.Add(-1 * time.Hour).Format(time.RFC3339), false, ""},
				{now.Add(-30 * time.Minute).Format(time.RFC3339), false, ""},
			}, nil
		case strings.Contains(sql, "hot_events"):
			return nil, nil
		case strings.Contains(sql, "ingested_at"):
			return nil, nil
		case strings.Contains(sql, "session_starts"):
			return nil, nil
		case strings.Contains(sql, "SELECT value FROM meta WHERE key = $1"):
			return nil, nil
		case strings.Contains(sql, "last_read_at"):
			return [][]any{{"0", "0"}}, nil
		case strings.Contains(sql, "ingested_at"):
			return nil, nil
		case strings.Contains(sql, "invalid_at >="):
			return [][]any{{"0"}}, nil
		case strings.Contains(sql, "count"):
			return [][]any{{"0", "0", "0"}}, nil
		}
		t.Fatalf("미매칭 쿼리: %s", sql)
		return nil, nil
	}}
	var out strings.Builder
	if code := runReport(context.Background(), st, []string{"--days", "3"}, nil, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"profile_hint":"insufficient sample`) {
		t.Fatalf("ids 없는 표본에 힌트 공백: %s", out.String())
	}
}
