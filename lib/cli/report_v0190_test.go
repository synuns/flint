// report_v0190_test.go — effect metering section (docs/METRICS.md §4): GSM
// metric computation, time-window pairing, and cell conversion. fakeStore queryFn patterns
// (carried over from report_v0140).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestTimeWindowHit — only times strictly after base and inside the window (0 < Δ <= window) hit — simultaneous (Δ=0),
// just-before, and outside-window are excluded; the window end is included. Assumes ascending times.
func TestTimeWindowHit(t *testing.T) {
	base := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	times := []time.Time{
		base.Add(-time.Minute),     // just before — no hit
		base.Add(25 * time.Minute), // inside the window — hit
	}
	if !timeWindowHit(times, base, 30*time.Minute) {
		t.Error("창 내(+25m) 히트 실패")
	}
	edge := []time.Time{base, base.Add(30 * time.Minute)} // simultaneous (skip) and window end (hit)
	if !timeWindowHit(edge, base, 30*time.Minute) {
		t.Error("Δ=0 스킵 후 창 끝(+30m) 히트 실패")
	}
	miss := []time.Time{base, base.Add(-time.Minute), base.Add(31 * time.Minute)}
	if timeWindowHit(miss, base, 30*time.Minute) {
		t.Error("동시·직전·창 밖만 있을 때 히트 오판")
	}
}

// TestCellToTime — accepts pgx time.Time and RFC3339 strings; nothing else.
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

// TestReportEffectSection — the effect
// section is computed per the METRICS §4 definitions — daily sums, knowledge-time
// aggregation + exposure correction, session-boundary pairing, recall lead, and chaining.
func TestReportEffectSection(t *testing.T) {
	now := time.Now().UTC()
	sessStart := now.Add(-3 * time.Hour) // session boundary for all events
	readA := now.Add(-2 * time.Hour)     // inside the window (1 day), via fallback, ids=7
	readB := now.Add(-1 * time.Hour)     // direct path, no ids
	writeAt := readB.Add(10 * time.Minute)
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "knowledge_id, ts, kind"), // kreads recall history
			strings.Contains(sql, "id, length(summary) FROM knowledge"),           // token accounting average and hot derivation
			strings.Contains(sql, "id, invalid_at, superseded_by FROM knowledge"): // recall fate
			return nil, nil
		case strings.Contains(sql, "IN ('hot_stats')"): // read_stats abolished, hot_stats only
			return [][]any{{"read_stats", `{"calls":10,"zero":4}`}}, nil
		case strings.Contains(sql, "SELECT value FROM meta WHERE key = $1"):
			return nil, nil // no read_fallback_calls
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil // thresholds unset — defaults (cumulative fallback rate 0)
		case strings.Contains(sql, "daily:"):
			return [][]any{{"daily:" + now.Format("2006-01-02"),
				`{"read":10,"read_zero":4,"read_zero_near":3,"write":8,"dup":6}`}}, nil
		case strings.Contains(sql, "read_count > 0"):
			// G3 counters — active 100, recalls 60.
			return [][]any{{int64(100), int64(60)}}, nil
		case strings.Contains(sql, "id FROM knowledge"):
			// G3 dead candidate ids — 20 and 30 (20 excluded below via hot exposure).
			return [][]any{{int64(20)}, {int64(30)}}, nil
		case strings.Contains(sql, "hot_events"):
			return [][]any{{"20,99"}}, nil
		case strings.Contains(sql, "read_events"):
			// the real SELECT column shape (ts, zero, fallback, ids,
			// first_rank) — readA via fallback (ids=7, rank 0), readB direct path
			// consistent rank 1 — validates fallback_pct=50 and exact/top1/mrr.
			return [][]any{
				{readA.Format(time.RFC3339), false, true, "7", 0},
				{readB.Format(time.RFC3339), false, false, "", 1},
			}, nil
		case strings.Contains(sql, "session_starts"):
			return [][]any{{sessStart.Format(time.RFC3339)}}, nil
		case strings.Contains(sql, "last_read_at"):
			return [][]any{{"0", "0"}}, nil
		case strings.Contains(sql, "id, topic FROM knowledge"):
			// chain(b) — a write with the same topic as returned id 7.
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
	// G1: 1-4/10=60, 3/10=30, fallback 1/2=50; G2: Σdup=6; G3: 60/100 and
	// of 2 dead candidates, hot-exposed 20 excluded -> 1%; G4: in 1 session only readB
	// pairs (1/2=50), recall lead 1/1=100, chain readA (7 -> same-topic write) 1/1=100.
	if e.HitPct != 60 || e.NearPct != 30 || e.FallbackPct != 50 ||
		e.DupPrevented != 6 || e.ReusePct != 60 || e.DeadPct != 1 ||
		e.ReadEventsN != 2 || e.ReadWritePairs != 1 || e.PairPct != 50 ||
		e.RecallLedPct != 100 || e.ChainPct != 100 {
		t.Errorf("effect = %+v, want {60 30 50 6 60 1 2 1 50 100 100}", e)
	}
}

