// readhot_v06_test.go — fast (fake) tests for read/hot inbound and reference metering.
package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
)

// instrumentedFake — a fakeStore with BumpReads and RecordUsage metering layered on.
// Verifies the silent failure paths via injected failures (bumpErr, usageErr).
type instrumentedFake struct {
	fakeStore
	bumped   [][]int
	usage    [][2]string
	bumpErr  error
	usageErr error
}

func (f *instrumentedFake) BumpReads(ctx context.Context, ids []int) error {
	f.bumped = append(f.bumped, ids)
	return f.bumpErr
}

func (f *instrumentedFake) RecordUsage(ctx context.Context, kind, source string) error {
	f.usage = append(f.usage, [2]string{kind, source})
	return f.usageErr
}

// readQueryFn — the active_knowledge query returns 2 id rows; other meta queries return empty results.
func readQueryFn(sql string, args []any) ([][]any, error) {
	if strings.Contains(sql, "FROM active_knowledge") {
		return [][]any{{int32(3), "t", "s", ":a:", nil, "main", 0.9, "active"},
			{int32(5), "t2", "s2", ":b:", nil, "main", 0.8, "active"}}, nil
	}
	return nil, nil
}

// TestReadInstrumentsSilently — metering does BumpReads over all result item ids
// + recordUsage("read",""). Metering failure (bumpErr) and an unimplemented Store
// both keep exit 0 and unchanged output.
func TestReadInstrumentsSilently(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &instrumentedFake{fakeStore: fakeStore{queryFn: readQueryFn}}
	code := runRead(ctx, f, []string{"topic"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("runRead exit = %d, want 0", code)
	}
	if len(f.bumped) != 1 || len(f.bumped[0]) != 2 ||
		f.bumped[0][0] != 3 || f.bumped[0][1] != 5 {
		t.Errorf("BumpReads ids = %v, want [3 5] (결과 id 전수)", f.bumped)
	}
	// the read-kind inbound metering (daily read counter) is gone —
	// the metering source was unified into read_events (RecordReadEvent) (writes 4->2).
	if len(f.usage) != 0 {
		t.Errorf("RecordUsage = %v, want [](read kind 유입 계측 폐지)", f.usage)
	}

	// metering failure is silent — exit 0, identical output.
	var out2 bytes.Buffer
	f2 := &instrumentedFake{fakeStore: fakeStore{queryFn: readQueryFn}, bumpErr: errSilent{}}
	if code := runRead(ctx, f2, []string{"topic"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("bumpErr 시 exit = %d, want 0", code)
	}
	if out2.String() != out.String() {
		t.Errorf("bumpErr 시 출력 변형:\n%q\n%q", out.String(), out2.String())
	}

	// with an unimplemented Store (plain fakeStore) it is also a no-op — exit 0.
	var out3 bytes.Buffer
	plain := &fakeStore{queryFn: readQueryFn}
	if code := runRead(ctx, plain, []string{"topic"}, strings.NewReader(""), &out3); code != 0 {
		t.Fatalf("미구현 Store exit = %d, want 0", code)
	}
}

// hotQueryFn — the hot_view essential query returns 7-column rows including the increment columns
// (id, valid_at, ingested_at) (2 knowledge rows, ids 3 and 7 — a single definition).
func hotQueryFn(sql string, args []any) ([][]any, error) {
	if strings.Contains(sql, "FROM hot_view") {
		return [][]any{
			{"pending", "토픽", "요약", "2026-10-22", int32(3), nil, nil},
			{"decision", "토픽2", "요약2", "2026-10-21", int32(7), nil, nil},
		}, nil
	}
	return nil, nil
}

// TestHotInstrumentsSilently — on hot exit, BumpReads over all injected knowledge ids +
// recordUsage("hot",""). Metering failure and unimplemented Store keep exit 0 and unchanged output.
// Incrementing happens only on the --full (actual injection) path — redirect the call to --full
// (the headline-path skip is covered by TestHotHeadlineSkipsReadBump in hot_v0132_test.go).
func TestHotInstrumentsSilently(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &instrumentedFake{fakeStore: fakeStore{queryFn: hotQueryFn}}
	code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("runHot exit = %d, want 0", code)
	}
	if len(f.bumped) != 1 || len(f.bumped[0]) != 2 ||
		f.bumped[0][0] != 3 || f.bumped[0][1] != 7 {
		t.Errorf("BumpReads ids = %v, want [3 7] (주입 지식 id 전수)", f.bumped)
	}
	if len(f.usage) != 1 || f.usage[0] != [2]string{"hot", ""} {
		t.Errorf("RecordUsage = %v, want [[hot ]]", f.usage)
	}

	// both usageErr and bumpErr keep exit 0 and unchanged output.
	var out2 bytes.Buffer
	f2 := &instrumentedFake{fakeStore: fakeStore{queryFn: hotQueryFn}, usageErr: errSilent{}}
	if code := runHot(ctx, f2, []string{"--full"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("usageErr 시 exit = %d, want 0", code)
	}
	if out2.String() != out.String() {
		t.Errorf("usageErr 시 출력 변형:\n%q\n%q", out.String(), out2.String())
	}

	// with an unimplemented Store (plain fakeStore) it is also a no-op — exit 0.
	var out3 bytes.Buffer
	plain := &fakeStore{queryFn: hotQueryFn}
	if code := runHot(ctx, plain, []string{"--full"}, strings.NewReader(""), &out3); code != 0 {
		t.Fatalf("미구현 Store exit = %d, want 0", code)
	}
}

// errSilent — for injecting metering failures.
type errSilent struct{}

func (errSilent) Error() string { return "계측 실패" }

// TestHotSQLSingleQueryDefinition — the increment ids and
// the pending-suffix valid_at come from the essential hot_view columns — the companion query
// re-gathering the three arm conditions is gone. HotSQL keeps ORDER BY ts DESC, id DESC
// tie-break (CI run 39713526850 ruling — a total order for tied ts) and the budget cap:
// the injected rows and increment ids come from the same query in the same order, so injected set = increment
// set is structurally guaranteed.
func TestHotSQLSingleQueryDefinition(t *testing.T) {
	sql := query.HotSQL("")
	for _, frag := range []string{
		"FROM hot_view",
		"id, valid_at, ingested_at", // increment and suffix columns come from the essential query too
		"ORDER BY ts DESC, id DESC LIMIT " + strconv.Itoa(query.ClampLimit(0)), // budget cap and tie-break
	} {
		if !strings.Contains(sql, frag) {
			t.Errorf("HotSQL에 %q 누락: %s", frag, sql)
		}
	}
}

// TestHotUsageRecordedWithoutIDCells — with no id cells (an old-view-style 4-column row) there are no
// ids to increment; only the single hot inbound record remains (the query itself succeeded).
func TestHotUsageRecordedWithoutIDCells(t *testing.T) {
	ctx := context.Background()
	f := &instrumentedFake{fakeStore: fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM hot_view") {
			return [][]any{{"pending", "토픽", "요약", "2026-10-22"}}, nil
		}
		return nil, nil
	}}}
	var out bytes.Buffer
	if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot exit = %d, want 0", code)
	}
	for _, ids := range f.bumped {
		if len(ids) != 0 {
			t.Errorf("id 셀 부재 시 증분 금지, got %v", f.bumped)
		}
	}
	if len(f.usage) != 1 || f.usage[0] != [2]string{"hot", ""} {
		t.Errorf("RecordUsage = %v, want [[hot ]] (유입 기록 유지)", f.usage)
	}
}

