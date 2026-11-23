package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

func withQuoteJSON(t *testing.T) {
	t.Helper()
	prev := QuoteJSONFn
	QuoteJSONFn = func(s string) string {
		b, _ := json.Marshal(s)
		return string(b)
	}
	t.Cleanup(func() { QuoteJSONFn = prev })
}

func newAutoModeFake(meta map[string]string, upserts *[]string) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "ON CONFLICT (key) DO UPDATE"):
			key, val := fmt.Sprint(args[0]), fmt.Sprint(args[1])
			meta[key] = val
			*upserts = append(*upserts, key+"="+val)
			return [][]any{{key}}, nil
		case strings.Contains(sql, "SELECT key, value FROM meta"):
			rows := make([][]any, 0, len(meta))
			for k, v := range meta {
				rows = append(rows, []any{k, v})
			}
			return rows, nil
		}
		return nil, nil
	}}
}

type autoPoolStore struct {
	pool *pgxpool.Pool
	wire.HistoryStubs
}

func (p *autoPoolStore) Migrate(ctx context.Context) error { return nil }
func (p *autoPoolStore) InsertBatch(ctx context.Context, recs []map[string]any) ([]int, error) {
	return nil, nil
}
func (p *autoPoolStore) ExistsHash(ctx context.Context, hash string) (bool, error) {
	return false, nil
}
func (p *autoPoolStore) ExistsProject(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (p *autoPoolStore) Commit(ctx context.Context, msg string) (string, error) {
	return "abc123", nil
}
func (p *autoPoolStore) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }
func (p *autoPoolStore) Close()                         { p.pool.Close() }
func (p *autoPoolStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	rows, err := p.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := [][]any{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

func autoSeamRealDSN() (string, bool) {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		return "", false
	}
	return testengine.ResolveDSN(), true
}

func autoModeStore(t *testing.T, f *fakeStore) wire.Store {
	t.Helper()
	if dsn, real := autoSeamRealDSN(); real {
		pool, err := db.Connect(context.Background(), dsn)
		if err != nil {
			t.Skipf("real store unavailable: %v", err)
		}
		s := &autoPoolStore{pool: pool}
		t.Cleanup(s.Close)
		return s
	}
	return f
}

func autoModeCleanup(t *testing.T, st wire.Store) {
	t.Helper()
	if _, real := autoSeamRealDSN(); !real {
		return
	}
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = wire.QueryOnce(ctx, st, `DELETE FROM meta WHERE key = $1`, []any{"auto_mode"})
	})
}

