// report_parity_test.go — the metric equivalence of the
// read_stats-blob abolition. From the same fixture (10 reads: 3 zero, 1 near, 2 via
// fallback), it compares the report and doctor outputs of the baseline computed with the
// old blob-path formulas (cumulative read_stats calls, daily counter sums, the old
// fallback_pct definition) and the derived path (read_events, the daily merge). The
// pre-migration baseline is snapshotted with the old path's pure computation functions (ReadZeroPct, PctOf, FallbackPct).
package ops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// parityFake — the derived-path fake: daily:* is blank like new data; only read_events
// is returned (fixture: read 10, zero 3, near 1, fallback 2 — all today).
func parityFake(n int, zeroN, nearN, fbN int) *fakeStore {
	now := time.Now().UTC()
	mergeRows := [][]any{}
	effectRows := [][]any{}
	for i := 0; i < n; i++ {
		mergeRows = append(mergeRows, []any{now, i < zeroN, i < nearN})
		effectRows = append(effectRows, []any{now, i < zeroN, i < fbN, "", 0})
	}
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "LIKE 'daily:%'"):
			return nil, nil // new data — no daily read fields
		case strings.Contains(sql, "near FROM read_events"):
			return mergeRows, nil // the WindowedDaily derived merge
		case strings.Contains(sql, "fallback, ids"):
			return effectRows, nil // CollectEffect(G4)
		case strings.Contains(sql, "FROM meta"):
			return nil, nil // no hot_stats or thresholds — defaults
		case strings.Contains(sql, "hot_events"),
			strings.Contains(sql, "session_starts"),
			strings.Contains(sql, "ingested_at >="),
			strings.Contains(sql, "id, topic FROM knowledge"):
			return nil, nil
		}
		return [][]any{{int64(0), int64(0), int64(0)}}, nil // tallies 0
	}}
}

// TestEffectParity — from the fixture of 10 reads with 3 zero and 1 near, the
// blob-path baseline (old formulas) == the derived path's report output.
func TestEffectParity(t *testing.T) {
	const reads, zeros, nears, fallbacks = 10, 3, 1, 2
	// ── pre-migration baseline snapshot (old blob-path formulas) ──
	// zero_pct and zero_near_pct: daily sums, floored integer %.
	legacyZeroPct := ReadZeroPct(reads, zeros)
	legacyNearPct := ReadZeroPct(reads, nears)
	// effect hit and near: derived from the same sums.
	legacyHitPct := PctOf(reads-zeros, reads)
	legacyEffectNear := PctOf(nears, reads)
	// fallback_pct: the old definition read_fallback_calls/read_stats calls (cumulative).
	legacyFallbackPct := FallbackPct(fallbacks, reads)

	var out strings.Builder
	if code := RunReport(context.Background(), parityFake(reads, zeros, nears, fallbacks),
		[]string{"--days", "1"}, nil, &out); code != 0 {
		t.Fatalf("report exit=%d: %s", code, out.String())
	}
	var rep struct {
		Days        []ReportDay `json:"days"`
		ZeroPct     int         `json:"zero_pct"`
		ZeroNearPct int         `json:"zero_near_pct"`
		FallbackPct int         `json:"fallback_pct"`
		Effect      EffectOutput
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &rep); err != nil {
		t.Fatalf("report JSON 파싱: %v (%s)", err, out.String())
	}
	if got := rep.ZeroPct; got != legacyZeroPct {
		t.Errorf("zero_pct=%d, want %d(blob 기준선)", got, legacyZeroPct)
	}
	if got := rep.ZeroNearPct; got != legacyNearPct {
		t.Errorf("zero_near_pct=%d, want %d(blob 기준선)", got, legacyNearPct)
	}
	if got := rep.FallbackPct; got != legacyFallbackPct {
		t.Errorf("fallback_pct=%d, want %d(blob 기준선)", got, legacyFallbackPct)
	}
	if got := rep.Effect.HitPct; got != legacyHitPct {
		t.Errorf("effect.hit_pct=%d, want %d(blob 기준선)", got, legacyHitPct)
	}
	if got := rep.Effect.NearPct; got != legacyEffectNear {
		t.Errorf("effect.near_pct=%d, want %d(blob 기준선)", got, legacyEffectNear)
	}
	if got := rep.Effect.FallbackPct; got != legacyFallbackPct {
		t.Errorf("effect.fallback_pct=%d, want %d(blob 기준선)", got, legacyFallbackPct)
	}
	if rep.Effect.ReadEventsN != reads {
		t.Errorf("read_events_n=%d, want %d", rep.Effect.ReadEventsN, reads)
	}
	// the daily rows also carry derived values (the last row = today).
	if len(rep.Days) != 1 {
		t.Fatalf("days 길이=%d, want 1", len(rep.Days))
	}
	d := rep.Days[0]
	if d.Read != reads || d.ReadZero != zeros || d.ReadNear != nears {
		t.Errorf("days[0] read/zero/near = %d/%d/%d, want %d/%d/%d",
			d.Read, d.ReadZero, d.ReadNear, reads, zeros, nears)
	}
}

