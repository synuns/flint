// hot_test.go — reflecting the decay-adjusted effective recall score in
// the hot headline selection ranking. Sorting contract: effective DESC
// stable sort — ties (and an all-zero set) inherit the existing SQL
// order (ts DESC, id DESC), and the injection format·byte cap are
// unchanged.
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

// TestEventEffectiveScore — the event-count-based effective score pure
// function (v1.9.0 T1): max(0, count − rate×elapsed days),
// never-recalled (count 0) = 0. Elapsed days is an integer in 24h units
// (23h59m = 0 days); a fractional result floors toward 0; a future
// last_recalled keeps count as-is (reverse-growth defense).
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
		// 4 contract examples
		{"계약: 3회 2일전 rate1", 3, now.Add(-2 * day), 1.0, 1},
		{"계약: 1회 5일전 rate0.3 → 음수 하한 0", 1, now.Add(-5 * day), 0.3, 0},
		{"계약: 미회상(count 0)", 0, time.Time{}, 1.0, 0},
		{"계약: 오늘(0일) 회상", 5, now, 2.0, 5},
		// boundaries
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

// hotRankedFixture — a 9-column synthetic fixture of the hot_view base
// query, including the ranking columns (derived cnt·last_at — the shape
// the recall_events aggregate join merged). Fake return order = the
// existing SQL order (ts DESC, id DESC) — ts is identical across rows
// (equal recency) so only the score differentiates. v1.9.0 T4:
// never-recalled (no events) has cnt NULL → zero score, bottom.
func hotRankedFixture(now time.Time) [][]any {
	ts := now.Truncate(time.Second)
	return [][]any{
		// id=9: 10 events·last recall 10 days ago → effective 0 (10−10)
		{"decision", "감쇠후보", "요약9", ts, int32(9), ts, ts, int32(10), now.Add(-10 * 24 * time.Hour)},
		// id=7: never recalled (cnt NULL) → 0
		{"decision", "중간점수", "요약7", ts, int32(7), ts, ts, nil, nil},
		// id=3: 9 events·recalled 1 hour ago → 9 kept (under 24h, no decay)
		{"decision", "최고점수", "요약3", ts, int32(3), ts, ts, int32(9), now.Add(-time.Hour)},
	}
}

// hotRankedStore — a fake returning the 9-column fixture for the
// hot_view ranking query (meta queries unset — default decay coefficient
// 1·default budget).
func hotRankedStore(t *testing.T, rows [][]any) *hotBumpRecorder {
	t.Helper()
	return &hotBumpRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM hot_view") {
			return rows, nil
		}
		return nil, nil // meta (thresholds·hot_budget·hot_stats): unset
	}}}
}

// TestHotFullRanksByEffectiveScore — the --full injection row order is
// the decay-adjusted effective score, descending (9·0·0 — zero-score
// ties inherit the SQL order), and the increment ids follow the same
// order under the order=increment contract. v1.9.0 T4: never-recalled
// (cnt NULL) = zero score.
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
	// Order = increment: BumpReads·RecordHotEvent follow the ranking
	// order as-is.
	if len(f.bumped) != 3 || f.bumped[0] != 3 || f.bumped[1] != 9 || f.bumped[2] != 7 {
		t.Errorf("BumpReads ids = %v, want [3 9 7](랭킹 순서 승계)", f.bumped)
	}
	if len(f.events) != 1 || f.events[0] != "3,9,7" {
		t.Errorf("RecordHotEvent = %v, want [3,9,7]", f.events)
	}
}

// hotHeadlineFixture — a fixture in the actual 7-column shape the
// headline query (HotHeadlineSQL: exposes through id only) gets after
// hotRankedSQL's merge (kind,topic,summary,ts,id,cnt,last_at — reusing
// the --full 9-column fixture would hide the headline ranking-index
// mismatch). v1.9.0 T4: never-recalled (no events) has cnt NULL → zero
// score.
func hotHeadlineFixture(now time.Time) [][]any {
	ts := now.Truncate(time.Second)
	return [][]any{
		// id=9: 10 events·last recall 10 days ago → effective 0 (10−10)
		{"decision", "감쇠후보", "요약9", ts, int32(9), int32(10), now.Add(-10 * 24 * time.Hour)},
		// id=7: never recalled (cnt NULL) → 0
		{"decision", "중간점수", "요약7", ts, int32(7), nil, nil},
		// id=3: 9 events·recalled 1 hour ago → 9 kept (under 24h, no decay)
		{"decision", "최고점수", "요약3", ts, int32(3), int32(9), now.Add(-time.Hour)},
	}
}

// TestHotHeadlineRanksTopics — the headline path ranks identically: the
// recent decisions topic listing order is the effective score,
// descending. The fixture guards that ranking works only in the actual
// headline query shape (7 columns — ranking column idx 5·6).
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