func TestTuningAutoModeSubcommand(t *testing.T) {
	withQuoteJSON(t)
	ctx := context.Background()

	meta := map[string]string{}
	upserts := []string{}
	fake := newAutoModeFake(meta, &upserts)
	st := autoModeStore(t, fake)
	autoModeCleanup(t, st)
	var out bytes.Buffer
	if code := RunTuning(ctx, st, []string{"auto", "shadow"}, nil, &out); code != 0 {
		t.Fatalf("auto shadow exit=%d, want 0: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"key":"auto_mode"`) || !strings.Contains(out.String(), `"value":"shadow"`) {
		t.Fatalf("auto shadow 출력=%q, want set JSON(auto_mode=shadow)", out.String())
	}

	if _, real := autoSeamRealDSN(); !real {
		if len(upserts) != 1 || upserts[0] != "auto_mode=shadow" {
			t.Fatalf("SetMetaKV 업서트=%v, want [auto_mode=shadow]", upserts)
		}
	}

	out.Reset()
	if code := RunTuning(ctx, st, []string{"get", "auto_mode"}, nil, &out); code != 0 {
		t.Fatalf("get auto_mode exit=%d, want 0: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"value":"shadow"`) || !strings.Contains(out.String(), `"override":true`) {
		t.Fatalf("get auto_mode 출력=%q, want value=shadow override=true", out.String())
	}

	if _, real := autoSeamRealDSN(); !real {
		out.Reset()
		if code := RunTuning(ctx, newAutoModeFake(map[string]string{}, &[]string{}), []string{"get", "auto_mode"}, nil, &out); code != 0 {
			t.Fatalf("get auto_mode(미설정) exit=%d, want 0", code)
		}
		if !strings.Contains(out.String(), `"value":"off"`) || !strings.Contains(out.String(), `"override":false`) {
			t.Fatalf("get auto_mode(미설정) 출력=%q, want value=off override=false", out.String())
		}
	}
}

func TestTuningAutoModeInvalid(t *testing.T) {
	withQuoteJSON(t)
	ctx := context.Background()
	cases := []struct {
		name string
		args []string
	}{
		{"invalid mode value", []string{"auto", "on"}},
		{"invalid mode word", []string{"auto", "apply"}},
		{"missing mode", []string{"auto"}},
		{"extra arg", []string{"auto", "off", "now"}},
	}
	for _, tc := range cases {
		st := newAutoModeFake(map[string]string{}, &[]string{})
		var out bytes.Buffer
		if code := RunTuning(ctx, st, tc.args, nil, &out); code != 3 {
			t.Fatalf("%s: exit=%d, want 3: %s", tc.name, code, out.String())
		}
	}
}

func TestTuningSetAutoModeEnum(t *testing.T) {
	withQuoteJSON(t)
	ctx := context.Background()
	meta := map[string]string{}
	upserts := []string{}
	st := autoModeStore(t, newAutoModeFake(meta, &upserts))
	autoModeCleanup(t, st)

	var out bytes.Buffer
	if code := RunTuning(ctx, st, []string{"set", "auto_mode", "shadow"}, nil, &out); code != 0 {
		t.Fatalf("set auto_mode shadow exit=%d, want 0: %s", code, out.String())
	}
	out.Reset()
	if code := RunTuning(ctx, st, []string{"set", "auto_mode", "sometimes"}, nil, &out); code != 3 {
		t.Fatalf("set auto_mode 무효값 exit=%d, want 3", code)
	}
	if !strings.Contains(out.String(), "off|shadow") {
		t.Fatalf("set auto_mode 무효값 출력=%q, want off|shadow 안내", out.String())
	}

	all := TuningKeyAll()
	found := false
	for _, k := range all {
		if k == "auto_mode" {
			found = true
		}
	}
	if !found {
		t.Fatal("TuningKeyAll에 auto_mode 누락(메타 관리 표면 미노출)")
	}
	defaultOff := ""
	for _, mk := range TuningMetaKeys() {
		if mk.Key == "auto_mode" {
			defaultOff = mk.Default
		}
	}
	if defaultOff != "off" {
		t.Fatalf("auto_mode 기본값=%q, want off", defaultOff)
	}
}

func TestTuningAutoKeyGate(t *testing.T) {
	var out bytes.Buffer
	if code := TuningAutoKeyGate(&out, "suggest_min_score"); code != 0 {
		t.Fatalf("화이트리스트 키 게이트 exit=%d, want 0: %s", code, out.String())
	}
	if out.Len() != 0 {
		t.Fatalf("통과 게이트 출력 발생: %s", out.String())
	}
	for _, key := range []string{"pending_max", "echo_min_score", "zz_not_a_key"} {
		out.Reset()
		if code := TuningAutoKeyGate(&out, key); code != 3 {
			t.Fatalf("%q 게이트 exit=%d, want 3", key, code)
		}
		if !strings.Contains(out.String(), "auto") {
			t.Fatalf("%q 게이트 출력=%q, want auto 안내", key, out.String())
		}
	}
}

func TestTuningUsageCoversAuto(t *testing.T) {
	st := newAutoModeFake(map[string]string{}, &[]string{})
	var out bytes.Buffer
	if code := RunTuning(context.Background(), st, []string{"zz-bogus"}, nil, &out); code != 3 {
		t.Fatalf("무효 서브커맨드 exit=%d, want 3", code)
	}
	if !strings.Contains(out.String(), "auto off|shadow") {
		t.Fatalf("usage에 auto 미포함: %s", out.String())
	}
}

type shadowDist struct {
	sampleN, zeroN, reclassN int
	zeroScores               []float64
}

func newWhatIfFake(meta map[string]string, dist *shadowDist) *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "COUNT(CASE WHEN zero") && strings.Contains(sql, "read_events"):
			return [][]any{{dist.sampleN, dist.zeroN, dist.reclassN}}, nil
		case strings.Contains(sql, "SELECT near_score FROM read_events"):
			rows := make([][]any, 0, len(dist.zeroScores))
			for _, s := range dist.zeroScores {
				rows = append(rows, []any{s})
			}
			return rows, nil
		case strings.Contains(sql, "SELECT key, value FROM meta"):
			rows := make([][]any, 0, len(meta))
			for k, v := range meta {
				rows = append(rows, []any{k, v})
			}
			return rows, nil
		}
		return nil, nil
	}}
}