// TestReportEffectZeroReads — with read_events empty, all of G4 is
// 0 (zero-denominator guard — the pairing queries also never run).
func TestReportEffectZeroReads(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "knowledge_id, ts, kind"), // kreads recall history
			strings.Contains(sql, "id, length(summary) FROM knowledge"),           // token accounting average and hot derivation
			strings.Contains(sql, "id, invalid_at, superseded_by FROM knowledge"): // recall fate
			return nil, nil
		case strings.Contains(sql, "IN ('hot_stats')"): // read_stats abolished, hot_stats only
			return nil, nil
		case strings.Contains(sql, "knowledge_id, ts, kind"), // kreads recall history
			strings.Contains(sql, "id, length(summary) FROM knowledge"),           // token accounting average and hot derivation
			strings.Contains(sql, "id, invalid_at, superseded_by FROM knowledge"): // recall fate
			return nil, nil
		case strings.Contains(sql, "IN ('hot_stats')"): // read_stats abolished, hot_stats only
			return nil, nil
		case strings.Contains(sql, "daily:"):
			return nil, nil
		case strings.Contains(sql, "read_count > 0"):
			return [][]any{{"10", "0"}}, nil // active 10, recalls 0
		case strings.Contains(sql, "id FROM knowledge"):
			// 2 dead-candidate rows (no exposure -> all dead) -> 2/10=20%.
			return [][]any{{int64(4)}, {int64(6)}}, nil
		case strings.Contains(sql, "hot_events"):
			return nil, nil // no exposure history
		case strings.Contains(sql, "SELECT value FROM meta WHERE key = $1"):
			return nil, nil // no read_fallback_calls
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil // thresholds unset — defaults
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
	// zero guard — the G4 pairing query never runs (detected via a pairing-specific shape
	// distinct from the stale tally's ingested_at < comparison).
	for _, q := range st.queries {
		if strings.Contains(q, "ingested_at >=") {
			t.Errorf("read 0인데 ingested_at 페어링 쿼리 실행: %s", q)
		}
	}
}

// TestReportEffectSessionPairing — the session boundary
// decides pairing — a write 20 minutes after a read (session 2), unlike the time-window fallback, is
// not a pair. The recall lead is 0 in a write-only session.
func TestReportEffectSessionPairing(t *testing.T) {
	now := time.Now().UTC()
	start1 := now.Add(-3 * time.Hour)    // session 1 start
	readA := now.Add(-90 * time.Minute)  // read belonging to session 1
	start2 := now.Add(-80 * time.Minute) // session 2 start
	writeB := now.Add(-70 * time.Minute) // write belonging to session 2 (readA+20m — a pair under the fallback rule)
	sessionsUp := true
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "knowledge_id, ts, kind"), // kreads recall history
			strings.Contains(sql, "id, length(summary) FROM knowledge"),           // token accounting average and hot derivation
			strings.Contains(sql, "id, invalid_at, superseded_by FROM knowledge"): // recall fate
			return nil, nil
		case strings.Contains(sql, "IN ('hot_stats')"): // read_stats abolished, hot_stats only
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
			return nil, nil // no read_fallback_calls
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil // thresholds unset — defaults
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

	// session boundary present — the inter-session write is not a pair (would be 1 under a time window).
	if pairs, led := run(t); pairs != 0 || led != 0 {
		t.Errorf("세션 판별: pairs=%d led=%d, want 0/0(write-only 세션)", pairs, led)
	}
	// fallback (session lookup failed) — the existing time-window rule: 1 pair within 20m.
	sessionsUp = false
	if pairs, _ := run(t); pairs != 1 {
		t.Errorf("폴백: pairs=%d, want 1(시간창 규칙)", pairs)
	}
	// a lone read without ids — the topics query never runs (denominator-0 skip).
	for _, q := range st.queries {
		if strings.Contains(q, "id, topic FROM knowledge") {
			t.Errorf("ids 없는 이벤트만 있는데 topics 조회 실행: %s", q)
		}
	}
}