// TestHotBudgetTruncationSkipsCutRows — rows cut by the ClampHot rune budget and absent from the actual
// output are not read_count/last_read_at increment targets (regression:
// unconditionally incrementing the whole top N — reaching even the cut rows — distorted the ranking
// and stale tallies). The budget is set exactly to the first row's formatted length, making the second
// row's start offset == the budget (a full-truncation boundary). Incrementing is on the --full path —
// redirect the call to --full (the headline skip is covered by hot_v0132_test.go).
func TestHotBudgetTruncationSkipsCutRows(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{
		{"pending", "토픽", "요약", "2026-10-22", int32(3), nil, nil},
		{"decision", "잘린행토픽", "잘린행요약", "2026-10-21", int32(7), nil, nil},
	}
	budget := len([]rune(query.FormatRows(hotRows[:1], "kind,topic,summary,ts", false)))
	qfn := func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM hot_view"):
			return hotRows, nil
		case strings.Contains(sql, "FROM meta") && len(args) > 0 && fmt.Sprint(args[0]) == "hot_budget":
			return [][]any{{strconv.Itoa(budget)}}, nil
		}
		return nil, nil
	}
	f := &instrumentedFake{fakeStore: fakeStore{queryFn: qfn}}
	var out bytes.Buffer
	if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot exit = %d, want 0", code)
	}
	if strings.Contains(out.String(), "잘린행토픽") {
		t.Fatalf("예산 절단된 행이 출력에 남음: %q", out.String())
	}
	if len(f.bumped) != 1 || len(f.bumped[0]) != 1 || f.bumped[0][0] != 3 {
		t.Errorf("BumpReads ids = %v, want [3](생존 첫 행만 — 잘린 둘째 행 id 7 제외)", f.bumped)
	}
	if len(f.usage) != 1 || f.usage[0] != [2]string{"hot", ""} {
		t.Errorf("RecordUsage = %v, want [[hot ]]", f.usage)
	}
}

