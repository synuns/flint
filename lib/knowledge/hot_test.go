// hot_test.go — v1.7.0 T3: hot 헤드라인 선정 순위에 감쇠 후 유효 회상
// 점수(effective recall score) 반영. 정렬 계약: effective DESC 안정 정렬 —
// 동점(및 0점 전원)은 기존 SQL 순서(ts DESC, id DESC)를 승계하고 주입
// 포맷·바이트 상한은 불변이다.
package knowledge

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/query"
)

// hotRankedFixture — 랭킹 열(recall_score·last_recalled_at)까지 포함한
// hot_view 본질의 9열 합성 픽스처. fake 반환 순서 = SQL 기존 순서
// (ts DESC, id DESC) — ts는 전 행 동일(동일 최신성)로 둬 점수만 변별.
func hotRankedFixture(now time.Time) [][]any {
	ts := now.Truncate(time.Second)
	return [][]any{
		// id=9: score 10이지만 10일 미회상 → 감쇠 후 0
		{"decision", "감쇠후보", "요약9", ts, int32(9), ts, ts, int32(10), now.Add(-10 * 24 * time.Hour)},
		// id=7: 미회상 score 5 → 5 유지
		{"decision", "중간점수", "요약7", ts, int32(7), ts, ts, int32(5), nil},
		// id=3: 1시간 전 회상 score 9 → 9 유지(24h 미만 감쇠 없음)
		{"decision", "최고점수", "요약3", ts, int32(3), ts, ts, int32(9), now.Add(-time.Hour)},
	}
}

// hotRankedStore — hot_view 랭킹 질의에 9열 픽스처를 반환하는 fake
// (meta 질의는 미설정 — 기본 감쇠 계수 1·기본 예산).
func hotRankedStore(t *testing.T, rows [][]any) *hotBumpRecorder {
	t.Helper()
	return &hotBumpRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM hot_view") {
			return rows, nil
		}
		return nil, nil // meta(thresholds·hot_budget·hot_stats): 미설정
	}}}
}

// TestHotFullRanksByEffectiveScore — Review Focus(동일 최신성에서 score 높은
// 항목 우선): --full 주입 행 순서가 감쇠 후 유효 점수 내림(9·5·0)이고,
// 주문=증분 계약 그대로 증분 id도 같은 순서다.
func TestHotFullRanksByEffectiveScore(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	f := hotRankedStore(t, hotRankedFixture(time.Now().UTC()))
	var out bytes.Buffer
	if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	wantOrder := []string{"최고점수", "중간점수", "감쇠후보"}
	if len(lines) != len(wantOrder) {
		t.Fatalf("행 수 = %d, want %d: %q", len(lines), len(wantOrder), out.String())
	}
	for i, want := range wantOrder {
		if !strings.Contains(lines[i], want) {
			t.Errorf("행 %d = %q, want %q 포함(유효 점수 내림 9·5·0)", i, lines[i], want)
		}
	}
	// 주문=증분: 랭킹 순서 그대로 BumpReads·RecordHotEvent.
	if len(f.bumped) != 3 || f.bumped[0] != 3 || f.bumped[1] != 7 || f.bumped[2] != 9 {
		t.Errorf("BumpReads ids = %v, want [3 7 9](랭킹 순서 승계)", f.bumped)
	}
	if len(f.events) != 1 || f.events[0] != "3,7,9" {
		t.Errorf("RecordHotEvent = %v, want [3,7,9]", f.events)
	}
}

// hotHeadlineFixture — 헤드라인 질의(HotHeadlineSQL: id까지만 노출)에
// hotRankedSQL이 병합한 실제 7열 형태 픽스처(kind,topic,summary,ts,id,
// recall_score,last_recalled_at — v1.7.0 리뷰 수정: --full 9열 픽스처를
// 그대로 쓰면 헤드라인 랭킹 인덱스 부정합이 가려진다).
func hotHeadlineFixture(now time.Time) [][]any {
	ts := now.Truncate(time.Second)
	return [][]any{
		// id=9: score 10이지만 10일 미회상 → 감쇠 후 0
		{"decision", "감쇠후보", "요약9", ts, int32(9), int32(10), now.Add(-10 * 24 * time.Hour)},
		// id=7: 미회상 score 5 → 5 유지
		{"decision", "중간점수", "요약7", ts, int32(7), int32(5), nil},
		// id=3: 1시간 전 회상 score 9 → 9 유내(24h 미만 감쇠 없음)
		{"decision", "최고점수", "요약3", ts, int32(3), int32(9), now.Add(-time.Hour)},
	}
}

