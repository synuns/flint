// report_v1200_test.go — v1.2.0 T3 집계 소비 회귀(픽스처 기반 — Review Focus):
// 토큰 회계 cost(Σdup×평균 기록 바이트 절약 하한·Σhit×평균 요약 바이트 주입
// 원가·표면별 p95 nearest-rank 분모<5 가드)·제로히트 분해(unowned/owned_miss)·
// 회상 운명(7일 후 survived/bad_recall)·hot→read 연쇄·G4 정합(kreads kind 불문
// 회상 선도 — hot-led 세션 계상)·검수 curation 카운터 소비·daily 플랫 키 읽기
// (플랫 우선·구 JSON 승계)·hot_stats blob 소비 전환(파생 우선·기존 값 승계).
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

// v1200Store — T3 픽스처 fake(관측 원천별 canned 행 — 나머지 질의는
// parityFake 관례: 집계 0·계측 공란).
type v1200Store struct {
	daily     map[string]string // meta daily:* 키(플랫·구 JSON 혼합)
	readEvts  [][]any           // (ts, zero, fallback, ids, first_rank, bytes, zero_reason, hits)
	readBytes [][]any           // read_events bytes 표본(상태 p95용 — 1열)
	kreads    [][]any           // (knowledge_id, ts, kind)
	hotEvts   [][]any           // hot_events ids 1열
	knowLens  [][]any           // (id, length(summary))
	knowFate  [][]any           // (id, invalid_at, superseded_by)
	hotBlob   string            // 구 hot_stats blob(승계 폴백 확인용)
	sessions  [][]any           // session_starts ts 1열
	writes    [][]any           // (ingested_at, topic)
	hotCalls  int               // kreads kind='hot' 호출 그룹 수
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
			// 질의별 구분: zero_reason 열 = 효과 이벤트(8열), bytes 단독 =
			// p95 표본, zero+near = 파생 병합(공란 — read 일별 수는 daily 플랫 키로).
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
				return nil, nil // dead_pct 노출 가드 질의 — 공란
			}
			return s.hotEvts, nil // DerivedHotStats 사건 분포
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

// runReportV1200 — 픽스처로 RunReport 실행 후 JSON 디코딩.
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

// effectOf — m["effect"] 디코딩 헬퍼.
func effectOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	e, ok := m["effect"].(map[string]any)
	if !ok {
		t.Fatalf("effect 없음: %v", m)
	}
	return e
}

// numEq — JSON float64 수치 비교 헬퍼.
func numEq(t *testing.T, where string, got any, want int) {
	t.Helper()
	n, ok := got.(float64)
	if !ok || int(n) != want {
		t.Errorf("%s=%v, want %d", where, got, want)
	}
}

// strEq — JSON 문자열 비교 헬퍼.
func strEq(t *testing.T, where string, got any, want string) {
	t.Helper()
	s, ok := got.(string)
	if !ok || s != want {
		t.Errorf("%s=%v, want %q", where, got, want)
	}
}

// v1200Today — 오늘 UTC 자정 기준 시각·날짜 헬퍼.
func v1200Today() (time.Time, string) {
	now := time.Now().UTC()
	return now, now.Format("2006-01-02")
}

// readEvt — read_events 1행 픽스처(8열 — v1.2.0 T2 확장 열 포함).
func readEvt(ts time.Time, zero bool, reason string, bytes, hits int) []any {
	return []any{ts, zero, false, "", 0, bytes, reason, hits}
}