// TestUsageInstrumentationSingleSeam — hot metering goes through the single UsageStore seam
// (write.go recordUsage) (regression: prevents a dual-seam revival — path recording checked with a
// Store implementing only UsageStore). The read kind is excluded from inbound metering
// (source = read_events), so 0 records is asserted alongside.
func TestUsageInstrumentationSingleSeam(t *testing.T) {
	ctx := context.Background()
	rec := &writeUsageRecorder{Store: &fakeStore{queryFn: readQueryFn}}
	var out bytes.Buffer
	if code := runRead(ctx, rec, []string{"topic"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d, want 0", code)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("read 계측 = %v, want [](v0.42.0 T2 — read kind 유입 계측 폐지)", rec.calls)
	}
	rec2 := &writeUsageRecorder{Store: &fakeStore{queryFn: hotQueryFn}}
	var out2 bytes.Buffer
	if code := runHot(ctx, rec2, nil, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("runHot exit = %d, want 0", code)
	}
	if len(rec2.calls) != 1 || rec2.calls[0] != (usageCall{"hot", ""}) {
		t.Fatalf("hot 계측 = %v, want [[hot ]]", rec2.calls)
	}
}

// hotSharedDBLock — mutual exclusion between packages using the shared Doltgres DB (file
// locking). go test ./... launches package binaries in parallel, so without the lock the full-table
// DELETE and pendingRec approval in lib/db newTestRepo can slip between this test's hot_view query
// and the eligibility query, making returned ids lose eligibility between the queries and produce
// the "returned un-injected ids" false positive (CI run
// 39713526850). The lock file path must match lib/db sharedDBLock and lib/backup drillLock for the
// mutual exclusion to hold.
func hotSharedDBLock(t *testing.T) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "flint-drill.lock"),
		os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("잠금 파일: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		t.Fatalf("flock: %v", err)
	}
	t.Cleanup(func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	})
}

