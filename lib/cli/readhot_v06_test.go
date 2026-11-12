// readhot_v06_test.go — T62 read/hot 유입·참조 계측 fast 테스트(fake).
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

// instrumentedFake — fakeStore에 BumpReads·RecordUsage 계측을 얹은 fake.
// 실패 주입(bumpErr·usageErr)으로 조용한 실패 경로를 검증한다.
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

// readQueryFn — active_knowledge 조회는 id 2행, 그 외 meta 질의는 빈 결과.
func readQueryFn(sql string, args []any) ([][]any, error) {
	if strings.Contains(sql, "FROM active_knowledge") {
		return [][]any{{int32(3), "t", "s", ":a:", nil, "main", 0.9, "active"},
			{int32(5), "t2", "s2", ":b:", nil, "main", 0.8, "active"}}, nil
	}
	return nil, nil
}

// TestReadInstrumentsSilently — Review Focus(state): 계측은 결과 항목 id
// 전수에 BumpReads + recordUsage("read",""). 계측 실패(bumpErr)·미구현 Store
// 모두 exit 0·출력 불변.
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
	if len(f.usage) != 1 || f.usage[0] != [2]string{"read", ""} {
		t.Errorf("RecordUsage = %v, want [[read ]]", f.usage)
	}

	// 계측 실패는 조용히 — exit 0·동일 출력.
	var out2 bytes.Buffer
	f2 := &instrumentedFake{fakeStore: fakeStore{queryFn: readQueryFn}, bumpErr: errSilent{}}
	if code := runRead(ctx, f2, []string{"topic"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("bumpErr 시 exit = %d, want 0", code)
	}
	if out2.String() != out.String() {
		t.Errorf("bumpErr 시 출력 변형:\n%q\n%q", out.String(), out2.String())
	}

	// 미구현 Store(plain fakeStore)에서도 no-op — exit 0.
	var out3 bytes.Buffer
	plain := &fakeStore{queryFn: readQueryFn}
	if code := runRead(ctx, plain, []string{"topic"}, strings.NewReader(""), &out3); code != 0 {
		t.Fatalf("미구현 Store exit = %d, want 0", code)
	}
}

// hotQueryFn — hot_view 본질의는 증분 컬럼(id·valid_at·ingested_at)까지
// 포함한 7열 행(지식 2행 id 3·7 — T5 단일 정의).
func hotQueryFn(sql string, args []any) ([][]any, error) {
	if strings.Contains(sql, "FROM hot_view") {
		return [][]any{
			{"pending", "토픽", "요약", "2026-10-22", int32(3), nil, nil},
			{"decision", "토픽2", "요약2", "2026-10-21", int32(7), nil, nil},
		}, nil
	}
	return nil, nil
}

// TestHotInstrumentsSilently — hot 종료 시 주입 지식 id 전수 BumpReads +
// recordUsage("hot",""). 계측 실패·미구현 Store exit 0·출력 불변.
// v0.13.2 N3: 증분은 --full(실제 주입) 경로에서만 — 호출을 --full로 재지정
// (헤드라인 경로 스킵은 hot_v0132_test.go TestHotHeadlineSkipsReadBump 담보).
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

	// usageErr·bumpErr 모두 exit 0·출력 불변.
	var out2 bytes.Buffer
	f2 := &instrumentedFake{fakeStore: fakeStore{queryFn: hotQueryFn}, usageErr: errSilent{}}
	if code := runHot(ctx, f2, []string{"--full"}, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("usageErr 시 exit = %d, want 0", code)
	}
	if out2.String() != out.String() {
		t.Errorf("usageErr 시 출력 변형:\n%q\n%q", out.String(), out2.String())
	}

	// 미구현 Store(plain fakeStore)에서도 no-op — exit 0.
	var out3 bytes.Buffer
	plain := &fakeStore{queryFn: hotQueryFn}
	if code := runHot(ctx, plain, []string{"--full"}, strings.NewReader(""), &out3); code != 0 {
		t.Fatalf("미구현 Store exit = %d, want 0", code)
	}
}

// errSilent — 계측 실패 주입용.
type errSilent struct{}

func (errSilent) Error() string { return "계측 실패" }

