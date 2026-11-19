// report_v1200_test.go — tally-consumption regressions (fixture-based):
// the token-accounting cost object (the Σdup x mean write bytes savings lower bound and
// the Σhit x mean summary bytes injection cost; per-surface p95 nearest-rank with the
// denominator<5 guard), the zero-hit decomposition (unowned/owned_miss), recall fate
// (survived/bad_recall after 7 days), the hot->read chain, G4 consistency (the recall lead
// regardless of kreads kind — counting hot-led sessions), the curation-counter consumption, the daily flat-key reads (flat preferred, legacy JSON inherited), and the hot_stats blob consumption switch (derived preferred, existing values inherited).
package ops

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/db"
	"github.com/synuns/flint/lib/wire"
)

// v1200Store — the fixture fake (canned rows per observation source — the other queries
// follow the parityFake conventions: tallies 0, metering blank).
type v1200Store struct {
	daily     map[string]string // meta daily:* keys (flat and legacy JSON mixed)
	readEvts  [][]any           // (ts, zero, fallback, ids, first_rank, bytes, zero_reason, hits)
	readBytes [][]any           // read_events bytes samples (for the status p95 — 1 column)
	kreads    [][]any           // (knowledge_id, ts, kind)
	hotEvts   [][]any           // hot_events ids, 1 column
	knowLens  [][]any           // (id, length(summary))
	knowFate  [][]any           // (id, invalid_at, superseded_by)
	hotBlob   string            // the old hot_stats blob (for verifying the legacy fallback)
	sessions  [][]any           // session_starts ts, 1 column
	writes    [][]any           // (ingested_at, topic)
	hotCalls  int               // the kreads kind='hot' call group count
}

func (s *v1200Store) store() *fakeStore {
	return &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "LIKE 'daily:%'"):
			rows := [][]any{}
			for k, v := range s.daily {
				rows = append(rows, []any{k, v})
			}
			return rows, nil
		case strings.Contains(sql, "FROM read_events"):
			// per-query distinction: a zero_reason column = an effect event (8 columns); bytes alone =
			// a p95 sample; zero+near = the derived merge (blank — the daily read counts come from the daily flat keys).
			switch {
			case strings.Contains(sql, "zero_reason"):
				return s.readEvts, nil
			case strings.Contains(sql, "bytes"):
				return s.readBytes, nil
			}
			return nil, nil
		case strings.Contains(sql, "FROM knowledge_reads"):
			if strings.Contains(sql, "DISTINCT") {
				return [][]any{{int64(s.hotCalls)}}, nil
			}
			return s.kreads, nil
		case strings.Contains(sql, "FROM hot_events"):
			if strings.Contains(sql, "WHERE ts >= $1") {
				return nil, nil // the dead_pct exposure guard query — blank
			}
			return s.hotEvts, nil // the DerivedHotStats per-event distribution
		case strings.Contains(sql, "id, topic FROM knowledge"):
			return nil, nil
		case strings.Contains(sql, "id, length(summary) FROM knowledge"):
			return s.knowLens, nil
		case strings.Contains(sql, "id, invalid_at, superseded_by FROM knowledge"):
			return s.knowFate, nil
		case strings.Contains(sql, "hot_stats"):
			if s.hotBlob == "" {
				return nil, nil
			}
			return [][]any{{"hot_stats", s.hotBlob}}, nil
		case strings.Contains(sql, "FROM session_starts"):
			return s.sessions, nil
		case strings.Contains(sql, "ingested_at >="):
			return s.writes, nil
		case strings.Contains(sql, "FROM meta WHERE key = 'schema_version'"):
			return [][]any{{strconv.Itoa(db.LatestSchemaVersion)}}, nil
		case strings.Contains(sql, "FROM meta"):
			return nil, nil
		}
		return [][]any{{int64(0), int64(0), int64(0)}}, nil
	}}
}

