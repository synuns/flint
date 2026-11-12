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

// TestTimeWindowHit — Review Focus(test_pair_window_boundary, v0.20.0
// 이식): base 직후 창 안(0 < Δ ≤ window)의 시각만 히트 — 동시(Δ=0)·
// 직전·창 밖은 제외, 창 끝은 포함. times 오름차순 가정.
func TestTimeWindowHit(t *testing.T) {
	base := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	times := []time.Time{
		base.Add(-time.Minute),     // 직전 — 불히트
		base.Add(25 * time.Minute), // 창 내 — 히트
	}
	if !timeWindowHit(times, base, 30*time.Minute) {
		t.Error("창 내(+25m) 히트 실패")
	}
	edge := []time.Time{base, base.Add(30 * time.Minute)} // 동시(스킵)·창 끝(히트)
	if !timeWindowHit(edge, base, 30*time.Minute) {
		t.Error("Δ=0 스킵 후 창 끝(+30m) 히트 실패")
	}
	miss := []time.Time{base, base.Add(-time.Minute), base.Add(31 * time.Minute)}
	if timeWindowHit(miss, base, 30*time.Minute) {
		t.Error("동시·직전·창 밖만 있을 때 히트 오판")
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
// 집계+노출 보정(G3)·세션 경계 페어링·회상 선도·연쇄(G4, v0.20.0).
func TestReportEffectSection(t *testing.T) {
	now := time.Now().UTC()
	sessStart := now.Add(-3 * time.Hour) // 모든 이벤트의 세션 경계
	readA := now.Add(-2 * time.Hour)     // 창 내(1일)·폴백 경유·ids=7
	readB := now.Add(-1 * time.Hour)     // 정경로·ids 없음
	writeAt := readB.Add(10 * time.Minute)
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "IN ('hot_stats')"): // v0.42.0 T2 — read_stats 폐지, hot_stats만
			return [][]any{{"read_stats", `{"calls":10,"zero":4}`}}, nil
		case strings.Contains(sql, "SELECT value FROM meta WHERE key = $1"):
			return nil, nil // read_fallback_calls 부재
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil // thresholds 미설정 — 기본값(v0.20.4 이양)(누적 폴백률 0)
		case strings.Contains(sql, "daily:"):
			return [][]any{{"daily:" + now.Format("2006-01-02"),
				`{"read":10,"read_zero":4,"read_zero_near":3,"write":8,"dup":6}`}}, nil
		case strings.Contains(sql, "read_count > 0"):
			// G3 카운터 — active 100·회상 60.
			return [][]any{{int64(100), int64(60)}}, nil
		case strings.Contains(sql, "id FROM knowledge"):
			// G3 dead 후보 id — 20·30(아래 hot 노출로 20은 제외).
			return [][]any{{int64(20)}, {int64(30)}}, nil
		case strings.Contains(sql, "hot_events"):
			return [][]any{{"20,99"}}, nil
		case strings.Contains(sql, "read_events"):
			// v0.30.0 리뷰 4: 실 SELECT 열 모양 (ts, zero, fallback, ids,
			// first_rank) — readA 폴백 경유(ids=7, rank 0)·readB 정경로
			// 정합 1위(rank 1) — fallback_pct=50·exact/top1/mrr 검증.
			return [][]any{
				{readA.Format(time.RFC3339), false, true, "7", 0},
				{readB.Format(time.RFC3339), false, false, "", 1},
			}, nil
		case strings.Contains(sql, "session_starts"):
			return [][]any{{sessStart.Format(time.RFC3339)}}, nil
		case strings.Contains(sql, "last_read_at"):
			return [][]any{{"0", "0"}}, nil
		case strings.Contains(sql, "id, topic FROM knowledge"):
			// chain(b) — 반환 id 7의 topic과 같은 write.
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
	// G1: 1−4/10=60·3/10=30·폴백 1/2=50 · G2: Σdup=6 · G3: 60/100·
	// dead 후보 2 중 hot 노출 20 제외→1% · G4: 세션 1개에서 readB만
	// 페어(1/2=50)·회상 선도 1/1=100·연쇄 readA(7→같은 topic write) 1/1=100.
	if e.HitPct != 60 || e.NearPct != 30 || e.FallbackPct != 50 ||
		e.DupPrevented != 6 || e.ReusePct != 60 || e.DeadPct != 1 ||
		e.ReadEventsN != 2 || e.ReadWritePairs != 1 || e.PairPct != 50 ||
		e.RecallLedPct != 100 || e.ChainPct != 100 {
		t.Errorf("effect = %+v, want {60 30 50 6 60 1 2 1 50 100 100}", e)
	}
}

// TestReportEffectZeroReads — Review Focus: read_events 공백 시 G4는
// 전부 0(제로 분모 가드 — 페어링 쿼리도 미실행).
func TestReportEffectZeroReads(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "IN ('hot_stats')"): // v0.42.0 T2 — read_stats 폐지, hot_stats만
			return nil, nil
		case strings.Contains(sql, "IN ('hot_stats')"): // v0.42.0 T2 — read_stats 폐지, hot_stats만
			return nil, nil
		case strings.Contains(sql, "daily:"):
			return nil, nil
		case strings.Contains(sql, "read_count > 0"):
			return [][]any{{"10", "0"}}, nil // active 10·회상 0
		case strings.Contains(sql, "id FROM knowledge"):
			// dead 후보 2행(노출 없음 → 전부 dead) → 2/10=20%.
			return [][]any{{int64(4)}, {int64(6)}}, nil
		case strings.Contains(sql, "hot_events"):
			return nil, nil // 노출 이력 없음
		case strings.Contains(sql, "SELECT value FROM meta WHERE key = $1"):
			return nil, nil // read_fallback_calls 부재
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil // thresholds 미설정 — 기본값(v0.20.4 이양)
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
	// 제로 가드 — G4 페어링 쿼리 미실행(stale 집계의 ingested_at < 과
	// 구분되는 페어링 고유 형태로 판정).
	for _, q := range st.queries {
		if strings.Contains(q, "ingested_at >=") {
			t.Errorf("read 0인데 ingested_at 페어링 쿼리 실행: %s", q)
		}
	}
}

