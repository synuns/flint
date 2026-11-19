// hot_test.go — hot 헤드라인 선정 순위에 감쇠 후 유효 회상
// 점수(effective recall score) 반영. 정렬 계약: effective DESC 안정 정렬 —
// 동점(및 0점 전원)은 기존 SQL 순서(ts DESC, id DESC)를 승계하고 주입
// 포맷·바이트 상한은 불변이다.
package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/query"
)

// TestEventEffectiveScore — 이벤트 카운트 기반 유효 점수 순수 함수
// (v1.9.0 T1): max(0, count − rate×경과일수), 미회상(count 0)=0.
// 경과일수는 24h 단위 정수(23h59m = 0일), 결과 소수는 0 방향 내림,
// 미래 last_recalled는 역성장 방어로 count 그대로.
func TestEventEffectiveScore(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name  string
		count int
		last  time.Time
		rate  float64
		want  int
	}{
		// 계약 예시 4종
		{"계약: 3회 2일전 rate1", 3, now.Add(-2 * day), 1.0, 1},
		{"계약: 1회 5일전 rate0.3 → 음수 하한 0", 1, now.Add(-5 * day), 0.3, 0},
		{"계약: 미회상(count 0)", 0, time.Time{}, 1.0, 0},
		{"계약: 오늘(0일) 회상", 5, now, 2.0, 5},
		// 경계
		{"0일: 23h59m 경과는 감쇠 없음", 3, now.Add(-day + time.Minute), 1.0, 3},
		{"0일: 정확히 24h 경과는 1일", 3, now.Add(-day), 1.0, 2},
		{"정확히 0되는 날: 4회 4일전 rate1", 4, now.Add(-4 * day), 1.0, 0},
		{"소수 내림: 5−1.5=3.5 → 3", 5, now.Add(-day), 1.5, 3},
		{"소수 내림: 5−2.5=2.5 → 2", 5, now.Add(-2 * day), 1.25, 2},
		{"대량 카운트: 100만−0.5×10", 1000000, now.Add(-10 * day), 0.5, 999995},
		{"미래 시각 방어: last가 now+2일", 3, now.Add(2 * day), 1.0, 3},
		{"rate 0: 감쇠 없음", 3, now.Add(-5 * day), 0.0, 3},
		{"음수 rate 방어: 부풀림 금지", 3, now.Add(-5 * day), -1.0, 3},
		{"last zero + count>0: 감쇠 없음", 7, time.Time{}, 1.0, 7},
		{"음수 count 방어: 0", -2, now, 1.0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EventEffectiveScore(tc.count, tc.last, now, tc.rate); got != tc.want {
				t.Fatalf("EventEffectiveScore(%d, %v, rate %v) = %d, want %d",
					tc.count, tc.last, tc.rate, got, tc.want)
			}
		})
	}
}