func TestTuningWhatIfReclass(t *testing.T) {
	withQuoteJSON(t)
	ctx := context.Background()

	dist := &shadowDist{sampleN: 12, zeroN: 5, reclassN: 4, zeroScores: []float64{0.01, 0.2, 0.42, 0.45, 0.5}}
	st := newWhatIfFake(map[string]string{}, dist)
	var out bytes.Buffer
	if code := RunTuning(ctx, st, []string{"what-if", "suggest_min_score", "0.1"}, nil, &out); code != 0 {
		t.Fatalf("what-if exit=%d, want 0: %s", code, out.String())
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("what-if 출력 JSON 아님: %q (%v)", out.String(), err)
	}
	want := map[string]any{
		"key": "suggest_min_score", "value": "0.1", "auto": "shadow",
		"sample_n": 12.0, "zero_n": 5.0, "reclass_n": 4.0, "near_pct": 33.0,
		"near_score_p50": 0.42, "near_score_p90": 0.5,
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("what-if[%s]=%v, want %v (전체: %s)", k, got[k], v, out.String())
		}
	}
}

func TestTuningWhatIfPercentileBoundaries(t *testing.T) {
	withQuoteJSON(t)
	ctx := context.Background()
	cases := []struct {
		name   string
		dist   *shadowDist
		p50    float64
		p90    float64
		pct    int
		sample int
	}{

		{"odd n", &shadowDist{sampleN: 5, zeroN: 5, reclassN: 5, zeroScores: []float64{0.01, 0.2, 0.42, 0.45, 0.5}}, 0.42, 0.5, 100, 5},

		{"even n", &shadowDist{sampleN: 4, zeroN: 4, reclassN: 3, zeroScores: []float64{0.1, 0.2, 0.3, 0.4}}, 0.2, 0.4, 75, 4},

		{"single zero row", &shadowDist{sampleN: 1, zeroN: 1, reclassN: 0, zeroScores: []float64{0.33}}, 0.33, 0.33, 0, 1},

		{"empty window", &shadowDist{sampleN: 0, zeroN: 0, reclassN: 0, zeroScores: nil}, 0, 0, 0, 0},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		st := newWhatIfFake(map[string]string{}, tc.dist)
		if code := RunTuning(ctx, st, []string{"what-if", "suggest_min_score", "0.1"}, nil, &out); code != 0 {
			t.Fatalf("%s: exit=%d, want 0: %s", tc.name, code, out.String())
		}
		var got map[string]any
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("%s: JSON 아님: %q", tc.name, out.String())
		}
		if got["near_score_p50"] != tc.p50 || got["near_score_p90"] != tc.p90 {
			t.Fatalf("%s: p50/p90=%v/%v, want %v/%v", tc.name, got["near_score_p50"], got["near_score_p90"], tc.p50, tc.p90)
		}
		if int(got["near_pct"].(float64)) != tc.pct {
			t.Fatalf("%s: near_pct=%v, want %d", tc.name, got["near_pct"], tc.pct)
		}
		if int(got["sample_n"].(float64)) != tc.sample {
			t.Fatalf("%s: sample_n=%v, want %d", tc.name, got["sample_n"], tc.sample)
		}
	}
}