// runReportV1200 — runs RunReport with the fixture then decodes the JSON.
func runReportV1200(t *testing.T, s *v1200Store, args ...string) map[string]any {
	t.Helper()
	var out strings.Builder
	if code := RunReport(context.Background(), s.store(), args, nil, &out); code != 0 {
		t.Fatalf("report exit=%d: %s", code, out.String())
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
		t.Fatalf("report JSON 파싱: %v (%s)", err, out.String())
	}
	return m
}

// effectOf — the m["effect"] decoding helper.
func effectOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	e, ok := m["effect"].(map[string]any)
	if !ok {
		t.Fatalf("effect 없음: %v", m)
	}
	return e
}

// numEq — the JSON float64 numeric comparison helper.
func numEq(t *testing.T, where string, got any, want int) {
	t.Helper()
	n, ok := got.(float64)
	if !ok || int(n) != want {
		t.Errorf("%s=%v, want %d", where, got, want)
	}
}

// strEq — the JSON string comparison helper.
func strEq(t *testing.T, where string, got any, want string) {
	t.Helper()
	s, ok := got.(string)
	if !ok || s != want {
		t.Errorf("%s=%v, want %q", where, got, want)
	}
}

// v1200Today — helpers for times/dates based on today's UTC midnight.
func v1200Today() (time.Time, string) {
	now := time.Now().UTC()
	return now, now.Format("2006-01-02")
}

// readEvt — a read_events 1-row fixture (8 columns).
func readEvt(ts time.Time, zero bool, reason string, bytes, hits int) []any {
	return []any{ts, zero, false, "", 0, bytes, reason, hits}
}

// TestReportCost — the token-accounting cost object. The savings lower bound =
// Σdup x mean write bytes (dup 2 x mean summary 100 = 200); the injection cost =
// Σhit x mean summary bytes (hit 12 x 100 = 1200); the label wording contract; and the per-surface bytes/avg/p95 and mode notation. The daily values are read from the flat keys (daily:<kind>:<date>) (flat-migration read consistency).
func TestReportCost(t *testing.T) {
	now, today := v1200Today()
	daily := map[string]string{
		"daily:write:" + today:       "4",
		"daily:dup:" + today:         "2",
		"daily:read:" + today:        "6",
		"daily:hot:" + today:         "2",
		"daily:brief:" + today:       "3",
		"daily:read_bytes:" + today:  "2100",
		"daily:hot_bytes:" + today:   "900",
		"daily:brief_bytes:" + today: "600",
	}
	readEvts := [][]any{}
	for i, b := range []int{100, 200, 300, 400, 500, 600} {
		readEvts = append(readEvts, readEvt(now, false, "", b, 2))
		_ = i
	}
	s := &v1200Store{
		daily:    daily,
		readEvts: readEvts,
		hotEvts:  [][]any{{"1,2"}},                                // 1 injection — ids 1 and 2
		knowLens: [][]any{{1, 100}, {2, 100}, {3, 100}, {4, 100}}, // mean summary 100
	}
	m := runReportV1200(t, s, "--days", "1")
	cost, ok := effectOf(t, m)["cost"].(map[string]any)
	if !ok {
		t.Fatalf("effect.cost 없음: %v", m["effect"])
	}
	// the savings lower bound — the dedup logical-physical formula (Σdup 2 x mean write bytes 100).
	numEq(t, "cost.savings_lower_bound_bytes", cost["savings_lower_bound_bytes"], 200)
	strEq(t, "cost.savings_label", cost["savings_label"], "savings lower-bound estimate")
	// the injection cost — Σhit (6x2=12) x mean summary bytes 100. Labeled as not a savings claim.
	numEq(t, "cost.hit_injection_cost_bytes", cost["hit_injection_cost_bytes"], 1200)
	strEq(t, "cost.hit_cost_label", cost["hit_cost_label"], "injection cost")

	read, ok := cost["read"].(map[string]any)
	if !ok {
		t.Fatalf("cost.read 없음: %v", cost)
	}
	numEq(t, "cost.read.bytes_14d", read["bytes_14d"], 2100)
	numEq(t, "cost.read.avg_bytes", read["avg_bytes"], 350)
	numEq(t, "cost.read.p95_bytes", read["p95_bytes"], 600) // nearest-rank ceil(0.95×6)=6
	strEq(t, "cost.read.mode", read["mode"], "on-invoke")

	hot := cost["hot"].(map[string]any)
	numEq(t, "cost.hot.bytes_14d", hot["bytes_14d"], 900)
	numEq(t, "cost.hot.avg_bytes", hot["avg_bytes"], 450)
	numEq(t, "cost.hot.p95_bytes", hot["p95_bytes"], 200) // the injected ids' summary sum 100+100
	strEq(t, "cost.hot.mode", hot["mode"], "always-on")

	brief := cost["brief"].(map[string]any)
	numEq(t, "cost.brief.bytes_14d", brief["bytes_14d"], 600)
	numEq(t, "cost.brief.avg_bytes", brief["avg_bytes"], 200)
	numEq(t, "cost.brief.p95_bytes", brief["p95_bytes"], 0) // no per-event source
	strEq(t, "cost.brief.mode", brief["mode"], "on-invoke")
}

