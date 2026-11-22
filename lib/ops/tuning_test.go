// tuning_test.go — the v1.23.0 `tuning auto off|shadow` contract: the
// auto_mode meta enum (default off — an unconfigured deployment pays zero
// shadow-circuit cost), the subcommand dispatch (exit 0/1/3 inherited), the
// non-auto-key gate for the auto surfaces, and the usage guard.
package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

// withQuoteJSON — swaps in the real quoting (the cli root init seam — the ops
// package var stays nil under a standalone test run).
func withQuoteJSON(t *testing.T) {
	t.Helper()
	prev := QuoteJSONFn
	QuoteJSONFn = func(s string) string {
		b, _ := json.Marshal(s)
		return string(b)
	}
	t.Cleanup(func() { QuoteJSONFn = prev })
}

// newAutoModeFake — a meta fake: the IN-list lookup reflects the map, the
// ON CONFLICT upsert records and persists (the SetMetaKV contract).
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

// autoPoolStore — the minimal Store for the real-DB seam (the meta_kv_test
// poolStore convention — reads and writes delegate to the pool Query).
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

// autoSeamRealDSN — a real DB connection only when SUPERLITE_REAL=1 +
// FLINT_TEST_DSN are set (the meta_kv_test seamRealDSN contract).
func autoSeamRealDSN() (string, bool) {
	if os.Getenv("SUPERLITE_REAL") != "1" {
		return "", false
	}
	dsn := os.Getenv("FLINT_TEST_DSN")
	if dsn == "" {
		return "", false
	}
	return dsn, true
}

// autoModeStore — the default map-backed fake; with SUPERLITE_REAL=1, the
// real DB via autoPoolStore (the producer Store seam — SetMetaKV and the
// TuningCurrent IN lookup run the real SQL). Skips without a running DB.
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

// autoModeCleanup — removes the auto_mode test row when the real-DB seam runs
// (unnecessary for the local map fake).
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

