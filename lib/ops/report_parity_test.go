// report_parity_test.go — v0.42.0 T2 Review Focus: read_stats blob 폐지
// 전환의 지표 동등성. 같은 픽스처(read 10회 중 zero 3·near 1·폴백 경유 2)에서
// 기존 blob 경로 산식(누적 read_stats calls·daily 카운터 합산·구 fallback_pct
// 정의)으로 계산한 기준선과 파생 경로(read_events·daily 병합 산출)의 report·
// doctor 출력이 동일한지 비교한다. 전환 전 기준선은 구 경로의 순수 산출
// 함수들(ReadZeroPct·PctOf·FallbackPct)로 스냅숏한다.
package ops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// parityFake — 파생 경로 fake: daily:* 는 신규 데이터처럼 공란, read_events
// 만 반환(픽스처: read 10·zero 3·near 1·fallback 2 — 전부 오늘).
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
			return nil, nil // 신규 데이터 — daily read 필드 부재
		case strings.Contains(sql, "near FROM read_events"):
			return mergeRows, nil // WindowedDaily 파생 병합
		case strings.Contains(sql, "first_rank FROM read_events"):
			return effectRows, nil // CollectEffect(G4)
		case strings.Contains(sql, "FROM meta"):
			return nil, nil // hot_stats·thresholds 부재 — 기본값
		case strings.Contains(sql, "hot_events"),
			strings.Contains(sql, "session_starts"),
			strings.Contains(sql, "ingested_at >="),
			strings.Contains(sql, "id, topic FROM knowledge"):
			return nil, nil
		}
		return [][]any{{int64(0), int64(0), int64(0)}}, nil // 집계 0
	}}
}

// TestEffectParity — Review Focus: 픽스처 read 10회 중 zero 3·near 1에서
// blob 경로 기준선(구 산식) == 파생 경로 report 출력.
func TestEffectParity(t *testing.T) {
	const reads, zeros, nears, fallbacks = 10, 3, 1, 2
	// ── 전환 전 기준선 스냅숏(구 blob 경로 산식) ──
	// zero_pct·zero_near_pct: daily 합산 버림 정수%(v0.15.0 R2).
	legacyZeroPct := ReadZeroPct(reads, zeros)
	legacyNearPct := ReadZeroPct(reads, nears)
	// effect hit·near: 동일 합산에서 유도.
	legacyHitPct := PctOf(reads-zeros, reads)
	legacyEffectNear := PctOf(nears, reads)
	// fallback_pct: 구 정의 read_fallback_calls/read_stats calls(누적).
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
	// 일별 행도 파생 값(마지막 행 = 오늘).
	if len(rep.Days) != 1 {
		t.Fatalf("days 길이=%d, want 1", len(rep.Days))
	}
	d := rep.Days[0]
	if d.Read != reads || d.ReadZero != zeros || d.ReadNear != nears {
		t.Errorf("days[0] read/zero/near = %d/%d/%d, want %d/%d/%d",
			d.Read, d.ReadZero, d.ReadNear, reads, zeros, nears)
	}
}

// TestZeroNearParity — doctor zero-near 판정 동등성: 같은 픽스처에서 구
// 산식(near*100/read, 표본 하한)과 WindowedDaily 파생 경로의 판정이 동일.
func TestZeroNearParity(t *testing.T) {
	const reads, nears = 10, 1
	// 구 blob 경로 산식 기준선 — 표본 10 ≥ min 10, 10% < warn 30 → ok.
	legacyPct := nears * 100 / reads
	legacyStatus := "ok"
	legacyDetail := "zero_near_pct 10% (7일 read 10)" // 구 포맷 그대로
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