// TestHotViewQueryLimitsToInjected — real-DB regression (only when FLINT_DSN is set):
// if 3 active projects occupy the top, the injected knowledge gets only cap (default 10) - 3 slots.
// End-to-end prevention of the unlimited-increment bug (incrementing even un-injected knowledge).
// Verified directly via the id column of the single essential HotSQL query (companion query gone).
func TestHotViewQueryLimitsToInjected(t *testing.T) {
	dsn := os.Getenv("FLINT_DSN")
	if dsn == "" {
		t.Skip("FLINT_DSN 미설정 — 실DB 회귀 건너뜀")
	}
	hotSharedDBLock(t)
	ctx := context.Background()
	real, err := newRealStore(ctx, dsn)
	if err != nil {
		t.Skipf("real store unavailable: %v", err)
	}
	t.Cleanup(real.Close)

	older := time.Now().Add(-2 * time.Hour)
	krecs := make([]map[string]any, 0, 12)
	for i := 0; i < 12; i++ {
		krecs = append(krecs, map[string]any{
			"kind": "knowledge", "topic": fmt.Sprintf("hot-reg-k-%d", i),
			"summary": "회귀", "tags": ":a:", "confidence": 0.9,
			"content_hash": fmt.Sprintf("hot-reg-k-%d", i),
			"status":       "pending", "ingested_at": older,
		})
	}
	// projects have updated=now(), newer than the knowledge (2h ago) — they occupy the top 3 slots.
	precords := make([]map[string]any, 0, 3)
	for i := 0; i < 3; i++ {
		precords = append(precords, map[string]any{
			"kind": "project", "name": fmt.Sprintf("hot-reg-p-%d", i),
			"status": "active",
		})
	}
	if _, err := real.InsertBatch(ctx, krecs); err != nil {
		t.Fatalf("InsertBatch knowledge: %v", err)
	}
	if err != nil {
		t.Fatalf("InsertBatch knowledge: %v", err)
	}
	if _, err := real.InsertBatch(ctx, precords); err != nil {
		t.Fatalf("InsertBatch projects: %v", err)
	}
	t.Cleanup(func() {
		_, _ = real.Query(ctx, `DELETE FROM knowledge WHERE topic LIKE 'hot-reg-k-%'`)
		_, _ = real.Query(ctx, `DELETE FROM projects WHERE name LIKE 'hot-reg-p-%'`)
	})

	// the single essential HotSQL query (single definition) — the knowledge ids to inject are pulled
	// directly from the id column (at the end). The 12 seeded rows all share one ts (a tie); without
	// the ", id DESC" tie-break (CI 39713526850), the set would
	// wobble with planner-arbitrary tie ordering.
	hrows, err := queryOnce(ctx, real, query.HotSQL(""), nil)
	if err != nil {
		t.Fatalf("hot_view 본질의 조회: %v", err)
	}
	got := []int{}
	for _, r := range hrows {
		if len(r) > 4 && fmt.Sprint(r[0]) != "project" {
			if id, ok := render.CellToInt(r[4]); ok {
				got = append(got, id)
			}
		}
	}
	// against shared-DB interference: the expectation is computed from the current number of active
	// projects (the top-slot occupants). With 12 seeded knowledge rows, the eligible knowledge always fills the cap.
	projs, err := queryOnce(ctx, real,
		`SELECT count(*) FROM projects WHERE status = 'active'`, nil)
	if err != nil || len(projs) == 0 || len(projs[0]) == 0 {
		t.Fatalf("활성 프로젝트 수 조회 실패: %v", err)
	}
	nActive := int(projs[0][0].(int64))
	wantN := query.ClampLimit(0) - min(nActive, query.ClampLimit(0))
	if len(got) != wantN {
		t.Fatalf("주입 지식 id 수 = %d, want %d(상한 - 활성 프로젝트 %d): %v",
			len(got), wantN, nActive, got)
	}
	// only the eligible knowledge ids (pending, decided within 7 days) are returned — no cap-overflow leakage.
	krows, err := queryOnce(ctx, real,
		`SELECT id FROM knowledge k WHERE k.invalid_at IS NULL
		   AND (k.status = 'pending'
		        OR (k.ingested_at >= now() - interval '7 days'
		            AND k.tags LIKE '%:adr:%'))`, nil)
	if err != nil {
		t.Fatalf("자격 지식 조회 실패: %v", err)
	}
	kset := map[int]bool{}
	for _, id := range rowIDs(krows) {
		kset[id] = true
	}
	for _, id := range got {
		if !kset[id] {
			t.Errorf("주입 안 된 id %d 반환(프로젝트·상한 밀림 항목 누출)", id)
		}
	}
}