// TestHotSQLSingleQueryDefinition — Review Focus(T5 단일 정의): 증분 id·
// pending 접미 valid_at은 hot_view 본질의 컬럼에서 나온다 — 세 팔 조건을
// 다시 모은 동반 질의는 폐지. HotSQL은 ORDER BY ts DESC, id DESC
// tie-break(CI run 39713526850 룰링 — 동률 ts의 완전 순서)와 예산 상한을
// 유지한다: 주입 행·증분 id가 같은 질의·같은 순서에서 나오므로 주입
// 집합=증분 집합이 구조적으로 보장된다.
func TestHotSQLSingleQueryDefinition(t *testing.T) {
	sql := query.HotSQL("")
	for _, frag := range []string{
		"FROM hot_view",
		"id, valid_at, ingested_at", // 증분·접미 컬럼까지 본질의에서
		"ORDER BY ts DESC, id DESC LIMIT " + strconv.Itoa(query.ClampLimit(0)), // 예산 상한·tie-break
	} {
		if !strings.Contains(sql, frag) {
			t.Errorf("HotSQL에 %q 누락: %s", frag, sql)
		}
	}
}

// TestHotUsageRecordedWithoutIDCells — id 셀 부재(구형 뷰 모양 4열 행)면
// 증분 대상 id는 없고 hot 유입 1회 기록만 유지(조회 자체는 성공했으므로).
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

// TestHotBudgetTruncationSkipsCutRows — ClampHot rune 예산에 잘려 실제
// 출력에 남지 않은 행은 read_count/last_read_at 증분 대상이 아니다(회귀:
// 상위 N 전체를 무조건 증분 — 잘린 행까지 올라가 랭킹·stale 집계를
// 왜곡). 예산은 첫 행 포맷 길이와 정확히 같게 잡아 둘째 행 시작
// 오프셋==예산(완전 절단 경계)을 만든다. v0.13.2 N3: 증분은 --full 경로
// — 호출을 --full로 재지정(헤드라인 스킵은 hot_v0132_test.go가 담보).
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

// TestUsageInstrumentationSingleSeam — read·hot 계측이 전부 UsageStore 단일
// seam(write.go recordUsage)을 경유한다(회귀: read.go usageRecorder 이중
// seam 부활 방지 — UsageStore만 구현한 Store로 전 경로 기록 확인).
func TestUsageInstrumentationSingleSeam(t *testing.T) {
	ctx := context.Background()
	rec := &writeUsageRecorder{Store: &fakeStore{queryFn: readQueryFn}}
	var out bytes.Buffer
	if code := runRead(ctx, rec, []string{"topic"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d, want 0", code)
	}
	if len(rec.calls) != 1 || rec.calls[0] != (usageCall{"read", ""}) {
		t.Fatalf("read 계측 = %v, want [read ]]", rec.calls)
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

// hotSharedDBLock — 공유 Doltgres DB를 쓰는 패키지 간 상호 배제(파일
// 잠금, v0.5 선례 e1704b8). go test ./...는 패키지 바이너리를 병렬로
// 띄우므로 잠금 없이는 lib/db newTestRepo의 테이블 전체 DELETE·pendingRec
// 승인이 이 테스트의 hot_view 조회와 자격 조회 사이에 끼어, 반환 id가
// 조회 사이에 자격을 잃어 "주입 안 된 id 반환" 오탐을 낸다(CI run
// 39713526850). 잠금 파일 경로는 lib/db sharedDBLock·lib/backup drillLock과
// 동일해야 상호 배제가 성립한다.
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

// TestHotViewQueryLimitsToInjected — 실DB 회귀(FLINT_DSN 설정 시에만):
// 활성 프로젝트 3건이 상위를 차지하면 주입 지식은 상한(기본 10) - 3건뿐이다.
// 무제한 증분이었던 버그(주입 안 된 지식까지 증분)의 엔드투엔드 방지.
// v0.41.0 T5: 본질의 HotSQL 한 질의의 id 컬럼으로 직접 확인(동반 질의 폐지).
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
	// 프로젝트는 updated=now()로 지식(2시간 전)보다 최신 — 상위 3슬롯 점유.
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

	// 본질의 HotSQL 한 질의(T5 단일 정의) — id 컬럼(말미)에서 주입될
	// 지식 id를 직접 뽑는다. 시드 12건은 전부 동일 ts(동률)라 ", id DESC"
	// tie-break(CI 39713526850)가 없으면 플래너 임의 동률 순서로 집합이
	// 흔들렸다.
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
	// 공유 DB 간섭 대비: 기대치는 현재 활성 프로젝트 수로 계산(상위 슬롯
	// 점유분). 시드 지식 12건이 있어 자격 지식은 항상 상한을 채운다.
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
	// 자격 지식(pending·결정 7일) id 전수만 반환 — 상한 밀림 항목 누출 없음.
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