// hotRankedFixture — 랭킹 열(파생 cnt·last_at — recall_events 집계 조인이
// 병합한 형태)까지 포함한 hot_view 본질의 9열 합성 픽스처. fake 반환
// 순서 = SQL 기존 순서(ts DESC, id DESC) — ts는 전 행 동일(동일 최신성)로
// 둬 점수만 변별. v1.9.0 T4: 미회상(이벤트 없음)은 cnt NULL → 0점 하단.
func hotRankedFixture(now time.Time) [][]any {
	ts := now.Truncate(time.Second)
	return [][]any{
		// id=9: 이벤트 10개·10일 전 최종 회상 → 유효 0(10−10)
		{"decision", "감쇠후보", "요약9", ts, int32(9), ts, ts, int32(10), now.Add(-10 * 24 * time.Hour)},
		// id=7: 미회상(cnt NULL) → 0
		{"decision", "중간점수", "요약7", ts, int32(7), ts, ts, nil, nil},
		// id=3: 이벤트 9개·1시간 전 회상 → 9 유지(24h 미만 감쇠 없음)
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

// TestHotFullRanksByEffectiveScore — --full 주입 행 순서가 감쇠 후 유효
// 점수 내림(9·0·0 — 0점 동점은 SQL 순서 승계)이고, 주문=증분 계약 그대로
// 증분 id도 같은 순서다. v1.9.0 T4: 미회상(cnt NULL)=0점.
func TestHotFullRanksByEffectiveScore(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	f := hotRankedStore(t, hotRankedFixture(time.Now().UTC()))
	var out bytes.Buffer
	if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	wantOrder := []string{"최고점수", "감쇠후보", "중간점수"}
	if len(lines) != len(wantOrder) {
		t.Fatalf("행 수 = %d, want %d: %q", len(lines), len(wantOrder), out.String())
	}
	for i, want := range wantOrder {
		if !strings.Contains(lines[i], want) {
			t.Errorf("행 %d = %q, want %q 포함(유효 점수 내림 9·0·0)", i, lines[i], want)
		}
	}
	// 주문=증분: 랭킹 순서 그대로 BumpReads·RecordHotEvent.
	if len(f.bumped) != 3 || f.bumped[0] != 3 || f.bumped[1] != 9 || f.bumped[2] != 7 {
		t.Errorf("BumpReads ids = %v, want [3 9 7](랭킹 순서 승계)", f.bumped)
	}
	if len(f.events) != 1 || f.events[0] != "3,9,7" {
		t.Errorf("RecordHotEvent = %v, want [3,9,7]", f.events)
	}
}

// hotHeadlineFixture — 헤드라인 질의(HotHeadlineSQL: id까지만 노출)에
// hotRankedSQL이 병합한 실제 7열 형태 픽스처(kind,topic,summary,ts,id,
// cnt,last_at — --full 9열 픽스처를 그대로 쓰면 헤드라인 랭킹 인덱스
// 부정합이 가려진다). v1.9.0 T4: 미회상(이벤트 없음)은 cnt NULL → 0점.
func hotHeadlineFixture(now time.Time) [][]any {
	ts := now.Truncate(time.Second)
	return [][]any{
		// id=9: 이벤트 10개·10일 전 최종 회상 → 유효 0(10−10)
		{"decision", "감쇠후보", "요약9", ts, int32(9), int32(10), now.Add(-10 * 24 * time.Hour)},
		// id=7: 미회상(cnt NULL) → 0
		{"decision", "중간점수", "요약7", ts, int32(7), nil, nil},
		// id=3: 이벤트 9개·1시간 전 회상 → 9 유지(24h 미만 감쇠 없음)
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
	if !strings.Contains(out.String(), "recent decisions: 최고점수 · 감쇠후보 · 중간점수") {
		t.Errorf("헤드라인 주제 순서 = %q, want 유효 점수 내림 나열", out.String())
	}
}

// TestHotRankZeroScoresKeepExistingOrder — 랭킹 열이 없는 행(구형 뷰 모양 7열)과 전원 0점 행은
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

// TestHotRankUnrecalledSinksToZero — v1.9.0 T4 시맨틱 변경 실측: 이벤트
// 없음(cnt NULL·미회상)은 0점 — 감쇠 중인 회상 이력 항목보다 아래(하단).
// 0점 동점 미회상 둘은 SQL 순서(ts DESC, id DESC)를 안정 정렬으로 승계.
func TestHotRankUnrecalledSinksToZero(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ts := now.Truncate(time.Second)
	rows := [][]any{
		{"decision", "회상있음", "요약", ts, int32(11), ts, ts, int32(9), now.Add(-5 * 24 * time.Hour)}, // 9-5=4
		{"decision", "미회상고점", "요약", ts, int32(5), ts, ts, nil, nil},                               // 이벤트 없음 → 0
		{"decision", "미회상저점", "요약", ts, int32(2), ts, ts, nil, nil},                               // 이벤트 없음 → 0
	}
	f := hotRankedStore(t, rows)
	var out bytes.Buffer
	if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
	}
	wantOrder := []string{"회상있음", "미회상고점", "미회상저점"} // 4 > 0 = 0(동점 SQL 순서)
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	for i, want := range wantOrder {
		if i >= len(lines) || !strings.Contains(lines[i], want) {
			t.Errorf("행 %d = %v, want %q 포함(감쇠후 4·미회상 0·미회상 0 순)", i, lines, want)
		}
	}
}

// TestHotRankedSQLShape — 단일 정의(query.HotSQL·HotHeadlineSQL) 치환 계약:
// SELECT 열은 h. 한정 + 말미에 recall_events 파생 집계 조인(re.cnt·
// re.last_at) 병합, ORDER BY·LIMIT은 원본을 승계한다(v1.9.0 T4 — v11
// k.recall_score 직접 열 참조 폐지). 치환점 미발견 형태는 원본 반환
// (랭킹 조용히 생략 — 기존 동작 승계).
func TestHotRankedSQLShape(t *testing.T) {
	derived := "LEFT JOIN (SELECT knowledge_id, count(*)::int AS cnt, max(recalled_at) AS last_at FROM recall_events GROUP BY knowledge_id) re ON re.knowledge_id = h.id"
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
				"re.cnt, re.last_at",
				"FROM hot_view h " + derived,
				"ORDER BY h.ts DESC, h.id DESC",
			} {
				if !strings.Contains(got, frag) {
					t.Errorf("ranked SQL에 %q 누락: %s", frag, got)
				}
			}
			// v11 직접 열 참조 잔존 방어 — knowledge 조인·컬럼 폐지.
			if strings.Contains(got, "k.recall_score") || strings.Contains(got, "LEFT JOIN knowledge k") {
				t.Errorf("v11 직접 열 참조 잔존: %s", got)
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
// (thresholds 계약 승계 — 파생 전환 후에도 불변): decay_rate=0.5에서
// 이벤트 10개·4일 전 최종 회상은 유효 8로 이벤트 7개·오늘 회상보다 위.
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

// 감쇠 랭킹 부분 정렬 회귀: 초기 순서가 우연히 정답과 같으면
// 사전 계산 eff 슬라이스의 어긋남이 드러나지 않는다(감사 재현 케이스).
func TestHotRankRowsEffectiveOrderNotAccidental(t *testing.T) {
	rows := [][]any{
		{"gamma", 5, mustTime(t, "2026-08-01T00:00:00Z")}, // eff 0 (score 5 - 100일)
		{"alpha", 1, mustTime(t, "2026-11-10T00:00:00Z")}, // eff 0 (score 1 - 2일)
		{"beta", 3, mustTime(t, "2026-11-15T00:00:00Z")},  // eff 2 (score 3 - 0일)
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

// ── v1.9.0 T4 파생 랭킹 slow 실측(격리 DB — SUPERLITE_REAL=1 +
// FLINT_TEST_DSN 설정 시만 실행, 합성 데이터만). ──

// hotRealStore — hot·read 실행에 쓰는 실DB 어댑터(writeRealStore 재사용:
// db.Repo 계측·커밋 + 풀 Query).
func hotRealStore(t *testing.T) *writeRealStore {
	t.Helper()
	dsn, ok := writeRealDSN()
	if !ok {
		t.Skip("SUPERLITE_REAL=1 + FLINT_TEST_DSN required")
	}
	ctx := context.Background()
	repo, err := db.NewRepo(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		repo.Close()
		t.Skipf("real pool unavailable: %v", err)
	}
	st := &writeRealStore{Repo: repo, pool: pool}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 실패: %v", err)
	}
	return st
}

// hotSynthCleanup — 태그 접두사로 표시한 합성 지식·회상 이벤트 정리.
// 진입 시 선행 삭제(중단된 이전 런 잔존 행의 content_hash 중복으로
// InsertBatch가 간헐 실패하는 일 방지) + t.Cleanup 재정리(LIFO — 어댑터
// Close보다 먼저 등록해 닫히기 전에 실행).
func hotSynthCleanup(t *testing.T, st *writeRealStore, prefix string) {
	t.Helper()
	purge := func() error {
		ctx := context.Background()
		if _, err := st.pool.Exec(ctx,
			`DELETE FROM recall_events WHERE knowledge_id IN
			 (SELECT id FROM knowledge WHERE topic LIKE $1)`, prefix+"%"); err != nil {
			return err
		}
		_, err := st.pool.Exec(ctx,
			`DELETE FROM knowledge WHERE topic LIKE $1`, prefix+"%")
		return err
	}
	if err := purge(); err != nil {
		t.Fatalf("합성 잔존 정리(%s): %v", prefix, err)
	}
	t.Cleanup(func() {
		if err := purge(); err != nil {
			t.Errorf("합성 정리(%s): %v", prefix, err)
		}
	})
}

// hotRec — 합성 지식 레코드(db 테스트 knowledgeRec 관례 — 개인 데이터
// 부재). status: hot_view 노출은 'pending'(decision 팔은 :adr: 태그+7일
// 조건부), read 조회는 'active'.
func hotRec(topic, hash, status string) map[string]any {
	return map[string]any{
		"kind":         "knowledge",
		"topic":        topic,
		"summary":      "요약-" + hash,
		"tags":         ":t4v1900:",
		"confidence":   0.9,
		"content_hash": hash,
		"status":       status,
	}
}

// TestHotDerivedRankingRealSlow — 파생 랭킹 전환 실측(schema v12 실DB):
// 이벤트 적립 회상 점수가 랭킹에 감쇠 반영되고(A: 이벤트 3·2일 전 → 유효 1
// 최상), 감쇠 소진(B: 이벤트 1·5일 전 → 0)과 미회상(C: 이벤트 0 → cnt
// NULL)은 0점 하단(0점 동점은 SQL 순서 승계). 삽입 순서 A→B→C로 SQL
// 초기 순서는 C,B,A — 랭킹이 초기 순서를 뒤집는지도 함께 검증(R36 부분
// 정렬 회귀와 같은 취지의 실DB 변형).
func TestHotDerivedRankingRealSlow(t *testing.T) {
	st := hotRealStore(t)
	const prefix = "t4-v1900-rank-"
	hotSynthCleanup(t, st, prefix)
	injectHotSeams(t)
	ctx := context.Background()
	// 감쇠 계수 기본(1.0) 고정 — 타 테스트가 남긴 meta 오버라이드 배제.
	if _, err := st.pool.Exec(ctx, `DELETE FROM meta WHERE key = 'decay_rate'`); err != nil {
		t.Fatalf("decay_rate 오버라이드 초기화: %v", err)
	}
	ids, err := st.InsertBatch(ctx, []map[string]any{
		hotRec(prefix+"A-최근다회상", "t4-v1900-rank-a", "pending"),
		hotRec(prefix+"B-오래감쇠", "t4-v1900-rank-b", "pending"),
		hotRec(prefix+"C-미회상", "t4-v1900-rank-c", "pending"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	// 회상 이벤트 — A 3건(2일 전)·B 1건(5일 전)·C 0건.
	for _, e := range []struct {
		id int
		n  int
		d  string
	}{
		{ids[0], 3, "2"},
		{ids[1], 1, "5"},
	} {
		if _, err := st.pool.Exec(ctx,
			`INSERT INTO recall_events (knowledge_id, recalled_at)
			 SELECT $1::int, now() - ($2 || ' days')::interval FROM generate_series(1, $3::int)`, e.id, e.d, e.n); err != nil {
			t.Fatalf("이벤트 적립 id=%d: %v", e.id, err)
		}
	}
	var out bytes.Buffer
	if code := RunHot(ctx, st, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
	}
	idxA := strings.Index(out.String(), prefix+"A")
	idxB := strings.Index(out.String(), prefix+"B")
	idxC := strings.Index(out.String(), prefix+"C")
	if idxA < 0 || idxB < 0 || idxC < 0 {
		t.Fatalf("행 누락(A@%d B@%d C@%d): %q", idxA, idxB, idxC, out.String())
	}
	// 유효 점수 내림: A(1) 최상, B(0)·C(0) 동점은 SQL 순서 승계(C가 B보다
	// 최신 수집 — 미회상 C·감쇠 소진 B 모두 0점 하단).
	if !(idxA < idxC && idxC < idxB) {
		t.Errorf("랭킹 순서 = A@%d C@%d B@%d, want A < C < B(파생 감쇠 1·0·0 — 초기 SQL 순서 C,B,A에서 A가 최상으로 뒤집힘)",
			idxA, idxC, idxB)
	}
}

// TestReadJSONSchemaUnchangedRealSlow — 파생 전환 후에도 read --json 출력
// 스키마는 동일(행별 {id,topic,summary} 문자열 맵 — FormatRows 기본
// fields). 회상 이벤트가 있는 상태에서 기계 채널 불변을 실측한다.
func TestReadJSONSchemaUnchangedRealSlow(t *testing.T) {
	st := hotRealStore(t)
	const prefix = "t4-v1900-json-"
	hotSynthCleanup(t, st, prefix)
	ctx := context.Background()
	ids, err := st.InsertBatch(ctx, []map[string]any{
		hotRec(prefix+"토픽", "t4-v1900-json-a", "active"),
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := st.BumpRecallScores(ctx, ids); err != nil {
		t.Fatalf("BumpRecallScores: %v", err)
	}
	var out bytes.Buffer
	if code := RunRead(ctx, st, []string{prefix + "토픽", "--json"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunRead --json exit = %d: %s", code, out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("행 수 = %d, want 1: %q", len(lines), out.String())
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("JSON 파싱: %v: %q", err, lines[0])
	}
	want := map[string]bool{"id": true, "topic": true, "summary": true}
	if len(m) != len(want) {
		t.Errorf("JSON 키 = %v, want %v(스키마 동치)", m, want)
	}
	for k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("JSON 키 %q 누락: %v", k, m)
		}
	}
	if !strings.Contains(m["topic"], prefix) {
		t.Errorf("topic = %q, want 접두사 %q 포함", m["topic"], prefix)
	}
}

// hotBenchP95 — durations 밀리초 p95(올림 인덱스 — PERF.md 중앙값·p95 관례).
func hotBenchP95(ms []float64) float64 {
	s := append([]float64(nil), ms...)
	sort.Float64s(s)
	i := int(float64(len(s)) * 0.95)
	if i < 1 {
		i = 1
	}
	return s[i-1]
}

// TestHotDerivedRankingBenchRealSlow — 파생 랭킹 벤치(docs/PERF.md 관례:
// 격리 DB·합성 지식 100·이벤트 1만·3회 중앙값). v11 직접 열 형태(대조용
// 합성 테이블 bench_k11 — 폐기된 knowledge.recall_score·last_recalled_at
// 열의 동일 분포 재현)와 v12 파생 집계 조인의 hot --full 랭킹 질의 p95를
// 비교한다 — 스펙 요구: v11 대비 2배 미만.
func TestHotDerivedRankingBenchRealSlow(t *testing.T) {
	st := hotRealStore(t)
	const prefix = "t4-v1900-bench-"
	hotSynthCleanup(t, st, prefix)
	t.Cleanup(func() { // 테이블 정리(LIFO — 행 정리보다 먼저).
		if _, err := st.pool.Exec(context.Background(), `DROP TABLE IF EXISTS bench_k11`); err != nil {
			t.Errorf("bench_k11 정리: %v", err)
		}
	})
	ctx := context.Background()
	// 합성 시드 — 지식 100행 · 지식당 이벤트 100건(총 1만).
	recs := make([]map[string]any, 0, 100)
	for i := 0; i < 100; i++ {
		recs = append(recs, hotRec(
			prefix+"토픽", fmt.Sprintf("t4-v1900-bench-%03d", i), "pending"))
	}
	if _, err := st.InsertBatch(ctx, recs); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO recall_events (knowledge_id, recalled_at)
		 SELECT k.id, now() - (random()*30 || ' days')::interval
		 FROM knowledge k, generate_series(1, 100) g
		 WHERE k.topic LIKE $1`, prefix+"%"); err != nil {
		t.Fatalf("이벤트 시드: %v", err)
	}
	// v11 대조군 — 폐기 직접 열의 동일 분포 재현 테이블.
	if _, err := st.pool.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS bench_k11 (
		     knowledge_id integer NOT NULL,
		     recall_score integer NOT NULL,
		     last_recalled_at timestamptz)`); err != nil {
		t.Fatalf("bench_k11 생성: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO bench_k11
		 SELECT knowledge_id, count(*)::int, max(recalled_at)
		 FROM recall_events GROUP BY knowledge_id`); err != nil {
		t.Fatalf("bench_k11 적재: %v", err)
	}
	derivedSQL := hotRankedSQL(query.HotSQL(""))
	v11SQL := "SELECT h.kind, h.topic, h.summary, h.ts, h.id, h.valid_at, h.ingested_at," +
		" k.recall_score, k.last_recalled_at FROM hot_view h" +
		" LEFT JOIN bench_k11 k ON h.id = k.knowledge_id" +
		" ORDER BY h.ts DESC, h.id DESC LIMIT 10"
	const warmup, samples = 5, 30
	run := func(sql string) float64 {
		for i := 0; i < warmup; i++ {
			if _, err := st.Query(ctx, sql); err != nil {
				t.Fatalf("warmup 질의: %v", err)
			}
		}
		ms := make([]float64, 0, samples)
		for i := 0; i < samples; i++ {
			start := time.Now()
			if _, err := st.Query(ctx, sql); err != nil {
				t.Fatalf("벤치 질의: %v", err)
			}
			ms = append(ms, float64(time.Since(start).Microseconds())/1000)
		}
		return hotBenchP95(ms)
	}
	// 3회 중앙값(PERF.md §1 관례).
	p95 := func(sql string) (float64, float64, float64) {
		a, b, c := run(sql), run(sql), run(sql)
		meds := []float64{a, b, c}
		sort.Float64s(meds)
		return meds[1], a, c
	}
	derivedMed, dMin, dMax := p95(derivedSQL)
	v11Med, vMin, vMax := p95(v11SQL)
	t.Logf("파생 랭킹 p95 중앙값 %.2fms (3회 %.2f·%.2f·%.2f) vs v11 직접 열 %.2fms (3회 %.2f·%.2f·%.2f) — 비율 %.2fx",
		derivedMed, dMin, derivedMed, dMax, v11Med, vMin, v11Med, vMax, derivedMed/v11Med)
	if ratio := derivedMed / v11Med; ratio >= 2 {
		t.Errorf("파생 랭킹 p95 비율 = %.2fx, want < 2x(스펙 §성능)", ratio)
	}
}