// TestHotHeadlineRanksTopics — 헤드라인 경로도 동일 랭킹: recent decisions
// 주제 나열 순서가 유효 점수 내림. 픽스처는 실제 헤드라인 질의 형태
// (7열 — 랭킹 열 idx 5·6)로만 랭킹이 작동함을 가드한다.
func TestHotHeadlineRanksTopics(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	f := hotRankedStore(t, hotHeadlineFixture(time.Now().UTC()))
	var out bytes.Buffer
	if code := RunHot(ctx, f, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunHot exit = %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "recent decisions: 최고점수 · 중간점수 · 감쇠후보") {
		t.Errorf("헤드라인 주제 순서 = %q, want 유효 점수 내림 나열", out.String())
	}
}

// TestHotRankZeroScoresKeepExistingOrder — Review Focus(0점 전원 기존
// 순서·포맷 회귀): 랭킹 열이 없는 행(구형 뷰 모양 7열)과 전원 0점 행은
// 안정 정렬이 SQL 순서를 그대로 승계하고 출력은 기본 포맷과 바이트 동일.
func TestHotRankZeroScoresKeepExistingOrder(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	ts := time.Now().UTC().Truncate(time.Second)
	cases := []struct {
		name string
		rows [][]any
	}{
		{"랭킹 열 부재(구형 뷰 7열)", [][]any{
			{"decision", "토픽A", "요약A", ts, int32(7), ts, ts},
			{"pending", "토픽B", "요약B", ts.Add(-time.Minute), int32(3), ts.Add(-49 * time.Hour), ts.Add(-49 * time.Hour)},
		}},
		{"전원 0점", [][]any{
			{"decision", "토픽A", "요약A", ts, int32(7), ts, ts, int32(0), nil},
			{"pending", "토픽B", "요약B", ts.Add(-time.Minute), int32(3), ts.Add(-49 * time.Hour), ts.Add(-49 * time.Hour), int32(0), nil},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := hotRankedStore(t, tc.rows)
			var out bytes.Buffer
			if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
				t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
			}
			if got, want := out.String(), hotFullOutput(hotSuffixedLines(tc.rows, time.Now().UTC())); got != want {
				t.Errorf("출력 = %q, want %q(기존 순서·포맷 불변)", got, want)
			}
			idxA := strings.Index(out.String(), "토픽A")
			idxB := strings.Index(out.String(), "토픽B")
			if idxA < 0 || idxB < 0 || idxA > idxB {
				t.Errorf("행 순서 = A@%d B@%d, want A 선행(기존 SQL 순서 승계)", idxA, idxB)
			}
		})
	}
}

// TestHotRankUnrecalledKeepsScore — Review Focus(미회상 score 유지):
// last_recalled_at NULL(미회상)은 감쇠 불이익 없이 score 그대로 — 같은
// score의 회상 이력 항목(감쇠 진행)보다 위, 낮은 score보다는 아래.
func TestHotRankUnrecalledKeepsScore(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ts := now.Truncate(time.Second)
	rows := [][]any{
		{"decision", "회상있음", "요약", ts, int32(11), ts, ts, int32(9), now.Add(-5 * 24 * time.Hour)}, // 9-5=4
		{"decision", "미회상고점", "요약", ts, int32(5), ts, ts, int32(9), nil},                          // 9 유지
		{"decision", "미회상저점", "요약", ts, int32(2), ts, ts, int32(3), nil},                          // 3 유지
	}
	f := hotRankedStore(t, rows)
	var out bytes.Buffer
	if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
	}
	wantOrder := []string{"미회상고점", "회상있음", "미회상저점"} // 9 > 4 > 3
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	for i, want := range wantOrder {
		if i >= len(lines) || !strings.Contains(lines[i], want) {
			t.Errorf("행 %d = %v, want %q 포함(미회상 9·감쇠후 4·미회상 3 순)", i, lines, want)
		}
	}
}