// TestHotRankZeroScoresKeepExistingOrder — rows without ranking columns
// (an old-view 7-column shape) and an all-zero row set: the stable sort
// inherits the SQL order as-is and the output is byte-identical to the
// base format.
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

// TestHotRankUnrecalledSinksToZero — the v1.9.0 T4 semantics change,
// measured: no events (cnt NULL·never recalled) is a zero score — below
// items with decaying recall history (bottom). The two zero-score
// never-recalled ties inherit the SQL order (ts DESC, id DESC) via the
// stable sort.
func TestHotRankUnrecalledSinksToZero(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ts := now.Truncate(time.Second)
	rows := [][]any{
		{"decision", "회상있음", "요약", ts, int32(11), ts, ts, int32(9), now.Add(-5 * 24 * time.Hour)}, // 9-5=4
		{"decision", "미회상고점", "요약", ts, int32(5), ts, ts, nil, nil},                               // no events → 0
		{"decision", "미회상저점", "요약", ts, int32(2), ts, ts, nil, nil},                               // no events → 0
	}
	f := hotRankedStore(t, rows)
	var out bytes.Buffer
	if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
	}
	wantOrder := []string{"회상있음", "미회상고점", "미회상저점"} // 4 > 0 = 0 (ties keep the SQL order)
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	for i, want := range wantOrder {
		if i >= len(lines) || !strings.Contains(lines[i], want) {
			t.Errorf("행 %d = %v, want %q 포함(감쇠후 4·미회상 0·미회상 0 순)", i, lines, want)
		}
	}
}

// TestHotRankedSQLShape — the single-definition (query.HotSQL·
// HotHeadlineSQL) substitution contract: SELECT columns are h.-
// qualified + the recall_events derived aggregate join (re.cnt·
// re.last_at) merged at the tail; ORDER BY·LIMIT inherit the original
// (v1.9.0 T4 — the v11 k.recall_score direct-column reference
// abolished). A shape without the pivot returns the original (ranking
// silently skipped — existing behavior inherited).
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
			// Defense against residual v11 direct-column references — the
			// knowledge join·columns abolished.
			if strings.Contains(got, "k.recall_score") || strings.Contains(got, "LEFT JOIN knowledge k") {
				t.Errorf("v11 직접 열 참조 잔존: %s", got)
			}
			// LIMIT·the row cap inherit the original definitions.
			if !strings.HasSuffix(got, tc.base[strings.LastIndex(tc.base, " LIMIT"):]) {
				t.Errorf("LIMIT 절 미승계: %s", got)
			}
		})
	}
	if got := hotRankedSQL("SELECT x FROM other ORDER BY whatever"); got != "SELECT x FROM other ORDER BY whatever" {
		t.Errorf("치환점 미발견 시 원본 반환 위반: %s", got)
	}
}

// TestHotRankDecayRateMetaOverride — the decay coefficient prefers the
// meta override (thresholds contract inherited — unchanged even after
// the derived switch): with decay_rate=0.5, 10 events·last recall 4
// days ago yields an effective 8, above 7 events·recalled today.
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
	idxSlow := strings.Index(out.String(), "완만감쇠") // effective 8
	idxLow := strings.Index(out.String(), "저점")    // effective 7
	if idxSlow < 0 || idxLow < 0 || idxSlow > idxLow {
		t.Errorf("순서 = 완만감쇠@%d 저점@%d, want 완만감쇠 선행(0.5 계수: 8 > 7): %q", idxSlow, idxLow, out.String())
	}
}