// TestZeroNearParity — the doctor zero-near verdict's equivalence: from the same
// fixture, the old formula (near*100/read, sample floor) and the WindowedDaily derived path agree.
func TestZeroNearParity(t *testing.T) {
	const reads, nears = 10, 1
	// the old blob-path formula baseline — sample 10 >= min 10, 10% < warn 30 -> ok.
	legacyPct := nears * 100 / reads
	legacyStatus := "ok"
	legacyDetail := "zero_near_pct 10% (7d read 10)" // the old format verbatim
	if reads < 10 || legacyPct >= 30 {
		t.Fatalf("픽스처가 기준선과 불일치(pct=%d)", legacyPct)
	}

	c := CheckZeroNear(context.Background(), parityFake(reads, 0, nears, 0))
	if c.Status != legacyStatus {
		t.Errorf("zero-near status=%s, want %s(기준선): %s", c.Status, legacyStatus, c.Detail)
	}
	if c.Detail != legacyDetail {
		t.Errorf("zero-near detail=%q, want %q(기준선)", c.Detail, legacyDetail)
	}
}

// TestDerivedWindowBoundary — the derived merge cutoff must include the exact
// UTC midnight of the window's first day (ts >= cutoff). An event 1 second before
// midnight is outside the window (not counted in the first row); an event at exactly
// midnight is counted in the first row. Asserts the cutoff argument and the daily bucket placement together.
func TestDerivedWindowBoundary(t *testing.T) {
	now := time.Now().UTC()
	const window = 7
	oldestMidnight := now.AddDate(0, 0, -(window - 1)).Truncate(24 * time.Hour)
	var gotCutoff time.Time
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "LIKE 'daily:%'"):
			return nil, nil // new data — no daily read fields
		case strings.Contains(sql, "near FROM read_events"):
			if len(args) > 0 {
				if ts, ok := args[0].(time.Time); ok {
					gotCutoff = ts
				}
			}
			// exactly midnight (included), 1 second before (excluded), and today's 1 near.
			return [][]any{
				{oldestMidnight, false, false},
				{oldestMidnight.Add(-time.Second), false, false},
				{now, false, true},
			}, nil
		case strings.Contains(sql, "fallback, ids"):
			return [][]any{{oldestMidnight, false, false, "", 0}, {now, false, false, "", 0}}, nil
		case strings.Contains(sql, "FROM meta"):
			return nil, nil
		case strings.Contains(sql, "hot_events"),
			strings.Contains(sql, "session_starts"),
			strings.Contains(sql, "ingested_at >="),
			strings.Contains(sql, "id, topic FROM knowledge"):
			return nil, nil
		}
		return [][]any{{int64(0), int64(0), int64(0)}}, nil
	}}
	var out strings.Builder
	if code := RunReport(context.Background(), f, []string{"--days", "7"}, nil, &out); code != 0 {
		t.Fatalf("report exit=%d: %s", code, out.String())
	}
	// 1. the cutoff argument = the exact midnight of the window's first day (inclusive boundary — an off-by-one guard).
	if !gotCutoff.Equal(oldestMidnight) {
		t.Errorf("파생 컷오프=%v, want %v(창 첫 날 UTC 자정)", gotCutoff, oldestMidnight)
	}
	var rep struct {
		Days []ReportDay `json:"days"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &rep); err != nil {
		t.Fatalf("report JSON 파싱: %v (%s)", err, out.String())
	}
	if len(rep.Days) != window {
		t.Fatalf("days 길이=%d, want %d", len(rep.Days), window)
	}
	// 2. the first row: only the exactly-midnight event (the 1-second-earlier event is outside the window).
	first, last := rep.Days[0], rep.Days[len(rep.Days)-1]
	if first.Read != 1 || first.ReadZero != 0 || first.ReadNear != 0 {
		t.Errorf("days[0] read/zero/near = %d/%d/%d, want 1/0/0(자정 1초 전 배제)",
			first.Read, first.ReadZero, first.ReadNear)
	}
	// 3. the last row (today): includes the 1 near.
	if last.Read != 1 || last.ReadNear != 1 {
		t.Errorf("days[last] read/near = %d/%d, want 1/1", last.Read, last.ReadNear)
	}
}
