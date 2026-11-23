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

		{"계약: 3회 2일전 rate1", 3, now.Add(-2 * day), 1.0, 1},
		{"계약: 1회 5일전 rate0.3 → 음수 하한 0", 1, now.Add(-5 * day), 0.3, 0},
		{"계약: 미회상(count 0)", 0, time.Time{}, 1.0, 0},
		{"계약: 오늘(0일) 회상", 5, now, 2.0, 5},

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
			if got := EventEffectiveScore(tc.count, tc.last, now, tc.rate, 0, 0); got != tc.want {
				t.Fatalf("EventEffectiveScore(%d, %v, rate %v) = %d, want %d",
					tc.count, tc.last, tc.rate, got, tc.want)
			}
		})
	}
}

func TestEffectiveScoreForVolExemption(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	last := now.Add(-100 * 24 * time.Hour)

	if got := EffectiveScoreFor(3, last, now, 1.0, ":vol:immutable:", 0, 0); got != 3 {
		t.Errorf("immutable 면제 = %d, want 3(계약 예)", got)
	}
	if got := EffectiveScoreFor(3, last, now, 1.0, "", 0, 0); got != 0 {
		t.Errorf("태그 없음 = %d, want 0(계약 예)", got)
	}

	for _, tags := range []string{"", ":adr:", ":vol:conditional:", ":vol:volatile:", ":vol:immutable2:"} {
		want := EventEffectiveScore(3, last, now, 1.0, 0, 0)
		if got := EffectiveScoreFor(3, last, now, 1.0, tags, 0, 0); got != want {
			t.Errorf("tags %q = %d, want %d(순수 식 동치)", tags, got, want)
		}
	}

	if got := EffectiveScoreFor(5, last, now, 1.0, ":adr::vol:immutable:", 0, 0); got != 5 {
		t.Errorf("다중 태그 내 immutable = %d, want 5", got)
	}
	if got := EffectiveScoreFor(0, last, now, 1.0, ":vol:immutable:", 0, 0); got != 0 {
		t.Errorf("count 0 면제 = %d, want 0", got)
	}
	if got := EffectiveScoreFor(-1, last, now, 1.0, ":vol:immutable:", 0, 0); got != 0 {
		t.Errorf("음수 count 면제 = %d, want 0", got)
	}
}

func hotRankedFixture(now time.Time) [][]any {
	ts := now.Truncate(time.Second)
	return [][]any{

		{"decision", "감쇠후보", "요약9", ts, int32(9), ts, ts, int32(10), now.Add(-10 * 24 * time.Hour), ":adr:"},

		{"decision", "중간점수", "요약7", ts, int32(7), ts, ts, nil, nil, ":adr:"},

		{"decision", "최고점수", "요약3", ts, int32(3), ts, ts, int32(9), now.Add(-time.Hour), ":adr:"},
	}
}

func hotRankedStore(t *testing.T, rows [][]any) *hotBumpRecorder {
	t.Helper()
	return &hotBumpRecorder{fakeStore: &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM hot_view") {
			return rows, nil
		}
		return nil, nil
	}}}
}

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

	if len(f.bumped) != 3 || f.bumped[0] != 3 || f.bumped[1] != 9 || f.bumped[2] != 7 {
		t.Errorf("BumpReads ids = %v, want [3 9 7](랭킹 순서 승계)", f.bumped)
	}
	if len(f.events) != 1 || f.events[0] != "3,9,7" {
		t.Errorf("RecordHotEvent = %v, want [3,9,7]", f.events)
	}
}

func hotHeadlineFixture(now time.Time) [][]any {
	ts := now.Truncate(time.Second)
	return [][]any{

		{"decision", "감쇠후보", "요약9", ts, int32(9), int32(10), now.Add(-10 * 24 * time.Hour), ":adr:"},

		{"decision", "중간점수", "요약7", ts, int32(7), nil, nil, ":adr:"},

		{"decision", "최고점수", "요약3", ts, int32(3), int32(9), now.Add(-time.Hour), ":adr:"},
	}
}

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