// TestReportCostP95Guards — the p95 guards (empty and 4 samples — denominator<5 is 0).
func TestReportCostP95Guards(t *testing.T) {
	now, today := v1200Today()
	for name, n := range map[string]int{"빈 창": 0, "표본 4건": 4} {
		readEvts := [][]any{}
		for i := 0; i < n; i++ {
			readEvts = append(readEvts, readEvt(now, false, "", 100*(i+1), 1))
		}
		s := &v1200Store{daily: map[string]string{"daily:read:" + today: strconv.Itoa(n)}, readEvts: readEvts}
		m := runReportV1200(t, s, "--days", "1")
		cost, ok := effectOf(t, m)["cost"].(map[string]any)
		if !ok {
			t.Errorf("%s: effect.cost 없음", name)
			continue
		}
		read, ok := cost["read"].(map[string]any)
		if !ok {
			t.Errorf("%s: cost.read 없음", name)
			continue
		}
		numEq(t, name+" cost.read.p95_bytes", read["p95_bytes"], 0)
	}
}

// TestZeroDecompose — the zero-hit decomposition (spec §2.2). Of 10 reads,
// unowned 2·owned_miss 3 → zero_unowned_pct 20·zero_owned_miss_pct 30
// (consistent with zero_pct 50 — the denominator is the in-window read events).
func TestZeroDecompose(t *testing.T) {
	now, today := v1200Today()
	readEvts := [][]any{}
	for i := 0; i < 10; i++ {
		switch {
		case i < 2:
			readEvts = append(readEvts, readEvt(now, true, "unowned", 0, 0))
		case i < 5:
			readEvts = append(readEvts, readEvt(now, true, "owned_miss", 0, 0))
		default:
			readEvts = append(readEvts, readEvt(now, false, "", 200, 1))
		}
	}
	s := &v1200Store{daily: map[string]string{"daily:read:" + today: "10"}, readEvts: readEvts}
	m := runReportV1200(t, s, "--days", "1")
	e := effectOf(t, m)
	numEq(t, "zero_unowned_pct", e["zero_unowned_pct"], 20)
	numEq(t, "zero_owned_miss_pct", e["zero_owned_miss_pct"], 30)
}