// TestReportEffectSessionPairing — Review Focus(v0.20.0 D): 세션 경계가
// 페어링을 판별한다 — read(세션1) 뒤 20분 write(세션2)는 시간창 폴백과
// 달리 페어가 아니다. 회상 선도는 write-only 세션에서 0.
func TestReportEffectSessionPairing(t *testing.T) {
	now := time.Now().UTC()
	start1 := now.Add(-3 * time.Hour)    // 세션1 시작
	readA := now.Add(-90 * time.Minute)  // 세션1 소속 read
	start2 := now.Add(-80 * time.Minute) // 세션2 시작
	writeB := now.Add(-70 * time.Minute) // 세션2 소속 write(readA+20m — 폴백이면 페어)
	sessionsUp := true
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "IN ('hot_stats')"): // v0.42.0 T2 — read_stats 폐지, hot_stats만
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
			return nil, nil // read_fallback_calls 부재
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil // thresholds 미설정 — 기본값(v0.20.4 이양)
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

	// 세션 경계 있음 — 세간 write는 페어 아님(시간창이었다면 1).
	if pairs, led := run(t); pairs != 0 || led != 0 {
		t.Errorf("세션 판별: pairs=%d led=%d, want 0/0(write-only 세션)", pairs, led)
	}
	// 폴백(세션 조회 실패) — 기존 v0.19.0 시간창 규칙: 20m 내 페어 1.
	sessionsUp = false
	if pairs, _ := run(t); pairs != 1 {
		t.Errorf("폴백: pairs=%d, want 1(시간창 규칙)", pairs)
	}
	// ids 없는 read 단독 — topics 조회 미실행(분모 0 skip, 리뷰 #6).
	for _, q := range st.queries {
		if strings.Contains(q, "id, topic FROM knowledge") {
			t.Errorf("ids 없는 이벤트만 있는데 topics 조회 실행: %s", q)
		}
	}
}