func TestHotRankUnrecalledSinksToZero(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ts := now.Truncate(time.Second)
	rows := [][]any{
		{"decision", "회상있음", "요약", ts, int32(11), ts, ts, int32(9), now.Add(-5 * 24 * time.Hour)},
		{"decision", "미회상고점", "요약", ts, int32(5), ts, ts, nil, nil},
		{"decision", "미회상저점", "요약", ts, int32(2), ts, ts, nil, nil},
	}
	f := hotRankedStore(t, rows)
	var out bytes.Buffer
	if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
	}
	wantOrder := []string{"회상있음", "미회상고점", "미회상저점"}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	for i, want := range wantOrder {
		if i >= len(lines) || !strings.Contains(lines[i], want) {
			t.Errorf("행 %d = %v, want %q 포함(감쇠후 4·미회상 0·미회상 0 순)", i, lines, want)
		}
	}
}

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
				"re.cnt, re.last_at, vtag.tags",
				"FROM hot_view h LEFT JOIN knowledge vtag ON vtag.id = h.id " + derived,
				"ORDER BY h.ts DESC, h.id DESC",
			} {
				if !strings.Contains(got, frag) {
					t.Errorf("ranked SQL에 %q 누락: %s", frag, got)
				}
			}

			if strings.Contains(got, "k.recall_score") || strings.Contains(got, "LEFT JOIN knowledge k") {
				t.Errorf("v11 직접 열 참조 잔존: %s", got)
			}

			if !strings.HasSuffix(got, tc.base[strings.LastIndex(tc.base, " LIMIT"):]) {
				t.Errorf("LIMIT 절 미승계: %s", got)
			}
		})
	}
	if got := hotRankedSQL("SELECT x FROM other ORDER BY whatever"); got != "SELECT x FROM other ORDER BY whatever" {
		t.Errorf("치환점 미발견 시 원본 반환 위반: %s", got)
	}
}

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
	idxSlow := strings.Index(out.String(), "완만감쇠")
	idxLow := strings.Index(out.String(), "저점")
	if idxSlow < 0 || idxLow < 0 || idxSlow > idxLow {
		t.Errorf("순서 = 완만감쇠@%d 저점@%d, want 완만감쇠 선행(0.5 계수: 8 > 7): %q", idxSlow, idxLow, out.String())
	}
}

func TestHotRankRowsEffectiveOrderNotAccidental(t *testing.T) {
	rows := [][]any{
		{"gamma", 5, mustTime(t, "2026-08-01T00:00:00Z")},
		{"alpha", 1, mustTime(t, "2026-11-10T00:00:00Z")},
		{"beta", 3, mustTime(t, "2026-11-15T00:00:00Z")},
	}
	got := rankRowsByEffectiveScore(context.Background(), nil, rows, 0, 1, 2, 3, 1.0, 0, mustTime(t, "2026-11-17T00:00:00Z"))
	if got[0][0] != "beta" || got[1][0] != "gamma" || got[2][0] != "alpha" {
		t.Fatalf("rank order = %v %v %v, want beta gamma alpha(effective DESC·동점 안정)", got[0][0], got[1][0], got[2][0])
	}
}

func TestHotVolImmutableExemptFromDecay(t *testing.T) {
	injectHotSeams(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ts := now.Truncate(time.Second)
	rows := [][]any{

		{"decision", "면제고점", "요약9", ts, int32(9), ts, ts, int32(10), now.Add(-10 * 24 * time.Hour), ":adr::vol:immutable:"},

		{"decision", "조건부감쇠", "요약4", ts, int32(4), ts, ts, int32(10), now.Add(-10 * 24 * time.Hour), ":vol:conditional:"},

		{"decision", "최고점수", "요약3", ts, int32(3), ts, ts, int32(9), now.Add(-time.Hour), ":adr:"},
	}
	f := hotRankedStore(t, rows)
	var out bytes.Buffer
	if code := RunHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunHot --full exit = %d: %s", code, out.String())
	}
	wantOrder := []string{"면제고점", "최고점수", "조건부감쇠"}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	for i, want := range wantOrder {
		if i >= len(lines) || !strings.Contains(lines[i], want) {
			t.Errorf("행 %d = %v, want %q 포함(면제 10·9·감쇠 0 순)", i, lines, want)
		}
	}
}