// TestRecallFate — recall fate (spec §2.6). Recalled id 1 (stays active) and id 2
// (invalidated after 7 days) — of 2 verdict subjects, survived 50 and bad_recall 50. The
// recent (1-day) recall of id 3 is excluded from the verdict (under 7 days).
func TestRecallFate(t *testing.T) {
	now, today := v1200Today()
	old := now.Add(-8 * 24 * time.Hour)
	s := &v1200Store{
		daily: map[string]string{"daily:read:" + today: "1"},
		kreads: [][]any{
			{1, old, "read"},
			{2, old, "read"},
			{3, now.Add(-24 * time.Hour), "hot"}, // under 7 days — excluded from the verdict
		},
		knowFate: [][]any{
			{1, nil, nil},                 // stays active — survived
			{2, old.Add(-time.Hour), nil}, // invalid_at — bad
			{3, nil, 9},                   // recent recall — excluded from the verdict
		},
	}
	m := runReportV1200(t, s, "--days", "10")
	e := effectOf(t, m)
	numEq(t, "survived_pct", e["survived_pct"], 50)
	numEq(t, "bad_recall_pct", e["bad_recall_pct"], 50)
}

// TestHotReadChain — the hot->read chain (spec §2.1 gap 8). Of 2 hot exposures, 1
// followed within 30m by a read of the same knowledge -> hot_read_chain_pct 50.
func TestHotReadChain(t *testing.T) {
	now, today := v1200Today()
	t0 := now.Add(-time.Hour)
	s := &v1200Store{
		daily: map[string]string{"daily:read:" + today: "1"},
		kreads: [][]any{
			{1, t0, "hot"},
			{1, t0.Add(10 * time.Minute), "read"}, // chain hit (10m <= 30m)
			{2, t0, "hot"},                        // no subsequent read
			{2, t0.Add(-time.Minute), "read"},     // a preceding read is not a chain (Δ<0)
		},
	}
	m := runReportV1200(t, s, "--days", "1")
	numEq(t, "hot_read_chain_pct", effectOf(t, m)["hot_read_chain_pct"], 50)
}

// TestRecallLedHotIncluded — G4 consistency (spec §2.1). A session whose first recall
// is a hot exposure (preceding the write) counts as recall-led (regardless of kind — the
// kreads source). Even with read_events empty, it computes from the kreads source.
func TestRecallLedHotIncluded(t *testing.T) {
	now, today := v1200Today()
	t0 := now.Add(-2 * time.Hour)
	s := &v1200Store{
		daily:    map[string]string{"daily:read:" + today: "0"},
		sessions: [][]any{{t0}},
		kreads:   [][]any{{7, t0.Add(time.Minute), "hot"}},    // the hot exposure is the first recall
		writes:   [][]any{{t0.Add(5 * time.Minute), "배포 절차"}}, // 이후 기록
	}
	m := runReportV1200(t, s, "--days", "1")
	numEq(t, "recall_led_pct", effectOf(t, m)["recall_led_pct"], 100)
}

// TestRecallLedLegacyFallback — when kreads is empty (right after an upgrade), it
// inherits the old read_events-assignment computation (the equivalent fallback — the existing contract unchanged).
func TestRecallLedLegacyFallback(t *testing.T) {
	now, today := v1200Today()
	t0 := now.Add(-2 * time.Hour)
	s := &v1200Store{
		daily:    map[string]string{"daily:read:" + today: "1"},
		sessions: [][]any{{t0}},
		readEvts: [][]any{readEvt(t0.Add(time.Minute), false, "", 100, 1)},
		writes:   [][]any{{t0.Add(5 * time.Minute), "배포 절차"}},
	}
	m := runReportV1200(t, s, "--days", "1")
	numEq(t, "recall_led_pct", effectOf(t, m)["recall_led_pct"], 100)
}

// TestReportCurationCounters — the curation result counter consumption (spec §2.4).
// The flat daily keys' window sum is exposed as the report curation object
// (unrecorded kinds are 0).
func TestReportCurationCounters(t *testing.T) {
	_, today := v1200Today()
	s := &v1200Store{daily: map[string]string{
		"daily:approve_done:" + today:   "3",
		"daily:import_skipped:" + today: "2",
	}}
	m := runReportV1200(t, s, "--days", "1")
	cur, ok := m["curation"].(map[string]any)
	if !ok {
		t.Fatalf("curation 없음: %v", m)
	}
	numEq(t, "curation.approve_done", cur["approve_done"], 3)
	numEq(t, "curation.import_skipped", cur["import_skipped"], 2)
	numEq(t, "curation.maintain_supersede", cur["maintain_supersede"], 0)
}