// TestTuningAutoModeSubcommand — the enum round trip: `tuning auto shadow`
// upserts meta auto_mode (SetMetaKV idempotent path), `tuning get auto_mode`
// reflects the override, and an unconfigured deployment reads the default off
// with override false (the off-by-default ruling — the shadow circuit stays
// inert without an explicit opt-in).
func TestTuningAutoModeSubcommand(t *testing.T) {
	withQuoteJSON(t)
	ctx := context.Background()

	// set: off -> shadow via the subcommand.
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
	// the upsert recording is a fake-only observation (a real pool cannot intercept).
	if _, real := autoSeamRealDSN(); !real {
		if len(upserts) != 1 || upserts[0] != "auto_mode=shadow" {
			t.Fatalf("SetMetaKV 업서트=%v, want [auto_mode=shadow]", upserts)
		}
	}

	// get: the override is reflected.
	out.Reset()
	if code := RunTuning(ctx, st, []string{"get", "auto_mode"}, nil, &out); code != 0 {
		t.Fatalf("get auto_mode exit=%d, want 0: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"value":"shadow"`) || !strings.Contains(out.String(), `"override":true`) {
		t.Fatalf("get auto_mode 출력=%q, want value=shadow override=true", out.String())
	}

	// default: an unconfigured deployment reads off, override false (fake only —
	// a real shared DB may carry a residue row from an earlier run).
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

// TestTuningAutoModeInvalid — the enum validation: an invalid mode value and a
// wrong argument count are invalid-argument exit 3 (the tuning 0/1/3 contract).
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

// TestTuningSetAutoModeEnum — auto_mode is a TuningMetaKeys member: the
// set/get/reset surface validates the same off|shadow enum (default off).
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

	// the meta-key surface includes auto_mode (list/reset --all/TuningCurrent).
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

// TestTuningAutoKeyGate — the auto-surface key gate: only whitelisted keys
// (the registry auto field) pass; a manual-only or unknown key is exit 3 (the
// what-if/auto key contract — a non-auto key on an auto path is a key error,
// not a silent no-op).
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

// TestTuningUsageCoversAuto — the tuning usage line lists the auto
// subcommand (an unknown subcommand prints the full usage, exit 3).
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

// ── T5 C1/C2/C6: `tuning what-if` + `tuning status` ──

// shadowDist — the synthetic 14d-window distribution a what-if fake
// serves: the aggregate row (sample/zero/reclass at the asked bound) and
// the zero rows' ordered near_score list. Numbers only — the synthetic
// demo-data principle.
type shadowDist struct {
	sampleN, zeroN, reclassN int
	zeroScores               []float64
}

// newWhatIfFake — a fake store routing the what-if/status SQL shapes:
// the registry verdict aggregate (executed at the hypothetical bound),
// the zero-row near_score distribution, and the meta IN lookup
// (TuningCurrent + the counter read — fired/streak/rounds all ride meta
// since the r100-1 split; no tuning_predictions query runs).
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

// TestTuningWhatIfReclass — the what-if reclassification contract: the
// zero rows with near_score >= the hypothetical value are the reclass
// pool, the new near share is reclass/sample (the T4 predPct convention),
// and p50/p90 are nearest-rank over the zero rows' near_score distribution.
func TestTuningWhatIfReclass(t *testing.T) {
	withQuoteJSON(t)
	ctx := context.Background()
	// 12 reads, 5 zero rows; at 0.1 the reclass pool is 4 (0.5,0.45,0.42,0.2
	// pass, 0.01 does not) — near_pct 4*100/12 = 33.
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

// TestTuningWhatIfPercentileBoundaries — nearest-rank p50/p90 at odd and
// even n (ceil(p*n)-1 over the ascending order), and the empty-window
// zero guard (0, no division).
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
		// n=5: p50 ceil(2.5)-1 = idx2 -> 0.42; p90 ceil(4.5)-1 = idx4 -> 0.5.
		{"odd n", &shadowDist{sampleN: 5, zeroN: 5, reclassN: 5, zeroScores: []float64{0.01, 0.2, 0.42, 0.45, 0.5}}, 0.42, 0.5, 100, 5},
		// n=4: p50 ceil(2)-1 = idx1 -> 0.2; p90 ceil(3.6)-1 = idx3 -> 0.4.
		{"even n", &shadowDist{sampleN: 4, zeroN: 4, reclassN: 3, zeroScores: []float64{0.1, 0.2, 0.3, 0.4}}, 0.2, 0.4, 75, 4},
		// n=1: both ranks clamp to the single sample.
		{"single zero row", &shadowDist{sampleN: 1, zeroN: 1, reclassN: 0, zeroScores: []float64{0.33}}, 0.33, 0.33, 0, 1},
		// empty window: 0 everywhere, near_pct 0 (no division by zero).
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

// TestTuningWhatIfNonAutoKey — a manual-only or unknown key on the
// what-if surface is exit 3 via TuningAutoKeyGate — and no read_events
// query is ever issued (the gate precedes every observation query).
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

// TestTuningWhatIfValueValidation — the hypothetical value runs through
// the real-path threshold parse (the tuningSet single source): an
// unparseable value and 0 (unset on a fallback key) are exit 3 before any
// query; an equivalent float notation (0.10) is accepted.
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

// TestTuningWhatIfNaNDefense — the r101-1 NaN/Inf guard: a stored
// numeric NaN (or an Inf) in the zero rows' near_score distribution is
// skipped at collection, so json.Marshal never sees it (an unguarded NaN
// fails the whole output) and p50/p90 rank over the remaining finite
// scores.
func TestTuningWhatIfNaNDefense(t *testing.T) {
	withQuoteJSON(t)
	ctx := context.Background()
	// 4 zero rows, 2 of them non-finite: the ranking sees [0.2, 0.5] —
	// p50 ceil(0.5*2)-1 = idx0 → 0.2, p90 ceil(0.9*2)-1 = idx1 → 0.5.
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

// TestTuningWhatIfDomainGuard — the r102-3 domain filter: near_score is
// a [0,1] score, so an out-of-domain stored cell (5.0) is skipped at
// collection like NaN/Inf — p50/p90 rank over the in-domain remainder
// and the outlier never reaches the output.
func TestTuningWhatIfDomainGuard(t *testing.T) {
	withQuoteJSON(t)
	ctx := context.Background()
	// 3 zero rows, one out-of-domain: the ranking sees [0.2, 0.5] —
	// p50 ceil(0.5*2)-1 = idx0 → 0.2, p90 ceil(0.9*2)-1 = idx1 → 0.5.
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

// TestTuningStatusCard — the status card contract: the list skeleton rows
// extended with the auto grade and drift (current − default), the per-rule
// accumulation card (fired counter, streak, rounds = the meta
// auto_rounds_<key> every-stamped-round counter — the r100-1 split: a
// non-fired round counts as a round, independently of the fires), the
// AccumulationGate verdict with per-condition progress, and the mode
// line. Every number comes from meta — no observation query runs (status
// is a counter surface, not a what-if).
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
	// the key row: auto grade + drift (0.2 − 0.15 = 0.05) on the list skeleton.
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
	// the accumulation card: fired 2, streak 1, rounds 3, not eligible with
	// the per-condition progress sentence.
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
	// a manual-only key stays on the bare skeleton (no auto fields).
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

// TestTuningStatusArgGuard — status takes no arguments (exit 3).
func TestTuningStatusArgGuard(t *testing.T) {
	st := newWhatIfFake(map[string]string{}, &shadowDist{})
	var out bytes.Buffer
	if code := RunTuning(context.Background(), st, []string{"status", "now"}, nil, &out); code != 3 {
		t.Fatalf("status 인자 과잉 exit=%d, want 3", code)
	}
}

// whatIfScratch — a dedicated scratch DB for the slow what-if run (the
// flint-test-db-isolation-order convention): admin connect on the
// postgres catalog, CREATE DATABASE, NewRepo + Migrate. LIFO cleanup:
// DROP DATABASE. Never a shared or personal DB — synthetic demo rows only.
func whatIfScratch(t *testing.T) (*autoPoolStore, string) {
	t.Helper()
	dsn := os.Getenv("FLINT_TEST_DSN")
	if dsn == "" {
		t.Skip("no test DSN: set FLINT_TEST_DSN")
	}
	ctx := context.Background()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN parse: %v", err)
	}
	scratch := func(name string) string {
		c := *u
		c.Path = "/" + name
		return c.String()
	}
	name := fmt.Sprintf("flint_wifsc_%d", time.Now().UnixNano())
	// the postgres catalog first, the original path as fallback (servers
	// without a postgres database — this Doltgres).
	admin, err := db.Connect(ctx, scratch("postgres"))
	if err != nil {
		if admin, err = db.Connect(ctx, dsn); err != nil {
			t.Skipf("admin connect unavailable: %v", err)
		}
	}
	t.Cleanup(admin.Close)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("CREATE DATABASE %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name)
	})
	r, err := db.NewRepo(ctx, scratch(name))
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	if err := r.Migrate(ctx); err != nil {
		r.Close()
		t.Fatalf("Migrate: %v", err)
	}
	r.Close()
	pool, err := db.Connect(ctx, scratch(name))
	if err != nil {
		t.Fatalf("connect scratch: %v", err)
	}
	s := &autoPoolStore{pool: pool}
	t.Cleanup(s.Close)
	return s, scratch(name)
}

// whatIfExec — raw SQL on the scratch (seeding and the direct-SQL
// comparison; the pool store exposes no generic exec).
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

// TestTuningWhatIfRealScratch — SUPERLITE_REAL=1 slow seam check: the
// what-if counts and p50/p90 read the real seeded window on a dedicated
// scratch DB, cross-checked against direct SQL on the same rows (the
// reclassification-exactness contract), and `tuning status` reads the
// empty-shadow counters (rounds 0, fired 0, streak 0, mode off).
func TestTuningWhatIfRealScratch(t *testing.T) {
	if _, real := autoSeamRealDSN(); !real {
		t.Skip("seam check: set SUPERLITE_REAL=1 and FLINT_TEST_DSN")
	}
	withQuoteJSON(t)
	ctx := context.Background()
	st, dsn := whatIfScratch(t)

	// the synthetic window: 7 hit rows + 5 zero rows (near_score 0.5, 0.45,
	// 0.42, 0.2 pass a 0.1 bound; 0.01 does not).
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
	// the direct-SQL comparison on the same window (sample, zero, reclass).
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
	// p50/p90 direct comparison: the ordered zero-row near_scores at the
	// nearest-rank offsets (n=5: idx 2 and 4).
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

	// status on the unconfigured scratch: mode off, counters 0, not eligible.
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

// toFloat — the scanned numeric cell as float64 (0 on a non-numeric).
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