// TestChainPct — both paths of usage chaining plus misses —
// (a) a hit for the same id returned again within 24h (a simultaneous Δ=0 record is excluded — consistent
// with the pairing rule), (b) a hit for a same-topic write; past 24h or a different topic is a miss.
func TestChainPct(t *testing.T) {
	base := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	evs := []readEventRow{
		{Ts: base, Ids: []int{7}},                     // (a) hit (+2h re-return)
		{Ts: base, Ids: []int{7}},                     // Δ=0 simultaneous — not (a) itself, but hits via +2h
		{Ts: base.Add(2 * time.Hour), Ids: []int{7}},  // the re-return counterpart for ev0/ev1
		{Ts: base.Add(3 * time.Hour), Ids: []int{9}},  // (b) same-topic write hit
		{Ts: base.Add(4 * time.Hour), Ids: []int{11}}, // miss
	}
	writes := []writeRow{
		{Ts: base.Add(3*time.Hour + 5*time.Minute), Topic: "t9"},
		{Ts: base.Add(30 * time.Hour), Topic: "t7"}, // outside the 24h window
	}
	topics := map[int]string{7: "t7", 9: "t9", 11: "t11"}
	// hits: ev0 and ev1 ((a) +2h re-return) and ev3 ((b)) = 3/5 = 60. ev2 has no later
	// re-return (the t7 write is beyond 24h) -> miss.
	if got := chainPct(evs, writes, topics, 24*time.Hour); got != 60 {
		t.Errorf("chainPct = %d, want 60(3/5)", got)
	}
	// only Δ=0 — a simultaneous record is not a chain (0/1).
	sameTS := []readEventRow{{Ts: base, Ids: []int{7}}, {Ts: base, Ids: []int{7}}}
	if got := chainPct(sameTS, nil, topics, 24*time.Hour); got != 0 {
		t.Errorf("Δ=0만 있을 때 chainPct = %d, want 0", got)
	}
	// reads without ids are excluded from the denominator — all misses means 0.
	if got := chainPct([]readEventRow{{Ts: base}}, writes, topics, 24*time.Hour); got != 0 {
		t.Errorf("ids 없는 read 단독 chainPct = %d, want 0", got)
	}
}

// TestReportEffectDegradesOnEventLogError — a read_events query failure (missing table etc.) only demotes G4 and fallback_pct to 0;
// it does not break the whole report (the metering principle — the other metrics stay as-is).
func TestReportEffectDegradesOnEventLogError(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "knowledge_id, ts, kind"), // kreads recall history
			strings.Contains(sql, "id, length(summary) FROM knowledge"),           // token accounting average and hot derivation
			strings.Contains(sql, "id, invalid_at, superseded_by FROM knowledge"): // recall fate
			return nil, nil
		case strings.Contains(sql, "IN ('hot_stats')"): // read_stats abolished, hot_stats only
			return nil, nil
		case strings.Contains(sql, "daily:"):
			return nil, nil
		case strings.Contains(sql, "read_count > 0"):
			return [][]any{{"10", "1"}}, nil // G3 counters — not demoted (active 10, recalls 1)
		case strings.Contains(sql, "id FROM knowledge"):
			return [][]any{{int64(5)}}, nil // 1 dead-candidate row -> 1/10=10%
		case strings.Contains(sql, "hot_events"):
			return nil, nil // no exposure history
		case strings.Contains(sql, "SELECT value FROM meta WHERE key = $1"):
			return nil, nil // no read_fallback_calls
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil // thresholds unset — defaults
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
		`"reuse_pct":10`, `"dead_pct":10`, // G3 is computed without demotion (1/10)
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("%s 누락: %s", want, out.String())
		}
	}
	// after the demotion, the ingested_at pairing query never runs either.
	for _, q := range st.queries {
		if strings.Contains(q, "ingested_at >=") {
			t.Errorf("이벤트 조회 실패 후 페어링 쿼리 실행: %s", q)
		}
	}
}

// TestReportProfileHintWithoutIDs — even with only read events without ids,
// profile_hint prints the sample-floor determination (a defect that used to be a permanent blank
// after the hasIDs early return — found via operational observation).
func TestReportProfileHintWithoutIDs(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "knowledge_id, ts, kind"), // kreads recall history
			strings.Contains(sql, "id, length(summary) FROM knowledge"),           // token accounting average and hot derivation
			strings.Contains(sql, "id, invalid_at, superseded_by FROM knowledge"): // recall fate
			return nil, nil
		case strings.Contains(sql, "IN ('hot_stats')"): // read_stats abolished, hot_stats only
			return nil, nil
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil // thresholds defaults
		case strings.Contains(sql, "daily:"):
			return nil, nil
		case strings.Contains(sql, "read_count > 0"):
			return [][]any{{"100", "60"}}, nil
		case strings.Contains(sql, "id FROM knowledge"):
			return nil, nil
		case strings.Contains(sql, "read_events"):
			// 3 samples without ids (read events only) — the hasIDs false path.
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