// TestFlatDailyPreferredOverLegacy — flat keys preferred: when the old JSON value and a
// flat key coexist on the same day, the flat value wins (per field — consistent with the readDailyFlat contract).
func TestFlatDailyPreferredOverLegacy(t *testing.T) {
	_, today := v1200Today()
	s := &v1200Store{daily: map[string]string{
		"daily:" + today:       `{"write":4,"hot":7}`,
		"daily:write:" + today: "9", // flat preferred — 9
	}}
	m := runReportV1200(t, s, "--days", "1")
	days, ok := m["days"].([]any)
	if !ok || len(days) != 1 {
		t.Fatalf("days=%v, want 1행", m["days"])
	}
	d := days[0].(map[string]any)
	numEq(t, "days[0].write(플랫 우선)", d["write"], 9)
	numEq(t, "days[0].hot(구 JSON 승계)", d["hot"], 7)
}

// TestHotStatsDerivedSwitch — the hot_stats blob consumption switch.
// (a) With neither kreads nor hot_events data, it reports the old blob value (the same
// value as before the switch — upgrade continuity). (b) With a derived source present,
// the derived value wins regardless of the blob (the consumption switch).
func TestHotStatsDerivedSwitch(t *testing.T) {
	_, today := v1200Today()
	const blob = `{"calls":4,"sum":1000,"lens":[100,200,300,400]}`
	// (a) the legacy fallback — blob p95 400.
	s := &v1200Store{daily: map[string]string{"daily:read:" + today: "1"}, hotBlob: blob}
	m := runReportV1200(t, s, "--days", "1")
	numEq(t, "hot_p95(blob 승계)", m["hot_p95"], 400)
	// (b) derived preferred — the hot_events ids' summary-sum distribution replaces the blob.
	s = &v1200Store{
		daily:    map[string]string{"daily:read:" + today: "1"},
		hotBlob:  blob,
		hotEvts:  [][]any{{"1"}},
		knowLens: [][]any{{1, 10}},
	}
	m = runReportV1200(t, s, "--days", "1")
	numEq(t, "hot_p95(파생 우선)", m["hot_p95"], 10)
}

// TestStatusBytesP95 — the additive read_bytes_p95 and hot_bytes_p95 at the end of
// status (spec §2.3 per-surface p95 — read from read_events bytes nearest-rank; hot
// derived from hot_events x summary lengths).
func TestStatusBytesP95(t *testing.T) {
	statusSeams(t, func(context.Context, wire.Store, string) (string, error) { return "", nil })
	s := &v1200Store{
		readBytes: [][]any{{int64(10)}, {int64(20)}, {int64(30)}, {int64(40)}, {int64(50)}},
		hotEvts:   [][]any{{"1,2"}},
		knowLens:  [][]any{{1, 100}, {2, 200}},
	}
	var out strings.Builder
	if code := RunStatus(context.Background(), s.store(), nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("status exit=%d: %s", code, out.String())
	}
	var got struct {
		ReadBytesP95 int `json:"read_bytes_p95"`
		HotBytesP95  int `json:"hot_bytes_p95"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &got); err != nil {
		t.Fatalf("status JSON 파싱: %v (%s)", err, out.String())
	}
	if got.ReadBytesP95 != 50 {
		t.Errorf("read_bytes_p95=%d, want 50(표본 5 — nearest-rank 최댓값)", got.ReadBytesP95)
	}
	if got.HotBytesP95 != 300 {
		t.Errorf("hot_bytes_p95=%d, want 300(주입 ids 요약 합 100+200)", got.HotBytesP95)
	}
}