func TestWriteColonTokenHardBlock(t *testing.T) {
	out, code := run(t, &fakeStore{}, []string{"knowledge"},
		`{"topic":"t2-guard-a","summary":"합성","tags":["foo:bar"],"confidence":0.9}`)
	if code != 3 {
		t.Fatalf("콜론 미등록 태그 exit = %d, want 3: %s", code, out)
	}
	if !strings.Contains(out, "rejected tag: foo:bar") {
		t.Errorf("거부 메시지 누락: %s", out)
	}
	out, code = run(t, &fakeStore{}, []string{"knowledge"},
		`{"topic":"t2-guard-b","summary":"합성","tags":["xxx"],"confidence":0.9}`)
	if code != 0 {
		t.Fatalf("무콜론 미등록 태그 exit = %d, want 0(경고 유지): %s", code, out)
	}
	if !strings.Contains(out, "unknown tag: xxx") {
		t.Errorf("경고 메시지 누락: %s", out)
	}
	out, code = run(t, &fakeStore{}, []string{"knowledge"},
		`{"topic":"t2-guard-c","summary":"합성","tags":["vol:immutable"],"confidence":0.9}`)
	if code != 0 {
		t.Fatalf("사전 등록 vol 태그 exit = %d, want 0: %s", code, out)
	}
	if strings.Contains(out, "unknown tag") || strings.Contains(out, "rejected tag") {
		t.Errorf("사전 등록 태그에 경고·거부 잔존: %s", out)
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

func TestHotDerivedRankingRealSlow(t *testing.T) {
	st := hotRealStore(t)
	const prefix = "t4-v1900-rank-"
	hotSynthCleanup(t, st, prefix)
	injectHotSeams(t)
	ctx := context.Background()

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

	if !(idxA < idxC && idxC < idxB) {
		t.Errorf("랭킹 순서 = A@%d C@%d B@%d, want A < C < B(파생 감쇠 1·0·0 — 초기 SQL 순서 C,B,A에서 A가 최상으로 뒤집힘)",
			idxA, idxC, idxB)
	}
}

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

func hotBenchP95(ms []float64) float64 {
	s := append([]float64(nil), ms...)
	sort.Float64s(s)
	i := int(float64(len(s)) * 0.95)
	if i < 1 {
		i = 1
	}
	return s[i-1]
}

func TestHotDerivedRankingBenchRealSlow(t *testing.T) {
	st := hotRealStore(t)
	const prefix = "t4-v1900-bench-"
	hotSynthCleanup(t, st, prefix)
	t.Cleanup(func() {
		if _, err := st.pool.Exec(context.Background(), `DROP TABLE IF EXISTS bench_k11`); err != nil {
			t.Errorf("bench_k11 정리: %v", err)
		}
	})
	ctx := context.Background()

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

	med := func(sql string) float64 {
		a, b, c := run(sql), run(sql), run(sql)
		meds := []float64{a, b, c}
		sort.Float64s(meds)
		return meds[1]
	}
	preDerived := med(derivedSQL)

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

func TestEventEffectiveScoreSurvivedRelief(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	last := now.Add(-10 * 24 * time.Hour)
	for _, c := range []struct {
		name     string
		count    int
		survived int
		relief   float64
		want     int
	}{
		{"relief 0 동치 — 감쇠 하한 0", 8, 0, 0, 0},
		{"relief 0 동치 — survived 있어도 무시", 8, 5, 0, 0},
		{"완화: 8−max(0,10−2×3)=4", 8, 2, 3, 4},
		{"완화: survived 0 → 0", 8, 0, 3, 0},
		{"단조: survived 4 → 8(내부 클램프)", 8, 4, 3, 8},
		{"내부 클램프: 초과 완화 무보너스", 8, 100, 3, 8},
		{"외부 클램프: 0 하한 유지", 1, 0, 0.5, 0},
		{"부분 완화: 12−max(0,10−4)=6", 12, 4, 1, 6},
	} {
		if got := EventEffectiveScore(c.count, last, now, 1.0, c.survived, c.relief); got != c.want {
			t.Errorf("%s: = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestEffectiveScoreForSurvivedExemptIgnored(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	last := now.Add(-100 * 24 * time.Hour)
	if got := EffectiveScoreFor(8, last, now, 1.0, ":vol:immutable:", 5, 3); got != 8 {
		t.Errorf("면제+survived = %d, want 8(survived 무시·면제 우선)", got)
	}

	if got := EffectiveScoreFor(8, last, now, 1.0, "", 50, 3); got != 8 {
		t.Errorf("비면제 초과 완화 = %d, want 8", got)
	}
	if got := EffectiveScoreFor(8, now.Add(-10*24*time.Hour), now, 1.0, "", 2, 3); got != 4 {
		t.Errorf("비면제 완화 = %d, want 4(8−max(0,10−6))", got)
	}
}

type rankRowsSurvivedStore struct {
	*fakeStore
	survived    map[int]int
	kreadsCalls int
}

func (s *rankRowsSurvivedStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	if strings.Contains(sql, "knowledge_reads") {
		s.kreadsCalls++
		var rows [][]any
		if ids, ok := args[0].([]int); ok {
			for _, id := range ids {
				if n, has := s.survived[id]; has {
					rows = append(rows, []any{int32(id), int64(n)})
				}
			}
		}
		return rows, nil
	}
	return s.fakeStore.Query(ctx, sql, args...)
}

func TestRankRowsSurvivedRelief(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	ts := now.Add(-time.Hour)

	rows := [][]any{
		{"pending", "t3", "s3", ts, int32(3), ts, ts, int32(12), now.Add(-10 * 24 * time.Hour), ":adr:"},
		{"decision", "t7", "s7", ts, int32(7), ts, ts, int32(4), now.Add(-time.Hour), ":adr:"},
	}
	st := &rankRowsSurvivedStore{fakeStore: &fakeStore{}, survived: map[int]int{3: 4}}
	got := rankRowsByEffectiveScore(ctx, st, rows, hotViewIDIdx, hotViewRecallScoreIdx,
		hotViewLastRecalledIdx, hotViewTagsIdx, 1.0, 1.0, now)
	if id := got[0][hotViewIDIdx]; id != int32(3) {
		t.Errorf("완화 랭킹 선두 id = %v, want 3(survived 완화로 역전)", id)
	}
	if st.kreadsCalls != 1 {
		t.Errorf("knowledge_reads 조회 %d회, want 1(일괄 1회)", st.kreadsCalls)
	}

	st2 := &rankRowsSurvivedStore{fakeStore: &fakeStore{}, survived: map[int]int{3: 4}}
	got2 := rankRowsByEffectiveScore(ctx, st2, rows, hotViewIDIdx, hotViewRecallScoreIdx,
		hotViewLastRecalledIdx, hotViewTagsIdx, 1.0, 0, now)
	if id := got2[0][hotViewIDIdx]; id != int32(7) {
		t.Errorf("relief 0 랭킹 선두 id = %v, want 7(기존 순서 불변)", id)
	}
	if st2.kreadsCalls != 0 {
		t.Errorf("relief 0에서 knowledge_reads 조회 %d회, want 0", st2.kreadsCalls)
	}
}