func TestTuningWhatIfNonAutoKey(t *testing.T) {
	withQuoteJSON(t)
	ctx := context.Background()
	for _, key := range []string{"pending_max", "echo_min_score", "zz_not_a_key"} {
		dist := &shadowDist{sampleN: 9, zeroN: 9, reclassN: 9, zeroScores: []float64{0.5}}
		fake := newWhatIfFake(map[string]string{}, dist)
		var out bytes.Buffer
		if code := RunTuning(ctx, fake, []string{"what-if", key, "0.1"}, nil, &out); code != 3 {
			t.Fatalf("%q what-if exit=%d, want 3", key, code)
		}
		if !strings.Contains(out.String(), "auto") {
			t.Fatalf("%q what-if 출력=%q, want auto 안내", key, out.String())
		}
		for _, q := range fake.queries {
			if strings.Contains(q, "read_events") {
				t.Fatalf("%q: 게이트 통과 전 관측 쿼리 실행: %s", key, q)
			}
		}
	}
}

func TestTuningWhatIfValueValidation(t *testing.T) {
	withQuoteJSON(t)
	ctx := context.Background()
	dist := &shadowDist{sampleN: 5, zeroN: 5, reclassN: 5, zeroScores: []float64{0.5}}
	for _, val := range []string{"many", "0", "-1"} {
		fake := newWhatIfFake(map[string]string{}, dist)
		var out bytes.Buffer
		if code := RunTuning(ctx, fake, []string{"what-if", "suggest_min_score", val}, nil, &out); code != 3 {
			t.Fatalf("무효값 %q exit=%d, want 3: %s", val, code, out.String())
		}
		for _, q := range fake.queries {
			if strings.Contains(q, "read_events") {
				t.Fatalf("무효값 %q 관측 쿼리 실행: %s", val, q)
			}
		}
	}
	var out bytes.Buffer
	if code := RunTuning(ctx, newWhatIfFake(map[string]string{}, dist), []string{"what-if", "suggest_min_score", "0.10"}, nil, &out); code != 0 {
		t.Fatalf("동등 표기 0.10 exit=%d, want 0: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"value":"0.10"`) {
		t.Fatalf("동등 표기 반영 출력: %s", out.String())
	}
}

func TestTuningWhatIfNaNDefense(t *testing.T) {
	withQuoteJSON(t)
	ctx := context.Background()

	dist := &shadowDist{sampleN: 4, zeroN: 4, reclassN: 2,
		zeroScores: []float64{0.2, math.NaN(), math.Inf(1), 0.5}}
	var out bytes.Buffer
	if code := RunTuning(ctx, newWhatIfFake(map[string]string{}, dist),
		[]string{"what-if", "suggest_min_score", "0.1"}, nil, &out); code != 0 {
		t.Fatalf("NaN 혼입 what-if exit=%d, want 0: %s", code, out.String())
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("NaN 혼입 출력 JSON 아님(마샬 붕괴): %q (%v)", out.String(), err)
	}
	if got["near_score_p50"] != 0.2 || got["near_score_p90"] != 0.5 {
		t.Fatalf("유한 분포 p50/p90=%v/%v, want 0.2/0.5", got["near_score_p50"], got["near_score_p90"])
	}
}

func TestTuningWhatIfDomainGuard(t *testing.T) {
	withQuoteJSON(t)
	ctx := context.Background()

	dist := &shadowDist{sampleN: 3, zeroN: 3, reclassN: 2,
		zeroScores: []float64{0.2, 5.0, 0.5}}
	var out bytes.Buffer
	if code := RunTuning(ctx, newWhatIfFake(map[string]string{}, dist),
		[]string{"what-if", "suggest_min_score", "0.1"}, nil, &out); code != 0 {
		t.Fatalf("도메인 이탈 혼입 what-if exit=%d, want 0: %s", code, out.String())
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("도메인 이탈 혼입 출력 JSON 아님: %q (%v)", out.String(), err)
	}
	if got["near_score_p50"] != 0.2 || got["near_score_p90"] != 0.5 {
		t.Fatalf("도메인 내 분포 p50/p90=%v/%v, want 0.2/0.5 (5.0 미반영)",
			got["near_score_p50"], got["near_score_p90"])
	}
}

func TestTuningStatusCard(t *testing.T) {
	withQuoteJSON(t)
	ctx := context.Background()
	meta := map[string]string{
		"suggest_min_score":              "0.2",
		"auto_mode":                      "shadow",
		"fired_auto_auto_zero_near_high": "2",
		"auto_streak_suggest_min_score":  "1",
		"auto_rounds_suggest_min_score":  "3",
	}
	st := newWhatIfFake(meta, &shadowDist{})
	var out bytes.Buffer
	if code := RunTuning(ctx, st, []string{"status"}, nil, &out); code != 0 {
		t.Fatalf("status exit=%d, want 0: %s", code, out.String())
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("status 출력 JSON 아님: %q (%v)", out.String(), err)
	}
	if got["mode"] != "shadow" {
		t.Fatalf("status mode=%v, want shadow", got["mode"])
	}

	var keyRow map[string]any
	for _, r := range got["keys"].([]any) {
		if r.(map[string]any)["key"] == "suggest_min_score" {
			keyRow = r.(map[string]any)
		}
	}
	if keyRow == nil {
		t.Fatalf("status 키 행에 suggest_min_score 없음: %s", out.String())
	}
	if keyRow["auto"] != "shadow" {
		t.Fatalf("status 키 행 auto=%v, want shadow", keyRow["auto"])
	}
	if keyRow["drift"] != 0.05 {
		t.Fatalf("status 키 행 drift=%v, want 0.05", keyRow["drift"])
	}

	cards, ok := got["auto"].([]any)
	if !ok || len(cards) != 1 {
		t.Fatalf("status auto 카드 없음/과잉: %s", out.String())
	}
	card := cards[0].(map[string]any)
	for k, v := range map[string]any{"signal": "auto_zero_near_high", "key": "suggest_min_score", "grade": "shadow", "fired": 2.0, "streak": 1.0, "rounds": 3.0} {
		if card[k] != v {
			t.Fatalf("카드[%s]=%v, want %v (%s)", k, card[k], v, out.String())
		}
	}
	acc := card["accumulation"].(map[string]any)
	if acc["eligible"] != false {
		t.Fatalf("accumulation eligible=%v, want false", acc["eligible"])
	}
	for _, frag := range []string{"rounds 3/14", "fires 2/5", "streak 1/3"} {
		if !strings.Contains(acc["detail"].(string), frag) {
			t.Fatalf("accumulation detail에 %q 없음: %v", frag, acc["detail"])
		}
	}

	var manualRow map[string]any
	for _, r := range got["keys"].([]any) {
		if r.(map[string]any)["key"] == "pending_max" {
			manualRow = r.(map[string]any)
		}
	}
	if manualRow == nil {
		t.Fatalf("status 키 행에 pending_max 없음: %s", out.String())
	}
	if _, has := manualRow["auto"]; has {
		t.Fatalf("manual 키에 auto 필드 있음: %v", manualRow)
	}
}

func TestTuningStatusArgGuard(t *testing.T) {
	st := newWhatIfFake(map[string]string{}, &shadowDist{})
	var out bytes.Buffer
	if code := RunTuning(context.Background(), st, []string{"status", "now"}, nil, &out); code != 3 {
		t.Fatalf("status 인자 과잉 exit=%d, want 3", code)
	}
}

func whatIfScratch(t *testing.T) (*autoPoolStore, string) {
	t.Helper()
	scratchDSN := testengine.Scratch(t)
	ctx := context.Background()
	r, err := db.NewRepo(ctx, scratchDSN)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	if err := r.Migrate(ctx); err != nil {
		r.Close()
		t.Fatalf("Migrate: %v", err)
	}
	r.Close()
	pool, err := db.Connect(ctx, scratchDSN)
	if err != nil {
		t.Fatalf("connect scratch: %v", err)
	}
	s := &autoPoolStore{pool: pool}
	t.Cleanup(s.Close)
	return s, scratchDSN
}

func whatIfExec(t *testing.T, dsn, sql string) {
	t.Helper()
	pool, err := db.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect scratch: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func TestTuningWhatIfRealScratch(t *testing.T) {
	if _, real := autoSeamRealDSN(); !real {
		t.Skip("seam check: set SUPERLITE_REAL=1 and FLINT_TEST_DSN")
	}
	withQuoteJSON(t)
	ctx := context.Background()
	st, dsn := whatIfScratch(t)

	for i := 0; i < 7; i++ {
		whatIfExec(t, dsn, `INSERT INTO read_events (hits, zero, near, fallback) VALUES (2, false, false, false)`)
	}
	for _, s := range []string{"0.5", "0.45", "0.42", "0.2", "0.01"} {
		whatIfExec(t, dsn, fmt.Sprintf(`INSERT INTO read_events (hits, zero, near, fallback, near_score) VALUES (0, true, false, false, %s)`, s))
	}

	var out bytes.Buffer
	if code := RunTuning(ctx, st, []string{"what-if", "suggest_min_score", "0.1"}, nil, &out); code != 0 {
		t.Fatalf("what-if exit=%d, want 0: %s", code, out.String())
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("what-if 출력 JSON 아님: %q (%v)", out.String(), err)
	}

	rows, err := st.Query(ctx,
		`SELECT COUNT(*), COUNT(CASE WHEN zero THEN 1 END), COUNT(CASE WHEN zero AND near_score >= 0.1 THEN 1 END)
		 FROM read_events WHERE ts >= now() - interval '14 days'`)
	if err != nil || len(rows) != 1 {
		t.Fatalf("직접 SQL 대조 실패: %v %v", rows, err)
	}
	wantN := []float64{12, 5, 4}
	gotN := []float64{got["sample_n"].(float64), got["zero_n"].(float64), got["reclass_n"].(float64)}
	for i := range wantN {
		gotSQL, _ := toFloat(rows[0][i])
		if gotN[i] != wantN[i] || gotSQL != wantN[i] {
			t.Fatalf("대조[%d] what-if=%v 직접SQL=%v, want %v", i, gotN[i], gotSQL, wantN[i])
		}
	}
	if int(got["near_pct"].(float64)) != 33 {
		t.Fatalf("near_pct=%v, want 33", got["near_pct"])
	}

	for _, tc := range []struct {
		field string
		off   int
	}{
		{"near_score_p50", 2},
		{"near_score_p90", 4},
	} {
		srows, err := st.Query(ctx,
			`SELECT near_score FROM read_events WHERE zero AND ts >= now() - interval '14 days' ORDER BY near_score LIMIT 1 OFFSET `+strconv.Itoa(tc.off))
		if err != nil || len(srows) != 1 {
			t.Fatalf("p%% 직접 SQL 실패(offset %d): %v %v", tc.off, srows, err)
		}
		wantV, _ := toFloat(srows[0][0])
		if got[tc.field].(float64) != wantV {
			t.Fatalf("%s=%v, want 직접 SQL %v", tc.field, got[tc.field], wantV)
		}
	}

	out.Reset()
	if code := RunTuning(ctx, st, []string{"status"}, nil, &out); code != 0 {
		t.Fatalf("status exit=%d, want 0: %s", code, out.String())
	}
	var stat map[string]any
	if err := json.Unmarshal(out.Bytes(), &stat); err != nil {
		t.Fatalf("status 출력 JSON 아님: %q (%v)", out.String(), err)
	}
	if stat["mode"] != "off" {
		t.Fatalf("status mode=%v, want off", stat["mode"])
	}
	cards, _ := stat["auto"].([]any)
	if len(cards) != 1 {
		t.Fatalf("status auto 카드 수=%d, want 1: %s", len(cards), out.String())
	}
	card := cards[0].(map[string]any)
	if card["rounds"] != 0.0 || card["fired"] != 0.0 || card["streak"] != 0.0 {
		t.Fatalf("빈 shadow 카운터=%v, want 전부 0: %s", card, out.String())
	}
	acc := card["accumulation"].(map[string]any)
	if acc["eligible"] != false {
		t.Fatalf("빈 상태 eligible=%v, want false", acc["eligible"])
	}
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}