// TestChainPct — Review Focus(v0.20.0 B): 사용 연쇄의 두 경로와 비히트 —
// (a) 24h 내 같은 id 재반환 히트(Δ=0 동시 기록은 제외 — 페어링 규칙과
// 일관), (b) 같은 topic write 히트, 24h 경과·다른 topic은 비히트.
func TestChainPct(t *testing.T) {
	base := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	evs := []readEventRow{
		{Ts: base, Ids: []int{7}},                     // (a) 히트(+2h 재반환)
		{Ts: base, Ids: []int{7}},                     // Δ=0 동시 — (a) 아니지만 +2h로 히트
		{Ts: base.Add(2 * time.Hour), Ids: []int{7}},  // ev0·ev1의 재반환 상대
		{Ts: base.Add(3 * time.Hour), Ids: []int{9}},  // (b) 같은 topic write 히트
		{Ts: base.Add(4 * time.Hour), Ids: []int{11}}, // 비히트
	}
	writes := []writeRow{
		{Ts: base.Add(3*time.Hour + 5*time.Minute), Topic: "t9"},
		{Ts: base.Add(30 * time.Hour), Topic: "t7"}, // 24h 창 밖
	}
	topics := map[int]string{7: "t7", 9: "t9", 11: "t11"}
	// 히트: ev0·ev1((a) +2h 재반환)·ev3((b)) = 3/5 = 60. ev2는 이후
	// 재반환 없음(t7 write는 24h 밖) → 비히트.
	if got := chainPct(evs, writes, topics, 24*time.Hour); got != 60 {
		t.Errorf("chainPct = %d, want 60(3/5)", got)
	}
	// Δ=0만 있는 경우 — 동시 기록은 연쇄 아님(0/1).
	sameTS := []readEventRow{{Ts: base, Ids: []int{7}}, {Ts: base, Ids: []int{7}}}
	if got := chainPct(sameTS, nil, topics, 24*time.Hour); got != 0 {
		t.Errorf("Δ=0만 있을 때 chainPct = %d, want 0", got)
	}
	// ids 없는 read는 분모에서 제외 — 전부 비히트면 0.
	if got := chainPct([]readEventRow{{Ts: base}}, writes, topics, 24*time.Hour); got != 0 {
		t.Errorf("ids 없는 read 단독 chainPct = %d, want 0", got)
	}
}

// TestReportEffectDegradesOnEventLogError — Review Focus(리뷰 #2):
// read_events 조회 실패(테이블 부재 등)는 G4·fallback_pct를 0으로
// 강등할 뿐 report 전체를 깨지 않는다(계측 원칙 — 다른 지표는 그대로).
func TestReportEffectDegradesOnEventLogError(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "IN ('hot_stats')"): // v0.42.0 T2 — read_stats 폐지, hot_stats만
			return nil, nil
		case strings.Contains(sql, "daily:"):
			return nil, nil
		case strings.Contains(sql, "read_count > 0"):
			return [][]any{{"10", "1"}}, nil // G3 카운터 — 강등 대상 아님(active 10·회상 1)
		case strings.Contains(sql, "id FROM knowledge"):
			return [][]any{{int64(5)}}, nil // dead 후보 1행 → 1/10=10%
		case strings.Contains(sql, "hot_events"):
			return nil, nil // 노출 이력 없음
		case strings.Contains(sql, "SELECT value FROM meta WHERE key = $1"):
			return nil, nil // read_fallback_calls 부재
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil // thresholds 미설정 — 기본값(v0.20.4 이양)
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
		`"reuse_pct":10`, `"dead_pct":10`, // G3은 강등 없이 산출(1/10)
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

// TestReportProfileHintWithoutIDs — v0.24.1: ids 없는 read 이벤트만
// 있어도 profile_hint가 표본 하한 판정을 출력한다(hasIDs 조기 return
// 뒤에 있어 영구 공백이던 결함 — 운영 관측 발견).
func TestReportProfileHintWithoutIDs(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "IN ('hot_stats')"): // v0.42.0 T2 — read_stats 폐지, hot_stats만
			return nil, nil
		case strings.Contains(sql, "key IN ('pending_max'"):
			return nil, nil // thresholds 기본값
		case strings.Contains(sql, "daily:"):
			return nil, nil
		case strings.Contains(sql, "read_count > 0"):
			return [][]any{{"100", "60"}}, nil
		case strings.Contains(sql, "id FROM knowledge"):
			return nil, nil
		case strings.Contains(sql, "read_events"):
			// ids 없는(read 이벤트만) 표본 3건 — hasIDs false 경로.
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
	if !strings.Contains(out.String(), `"profile_hint":"보류`) {
		t.Fatalf("ids 없는 표본에 힌트 공백: %s", out.String())
	}
}