// A decay-ranking partial-sort regression: when the initial order
// coincidentally matches the answer, a misaligned precomputed eff slice
// stays hidden (an audit-reproduction case).
func TestHotRankRowsEffectiveOrderNotAccidental(t *testing.T) {
	rows := [][]any{
		{"gamma", 5, mustTime(t, "2026-08-01T00:00:00Z")}, // eff 0 (score 5 - 100 days)
		{"alpha", 1, mustTime(t, "2026-11-10T00:00:00Z")}, // eff 0 (score 1 - 2 days)
		{"beta", 3, mustTime(t, "2026-11-15T00:00:00Z")},  // eff 2 (score 3 - 0 days)
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

// ── The v1.9.0 T4 derived-ranking slow measurement (isolated DB — runs
// only with SUPERLITE_REAL=1 + FLINT_TEST_DSN set, synthetic data
// only). ──

// hotRealStore — the real-DB adapter for running hot·read (reuses
// writeRealStore: db.Repo metering·commits + pool Query).
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

// hotSynthCleanup — cleans up the synthetic knowledge·recall events
// marked by the tag prefix. A leading delete on entry (preventing
// occasional InsertBatch failures from content_hash duplicates of rows
// left by an interrupted prior run) + a t.Cleanup re-purge (LIFO —
// registered before the adapter Close so it runs before closing).
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

// hotRec — a synthetic knowledge record (the db-test knowledgeRec
// convention — no personal data). status: 'pending' for hot_view
// exposure (the decision arm requires the :adr: tag + a 7-day
// condition), 'active' for read queries.
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

// TestHotDerivedRankingRealSlow — the derived-ranking switch, measured
// (schema v12 real DB): event-accrued recall scores are reflected in
// ranking with decay (A: 3 events·2 days ago → effective 1, top), while
// decay-exhausted (B: 1 event·5 days ago → 0) and never-recalled (C: 0
// events → cnt NULL) sit at the zero-score bottom (zero ties inherit
// the SQL order). Insert order A→B→C makes the initial SQL order C,B,A —
// also verifying that ranking flips the initial order (the real-DB
// variant of the R36 partial-sort regression, same intent).
func TestHotDerivedRankingRealSlow(t *testing.T) {
	st := hotRealStore(t)
	const prefix = "t4-v1900-rank-"
	hotSynthCleanup(t, st, prefix)
	injectHotSeams(t)
	ctx := context.Background()
	// The decay coefficient pinned to the default (1.0) — excluding meta
	// overrides left by other tests.
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
	// Recall events — A ×3 (2 days ago)·B ×1 (5 days ago)·C ×0.
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
	// Effective score descending: A (1) top; the B(0)·C(0) ties inherit
	// the SQL order (C was ingested more recently than B — both the
	// never-recalled C·the decay-exhausted B sit at the zero-score
	// bottom).
	if !(idxA < idxC && idxC < idxB) {
		t.Errorf("랭킹 순서 = A@%d C@%d B@%d, want A < C < B(파생 감쇠 1·0·0 — 초기 SQL 순서 C,B,A에서 A가 최상으로 뒤집힘)",
			idxA, idxC, idxB)
	}
}

// TestReadJSONSchemaUnchangedRealSlow — even after the derived switch,
// the read --json output schema is identical (a per-row {id,topic,
// summary} string map — the FormatRows default fields). Measures the
// machine-channel invariance in the presence of recall events.
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

// hotBenchP95 — the p95 of durations in milliseconds (ceiling index —
// the PERF.md median·p95 convention).
func hotBenchP95(ms []float64) float64 {
	s := append([]float64(nil), ms...)
	sort.Float64s(s)
	i := int(float64(len(s)) * 0.95)
	if i < 1 {
		i = 1
	}
	return s[i-1]
}

// TestHotDerivedRankingBenchRealSlow — the derived-ranking benchmark
// (the docs/PERF.md convention: isolated DB·100 synthetic knowledge
// rows·10k events·3-run median). Compares the hot --full ranking query
// p95 of the v11 direct-column shape (a synthetic comparison table
// bench_k11 — reproducing the same distribution of the dropped
// knowledge.recall_score·last_recalled_at columns) against the v12
// derived aggregate join — spec requirement: under 2x vs v11.
func TestHotDerivedRankingBenchRealSlow(t *testing.T) {
	st := hotRealStore(t)
	const prefix = "t4-v1900-bench-"
	hotSynthCleanup(t, st, prefix)
	t.Cleanup(func() { // table cleanup (LIFO — before the row cleanup)
		if _, err := st.pool.Exec(context.Background(), `DROP TABLE IF EXISTS bench_k11`); err != nil {
			t.Errorf("bench_k11 정리: %v", err)
		}
	})
	ctx := context.Background()
	// Synthetic seed — 100 knowledge rows · 100 events each (10k total).
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
	// The v11 control — a table reproducing the same distribution of the
	// dropped direct columns.
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
	// 3-run median (the PERF.md §1 convention).
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

// TestHotDerivedRankingAfterPruneBenchRealSlow — the v1.9.1 R42-D
// regression: the recall_events retention-prune (recall_event_
// retention_days default 90) restores the derived ranking p95 of a
// heavily accumulated event set back within the 2x contract. Synthetic
// seed of 100 knowledge × 100k events (uniform 0~900 days — reproducing
// long-term accumulation, ~10% surviving in the 90-day retention
// window)·measures the derived ranking p95 before·after the prune (the
// single db.Repo.PruneRecallEvents path), rebuilds the v11
// direct-column control (bench_k11) from the surviving events, and
// asserts the 3-run median ratio < 2x (the PERF.md convention — residual
// scale stated). The retention-window invariance is asserted alongside —
// rows whose every event lies inside the retention window keep identical
// cnt·last_at before·after the prune (the ranking source unchanged).
func TestHotDerivedRankingAfterPruneBenchRealSlow(t *testing.T) {
	st := hotRealStore(t)
	const prefix = "t4-v1910-prune-"
	hotSynthCleanup(t, st, prefix)
	t.Cleanup(func() {
		if _, err := st.pool.Exec(context.Background(), `DROP TABLE IF EXISTS bench_k11`); err != nil {
			t.Errorf("bench_k11 정리: %v", err)
		}
	})
	ctx := context.Background()
	// Synthetic seed — 100 ranking rows (1000 events each × 0~900 days) +
	// 1 retention-window row (3 events·2 days ago — 0 expired, the
	// prune-invariance assertion target).
	recs := make([]map[string]any, 0, 101)
	for i := 0; i < 100; i++ {
		recs = append(recs, hotRec(
			prefix+"토픽", fmt.Sprintf("t4-v1910-prune-%03d", i), "pending"))
	}
	recs = append(recs, hotRec(prefix+"보존", "t4-v1910-prune-fresh", "pending"))
	ids, err := st.InsertBatch(ctx, recs)
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	freshID := ids[len(ids)-1]
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO recall_events (knowledge_id, recalled_at)
		 SELECT k.id, now() - (random()*900 || ' days')::interval
		 FROM knowledge k, generate_series(1, 1000) g
		 WHERE k.topic LIKE $1 AND k.id <> $2`, prefix+"%", freshID); err != nil {
		t.Fatalf("이벤트 시드: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO recall_events (knowledge_id, recalled_at)
		 SELECT $1::int, now() - interval '2 days' FROM generate_series(1, 3)`, freshID); err != nil {
		t.Fatalf("보존 행 이벤트 시드: %v", err)
	}
	var seeded int
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*)::int FROM recall_events re JOIN knowledge k ON k.id = re.knowledge_id
		 WHERE k.topic LIKE $1`, prefix+"%").Scan(&seeded); err != nil {
		t.Fatalf("시드 수 확인: %v", err)
	}
	if seeded != 100*1000+3 {
		t.Fatalf("시드 이벤트 = %d, want 100003", seeded)
	}
	// Pre-prune snapshot of the retention row (cnt·last_at).
	var preCnt int
	var preLast time.Time
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*)::int, max(recalled_at) FROM recall_events WHERE knowledge_id = $1`,
		freshID).Scan(&preCnt, &preLast); err != nil {
		t.Fatalf("보존 행 스냅숏: %v", err)
	}
	derivedSQL := hotRankedSQL(query.HotSQL(""))
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
	// 3-run median (the PERF.md §1 convention).
	med := func(sql string) float64 {
		a, b, c := run(sql), run(sql), run(sql)
		meds := []float64{a, b, c}
		sort.Float64s(meds)
		return meds[1]
	}
	preDerived := med(derivedSQL) // the degraded p95 of the accumulated state (100k events)
	// prune — retention 90 days (the default-value path — no meta
	// override).
	n, err := st.PruneRecallEvents(ctx, time.Now().UTC().AddDate(0, 0, -90))
	if err != nil {
		t.Fatalf("PruneRecallEvents: %v", err)
	}
	if n <= 0 || int(n) >= seeded {
		t.Fatalf("prune 삭제 = %d(시드 %d) — 만료분만 삭제하는 성장 상한이 아님", n, seeded)
	}
	var postCnt int
	var postLast time.Time
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*)::int, max(recalled_at) FROM recall_events WHERE knowledge_id = $1`,
		freshID).Scan(&postCnt, &postLast); err != nil {
		t.Fatalf("보존 행 재조회: %v", err)
	}
	if postCnt != preCnt || !postLast.Equal(preLast) {
		t.Errorf("보존 구간 불변 위반: cnt %d→%d, last %v→%v", preCnt, postCnt, preLast, postLast)
	}
	// The v11 control — a table reproducing the same distribution of the
	// surviving events.
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
	v11SQL := "SELECT h.kind, h.topic, h.summary, h.ts, h.id, h.valid_at, h.ingested_at," +
		" k.recall_score, k.last_recalled_at FROM hot_view h" +
		" LEFT JOIN bench_k11 k ON h.id = k.knowledge_id" +
		" ORDER BY h.ts DESC, h.id DESC LIMIT 10"
	derivedMed, v11Med := med(derivedSQL), med(v11SQL)
	t.Logf("prune 전(축적 10만) 파생 p95 %.2fms → prune 후 파생 p95 중앙값 %.2fms vs v11 직접 열 %.2fms — 비율 %.2fx(잔존 이벤트 %d/%d)",
		preDerived, derivedMed, v11Med, derivedMed/v11Med, seeded-int(n), seeded)
	if ratio := derivedMed / v11Med; ratio >= 2 {
		t.Errorf("prune 후 파생 랭킹 p95 비율 = %.2fx, want < 2x(R42-D 회복 계약)", ratio)
	}
}