// TestReportCost — Review Focus: 토큰 회계 cost 객체. 절약 하한 = Σdup×평균
// 기록 바이트(dup 2 × 평균 요약 100 = 200), 주입 원가 = Σhit×평균 요약 바이트
// (hit 12 × 100 = 1200), 라벨 문구 계약, 표면별 bytes/avg/p95·모드 표기.
// daily 값은 플랫 키(daily:<kind>:<date>)에서 읽는다(플랫 전환 읽기 정합).
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
		hotEvts:  [][]any{{"1,2"}},                                // 1회 주입 — id 1·2
		knowLens: [][]any{{1, 100}, {2, 100}, {3, 100}, {4, 100}}, // 평균 요약 100
	}
	m := runReportV1200(t, s, "--days", "1")
	cost, ok := effectOf(t, m)["cost"].(map[string]any)
	if !ok {
		t.Fatalf("effect.cost 없음: %v", m["effect"])
	}
	// 절약 하한 — dedup 논리-물리 공식(Σdup 2 × 평균 기록 바이트 100).
	numEq(t, "cost.savings_lower_bound_bytes", cost["savings_lower_bound_bytes"], 200)
	strEq(t, "cost.savings_label", cost["savings_label"], "절약 하한 추정")
	// 주입 원가 — Σhit(6×2=12) × 평균 요약 바이트 100. 절약 주장 아님 라벨.
	numEq(t, "cost.hit_injection_cost_bytes", cost["hit_injection_cost_bytes"], 1200)
	strEq(t, "cost.hit_cost_label", cost["hit_cost_label"], "주입 원가")

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
	numEq(t, "cost.hot.p95_bytes", hot["p95_bytes"], 200) // 주입 ids 요약 합 100+100
	strEq(t, "cost.hot.mode", hot["mode"], "always-on")

	brief := cost["brief"].(map[string]any)
	numEq(t, "cost.brief.bytes_14d", brief["bytes_14d"], 600)
	numEq(t, "cost.brief.avg_bytes", brief["avg_bytes"], 200)
	numEq(t, "cost.brief.p95_bytes", brief["p95_bytes"], 0) // 사건별 원천 없음
	strEq(t, "cost.brief.mode", brief["mode"], "on-invoke")
}

// TestReportCostP95Guards — Review Focus: p95 가드(빈·표본 4건 — 분모<5는 0).
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

// TestZeroDecompose — Review Focus: 제로히트 분해(spec §2.2). read 10회 중
// unowned 2·owned_miss 3 → zero_unowned_pct 20·zero_owned_miss_pct 30
// (zero_pct 50과 분해 정합 — 분모는 창 내 read 이벤트).
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

// TestRecallFate — Review Focus: 회상 운명(spec §2.6). 회상 id 1(활성 유지)·
// id 2(7일 후 무효화) — 판정 대상 2 중 survived 50·bad_recall 50. 최근(1일)
// 회상 id 3은 7일 미경과로 판정에서 제외.
func TestRecallFate(t *testing.T) {
	now, today := v1200Today()
	old := now.Add(-8 * 24 * time.Hour)
	s := &v1200Store{
		daily: map[string]string{"daily:read:" + today: "1"},
		kreads: [][]any{
			{1, old, "read"},
			{2, old, "read"},
			{3, now.Add(-24 * time.Hour), "hot"}, // 7일 미경과 — 판정 제외
		},
		knowFate: [][]any{
			{1, nil, nil},                 // 활성 유지 — survived
			{2, old.Add(-time.Hour), nil}, // invalid_at — bad
			{3, nil, 9},                   // 최근 회상 — 판정 제외
		},
	}
	m := runReportV1200(t, s, "--days", "10")
	e := effectOf(t, m)
	numEq(t, "survived_pct", e["survived_pct"], 50)
	numEq(t, "bad_recall_pct", e["bad_recall_pct"], 50)
}

// TestHotReadChain — Review Focus: hot→read 연쇄(spec §2.1 갭8). hot 노출 2회 중
// 30분 내 같은 지식 read가 이어진 1회 → hot_read_chain_pct 50.
func TestHotReadChain(t *testing.T) {
	now, today := v1200Today()
	t0 := now.Add(-time.Hour)
	s := &v1200Store{
		daily: map[string]string{"daily:read:" + today: "1"},
		kreads: [][]any{
			{1, t0, "hot"},
			{1, t0.Add(10 * time.Minute), "read"}, // 연쇄 O(10분 ≤ 30분)
			{2, t0, "hot"},                        // 후속 read 없음
			{2, t0.Add(-time.Minute), "read"},     // 선행 read는 연쇄 아님(Δ<0)
		},
	}
	m := runReportV1200(t, s, "--days", "1")
	numEq(t, "hot_read_chain_pct", effectOf(t, m)["hot_read_chain_pct"], 50)
}

