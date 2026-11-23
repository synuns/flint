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

type v1200Store struct {
	daily     map[string]string
	readEvts  [][]any
	readBytes [][]any
	kreads    [][]any
	hotEvts   [][]any
	knowLens  [][]any
	knowFate  [][]any
	hotBlob   string
	sessions  [][]any
	writes    [][]any
	hotCalls  int
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
				return nil, nil
			}
			return s.hotEvts, nil
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

func effectOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	e, ok := m["effect"].(map[string]any)
	if !ok {
		t.Fatalf("effect 없음: %v", m)
	}
	return e
}

func numEq(t *testing.T, where string, got any, want int) {
	t.Helper()
	n, ok := got.(float64)
	if !ok || int(n) != want {
		t.Errorf("%s=%v, want %d", where, got, want)
	}
}

func strEq(t *testing.T, where string, got any, want string) {
	t.Helper()
	s, ok := got.(string)
	if !ok || s != want {
		t.Errorf("%s=%v, want %q", where, got, want)
	}
}

func v1200Today() (time.Time, string) {
	now := time.Now().UTC()
	return now, now.Format("2006-01-02")
}

func readEvt(ts time.Time, zero bool, reason string, bytes, hits int) []any {
	return []any{ts, zero, false, "", 0, bytes, reason, hits}
}

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
		hotEvts:  [][]any{{"1,2"}},
		knowLens: [][]any{{1, 100}, {2, 100}, {3, 100}, {4, 100}},
	}
	m := runReportV1200(t, s, "--days", "1")
	cost, ok := effectOf(t, m)["cost"].(map[string]any)
	if !ok {
		t.Fatalf("effect.cost 없음: %v", m["effect"])
	}

	numEq(t, "cost.savings_lower_bound_bytes", cost["savings_lower_bound_bytes"], 200)
	strEq(t, "cost.savings_label", cost["savings_label"], "savings lower-bound estimate")

	numEq(t, "cost.hit_injection_cost_bytes", cost["hit_injection_cost_bytes"], 1200)
	strEq(t, "cost.hit_cost_label", cost["hit_cost_label"], "injection cost")

	read, ok := cost["read"].(map[string]any)
	if !ok {
		t.Fatalf("cost.read 없음: %v", cost)
	}
	numEq(t, "cost.read.bytes_14d", read["bytes_14d"], 2100)
	numEq(t, "cost.read.avg_bytes", read["avg_bytes"], 350)
	numEq(t, "cost.read.p95_bytes", read["p95_bytes"], 600)
	strEq(t, "cost.read.mode", read["mode"], "on-invoke")

	hot := cost["hot"].(map[string]any)
	numEq(t, "cost.hot.bytes_14d", hot["bytes_14d"], 900)
	numEq(t, "cost.hot.avg_bytes", hot["avg_bytes"], 450)
	numEq(t, "cost.hot.p95_bytes", hot["p95_bytes"], 200)
	strEq(t, "cost.hot.mode", hot["mode"], "always-on")

	brief := cost["brief"].(map[string]any)
	numEq(t, "cost.brief.bytes_14d", brief["bytes_14d"], 600)
	numEq(t, "cost.brief.avg_bytes", brief["avg_bytes"], 200)
	numEq(t, "cost.brief.p95_bytes", brief["p95_bytes"], 0)
	strEq(t, "cost.brief.mode", brief["mode"], "on-invoke")
}

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

func TestRecallFate(t *testing.T) {
	now, today := v1200Today()
	old := now.Add(-8 * 24 * time.Hour)
	s := &v1200Store{
		daily: map[string]string{"daily:read:" + today: "1"},
		kreads: [][]any{
			{1, old, "read"},
			{2, old, "read"},
			{3, now.Add(-24 * time.Hour), "hot"},
		},
		knowFate: [][]any{
			{1, nil, nil},
			{2, old.Add(-time.Hour), nil},
			{3, nil, 9},
		},
	}
	m := runReportV1200(t, s, "--days", "10")
	e := effectOf(t, m)
	numEq(t, "survived_pct", e["survived_pct"], 50)
	numEq(t, "bad_recall_pct", e["bad_recall_pct"], 50)
}

func TestHotReadChain(t *testing.T) {
	now, today := v1200Today()
	t0 := now.Add(-time.Hour)
	s := &v1200Store{
		daily: map[string]string{"daily:read:" + today: "1"},
		kreads: [][]any{
			{1, t0, "hot"},
			{1, t0.Add(10 * time.Minute), "read"},
			{2, t0, "hot"},
			{2, t0.Add(-time.Minute), "read"},
		},
	}
	m := runReportV1200(t, s, "--days", "1")
	numEq(t, "hot_read_chain_pct", effectOf(t, m)["hot_read_chain_pct"], 50)
}

func TestRecallLedHotIncluded(t *testing.T) {
	now, today := v1200Today()
	t0 := now.Add(-2 * time.Hour)
	s := &v1200Store{
		daily:    map[string]string{"daily:read:" + today: "0"},
		sessions: [][]any{{t0}},
		kreads:   [][]any{{7, t0.Add(time.Minute), "hot"}},
		writes:   [][]any{{t0.Add(5 * time.Minute), "배포 절차"}},
	}
	m := runReportV1200(t, s, "--days", "1")
	numEq(t, "recall_led_pct", effectOf(t, m)["recall_led_pct"], 100)
}

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

func TestFlatDailyPreferredOverLegacy(t *testing.T) {
	_, today := v1200Today()
	s := &v1200Store{daily: map[string]string{
		"daily:" + today:       `{"write":4,"hot":7}`,
		"daily:write:" + today: "9",
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

func TestHotStatsDerivedSwitch(t *testing.T) {
	_, today := v1200Today()
	const blob = `{"calls":4,"sum":1000,"lens":[100,200,300,400]}`

	s := &v1200Store{daily: map[string]string{"daily:read:" + today: "1"}, hotBlob: blob}
	m := runReportV1200(t, s, "--days", "1")
	numEq(t, "hot_p95(blob 승계)", m["hot_p95"], 400)

	s = &v1200Store{
		daily:    map[string]string{"daily:read:" + today: "1"},
		hotBlob:  blob,
		hotEvts:  [][]any{{"1"}},
		knowLens: [][]any{{1, 10}},
	}
	m = runReportV1200(t, s, "--days", "1")
	numEq(t, "hot_p95(파생 우선)", m["hot_p95"], 10)
}

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
