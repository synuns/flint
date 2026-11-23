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

	"github.com/synuns/flint/internal/testengine"
	"github.com/synuns/flint/lib/query"
	"github.com/synuns/flint/lib/render"
)

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

func readQueryFn(sql string, args []any) ([][]any, error) {
	if strings.Contains(sql, "FROM active_knowledge") {
		return [][]any{{int32(3), "t", "s", ":a:", nil, "main", 0.9, "active"},
			{int32(5), "t2", "s2", ":b:", nil, "main", 0.8, "active"}}, nil
	}
	return nil, nil
}

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

	if len(f.usage) != 0 {
		t.Errorf("RecordUsage = %v, want [](read kind 유입 계측 폐지)", f.usage)
	}

	var out2 bytes.Buffer
	f2 := &instrumentedFake{fakeStore: fakeStore{queryFn: readQueryFn}, bumpErr: errSilent{}}
	if code := runRead(ctx, f2, []string{"topic"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("bumpErr 시 exit = %d, want 0", code)
	}
	if out2.String() != out.String() {
		t.Errorf("bumpErr 시 출력 변형:\n%q\n%q", out.String(), out2.String())
	}

	var out3 bytes.Buffer
	plain := &fakeStore{queryFn: readQueryFn}
	if code := runRead(ctx, plain, []string{"topic"}, strings.NewReader(""), &out3); code != 0 {
		t.Fatalf("미구현 Store exit = %d, want 0", code)
	}
}

func hotQueryFn(sql string, args []any) ([][]any, error) {
	if strings.Contains(sql, "FROM hot_view") {
		return [][]any{
			{"pending", "토픽", "요약", "2026-10-22", int32(3), nil, nil},
			{"decision", "토픽2", "요약2", "2026-10-21", int32(7), nil, nil},
		}, nil
	}
	return nil, nil
}

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

	var out2 bytes.Buffer
	f2 := &instrumentedFake{fakeStore: fakeStore{queryFn: hotQueryFn}, usageErr: errSilent{}}
	if code := runHot(ctx, f2, []string{"--full"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("usageErr 시 exit = %d, want 0", code)
	}
	if out2.String() != out.String() {
		t.Errorf("usageErr 시 출력 변형:\n%q\n%q", out.String(), out2.String())
	}

	var out3 bytes.Buffer
	plain := &fakeStore{queryFn: hotQueryFn}
	if code := runHot(ctx, plain, []string{"--full"}, strings.NewReader(""), &out3); code != 0 {
		t.Fatalf("미구현 Store exit = %d, want 0", code)
	}
}

type errSilent struct{}

func (errSilent) Error() string { return "계측 실패" }

func TestHotSQLSingleQueryDefinition(t *testing.T) {
	sql := query.HotSQL("")
	for _, frag := range []string{
		"FROM hot_view",
		"id, valid_at, ingested_at",
		"ORDER BY ts DESC, id DESC LIMIT " + strconv.Itoa(query.ClampLimit(0)),
	} {
		if !strings.Contains(sql, frag) {
			t.Errorf("HotSQL에 %q 누락: %s", frag, sql)
		}
	}
}

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

func TestHotViewQueryLimitsToInjected(t *testing.T) {
	if err := testengine.EnsureEngine(context.Background()); err != nil {
		t.Skipf("전용 테스트 엔진 기동 불가: %v", err)
	}
	dsn := testengine.ResolveDSN()
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