// TestRecallLedHotIncluded — Review Focus: G4 정합(spec §2.1). hot 노출이 세션
// 첫 회상인 세션(기록 앞섬)이 회상 선도로 계상된다(kind 불문 — kreads 원천).
// read_events가 비어 있어도 kreads 원천으로 산출된다.
func TestRecallLedHotIncluded(t *testing.T) {
	now, today := v1200Today()
	t0 := now.Add(-2 * time.Hour)
	s := &v1200Store{
		daily:    map[string]string{"daily:read:" + today: "0"},
		sessions: [][]any{{t0}},
		kreads:   [][]any{{7, t0.Add(time.Minute), "hot"}},    // hot 노출이 첫 회상
		writes:   [][]any{{t0.Add(5 * time.Minute), "배포 절차"}}, // 이후 기록
	}
	m := runReportV1200(t, s, "--days", "1")
	numEq(t, "recall_led_pct", effectOf(t, m)["recall_led_pct"], 100)
}

// TestRecallLedLegacyFallback — kreads가 비어 있으면(업그레이드 직후) 구
// read_events 배정 산출을 승계한다(동등 폴백 — 기존 계약 불변).
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

// TestReportCurationCounters — 검수 결과 카운터 소비(spec §2.4 — T4 기록·
// T3 노출). 플랫 daily 키 창 합산이 report curation 객체로 노출된다
// (미기록 종류는 0).
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

// TestFlatDailyPreferredOverLegacy — 플랫 키 우선: 같은 날 구 JSON 값과 플랫
// 키가 공존하면 플랫 값이 이긴다(필드별 — readDailyFlat 계약과 정합).
func TestFlatDailyPreferredOverLegacy(t *testing.T) {
	_, today := v1200Today()
	s := &v1200Store{daily: map[string]string{
		"daily:" + today:       `{"write":4,"hot":7}`,
		"daily:write:" + today: "9", // 플랫 우선 — 9
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

// TestHotStatsDerivedSwitch — Review Focus: hot_stats blob 소비 전환.
// (a) kreads·hot_events 어느 쪽도 데이터가 없으면 구 blob 값을 승계 보고
// (전환 전과 동일 값 — 업그레이드 연속성). (b) 파생 원천이 있으면 blob과
// 무관하게 파생값이 이긴다(소비 전환).
func TestHotStatsDerivedSwitch(t *testing.T) {
	_, today := v1200Today()
	const blob = `{"calls":4,"sum":1000,"lens":[100,200,300,400]}`
	// (a) 승계 폴백 — blob p95 400.
	s := &v1200Store{daily: map[string]string{"daily:read:" + today: "1"}, hotBlob: blob}
	m := runReportV1200(t, s, "--days", "1")
	numEq(t, "hot_p95(blob 승계)", m["hot_p95"], 400)
	// (b) 파생 우선 — hot_events ids 요약 합 분포가 blob을 대체한다.
	s = &v1200Store{
		daily:    map[string]string{"daily:read:" + today: "1"},
		hotBlob:  blob,
		hotEvts:  [][]any{{"1"}},
		knowLens: [][]any{{1, 10}},
	}
	m = runReportV1200(t, s, "--days", "1")
	numEq(t, "hot_p95(파생 우선)", m["hot_p95"], 10)
}

// TestStatusBytesP95 — status 말미 additive read_bytes_p95·hot_bytes_p95
// (spec §2.3 표면별 p95 — read는 read_events bytes nearest-rank, hot은
// hot_events×요약 길이 파생).
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