// TestHotRankedSQLShape — 단일 정의(query.HotSQL·HotHeadlineSQL) 치환 계약:
// SELECT 열은 h. 한정 + 말미에 k.recall_score·k.last_recalled_at 병합,
// ORDER BY·LIMIT은 원본을 승계한다. 치환점 미발견 형태는 원본 반환
// (랭킹 조용히 생략 — 기존 동작 승계).
func TestHotRankedSQLShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		base string
	}{
		{"--full", query.HotSQL("")},
		{"헤드라인", query.HotHeadlineSQL("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := hotRankedSQL(tc.base)
			for _, frag := range []string{
				"h.kind, h.topic, h.summary, h.ts, h.id",
				"k.recall_score, k.last_recalled_at",
				"FROM hot_view h LEFT JOIN knowledge k ON h.id = k.id",
				"ORDER BY h.ts DESC, h.id DESC",
			} {
				if !strings.Contains(got, frag) {
					t.Errorf("ranked SQL에 %q 누락: %s", frag, got)
				}
			}
			// LIMIT·행 상한은 원본 정의 승계.
			if !strings.HasSuffix(got, tc.base[strings.LastIndex(tc.base, " LIMIT"):]) {
				t.Errorf("LIMIT 절 미승계: %s", got)
			}
		})
	}
	if got := hotRankedSQL("SELECT x FROM other ORDER BY whatever"); got != "SELECT x FROM other ORDER BY whatever" {
		t.Errorf("치환점 미발견 시 원본 반환 위반: %s", got)
	}
}

// TestHotRankDecayRateMetaOverride — 감쇠 계수는 meta 오버라이드 우선
// (thresholds 계약 승계): decay_rate=0.5에서 score 10·4일 미회상은
// 유효 8로 score 7 항목보다 위.
func TestHotRankDecayRateMetaOverride(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ts := now.Truncate(time.Second)
	rows := [][]any{
		{"decision", "저점", "요약", ts, int32(2), ts, ts, int32(7), now.Add(-time.Hour)},
		{"decision", "완만감쇠", "요약", ts, int32(1), ts, ts, int32(10), now.Add(-4 * 24 * time.Hour)},
	}
	f := &hotBumpRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM hot_view"):
			return rows, nil
		case strings.Contains(sql, "FROM meta"):
			return [][]any{{"decay_rate", "0.5"}}, nil
		}
		return nil, nil
	}}}
	var out bytes.Buffer
	if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
	}
	idxSlow := strings.Index(out.String(), "완만감쇠") // 유효 8
	idxLow := strings.Index(out.String(), "저점")    // 유효 7
	if idxSlow < 0 || idxLow < 0 || idxSlow > idxLow {
		t.Errorf("순서 = 완만감쇠@%d 저점@%d, want 완만감쇠 선행(0.5 계수: 8 > 7): %q", idxSlow, idxLow, out.String())
	}
}

// R36-1 — 감쇠 랭킹 부분 정렬 회귀: 초기 순서가 우연히 정답과 같으면
// 사전 계산 eff 슬라이스의 어긋남이 드러나지 않는다(감사 재현 케이스).
func TestHotRankRowsEffectiveOrderNotAccidental(t *testing.T) {
	rows := [][]any{
		{"gamma", 5, mustTime(t, "2026-08-01T00:00:00Z")}, // eff 0 (score 5 - 100일)
		{"alpha", 1, mustTime(t, "2026-11-10T00:00:00Z")},  // eff 0 (score 1 - 2일)
		{"beta", 3, mustTime(t, "2026-11-15T00:00:00Z")},   // eff 2 (score 3 - 0일)
	}
	got := rankRowsByEffectiveScore(rows, 1, 2, 1.0, mustTime(t, "2026-11-17T00:00:00Z"))
	if got[0][0] != "beta" || got[1][0] != "gamma" || got[2][0] != "alpha" {
		t.Fatalf("rank order = %v %v %v, want beta gamma alpha(effective DESC·동점 안정)", got[0][0], got[1][0], got[2][0])
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return v
}
