// readhot_v06_test.go — T62 read/hot 유입·참조 계측 fast 테스트(fake).
package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"flint/lib/query"
)

// instrumentedFake — fakeStore에 BumpReads·RecordUsage 계측을 얹은 fake.
// 실패 주입(bumpErr·usageErr)으로 조용한 실패 경로를 검증한다.
type instrumentedFake struct {
	fakeStore
	bumped  [][]int
	usage   [][2]string
	bumpErr error
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

// hotQueryFn — hot_view 본질의는 1행, 지식 id 동반 질의는 [3 7].
func hotQueryFn(sql string, args []any) ([][]any, error) {
	if strings.Contains(sql, "FROM hot_view") {
		return [][]any{{"pending", "토픽", "요약", "2026-10-22"}}, nil
	}
	if strings.Contains(sql, "FROM projects p") {
		return [][]any{{int32(3)}, {int32(7)}}, nil
	}
	return nil, nil
}

// TestHotInstrumentsSilently — hot 종료 시 주입 지식 id 전수 BumpReads +
// recordUsage("hot",""). 계측 실패·미구현 Store exit 0·출력 불변.
func TestHotInstrumentsSilently(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &instrumentedFake{fakeStore: fakeStore{queryFn: hotQueryFn}}
	code := runHot(ctx, f, nil, strings.NewReader(""), &out)
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
	if code := runHot(ctx, f2, nil, strings.NewReader(""), &out2); code != 0 {
		t.Fatalf("usageErr 시 exit = %d, want 0", code)
	}
	if out2.String() != out.String() {
		t.Errorf("usageErr 시 출력 변형:\n%q\n%q", out.String(), out2.String())
	}

	// 미구현 Store(plain fakeStore)에서도 no-op — exit 0.
	var out3 bytes.Buffer
	plain := &fakeStore{queryFn: hotQueryFn}
	if code := runHot(ctx, plain, nil, strings.NewReader(""), &out3); code != 0 {
		t.Fatalf("미구현 Store exit = %d, want 0", code)
	}
}

// errSilent — 계측 실패 주입용.
type errSilent struct{}

func (errSilent) Error() string { return "계측 실패" }

// TestHotCompanionQueryMirrorsHotSQL — 동반 질의가 HotSQL과 같은 상한을
// 상위 N행 선택 시점에 적용하고(프로젝트 팔 포함 순위), 그 뒤에 지식만
// 걸러낸다. 무제한 재조합(주입되지 않은 지식 전수 증분) 회귀 방지.
func TestHotCompanionQueryMirrorsHotSQL(t *testing.T) {
	sql := hotKnowledgeIDsSQL()
	for _, frag := range []string{
		"FROM projects p WHERE p.status = 'active'", // 뷰 팔 1: 순위에 포함
		"k.status = 'pending'",                      // 뷰 팔 2
		"interval '7 days'",                         // 뷰 팔 3
		"k.domain = 'decision'",
		"ORDER BY ts DESC LIMIT " + strconv.Itoa(query.ClampLimit(0)), // HotSQL 동일 상한
		") top WHERE id IS NOT NULL", // 상한 적용 '후' 지식만 필터
	} {
		if !strings.Contains(sql, frag) {
			t.Errorf("동반 질의에 %q 누락: %s", frag, sql)
		}
	}
	// LIMIT이 id IS NOT NULL 필터보다 안쪽(먼저 적용)이어야 한다.
	lim := strings.Index(sql, "LIMIT")
	notNull := strings.Index(sql, "WHERE id IS NOT NULL")
	if lim < 0 || notNull < 0 || lim > notNull {
		t.Errorf("LIMIT(%d)는 id IS NOT NULL(%d)보다 먼저 적용돼야 함: %s", lim, notNull, sql)
	}
}

// TestHotUsageRecordedWhenCompanionFails — 동반 질의 실패 시 증분은
// 건너뛰되 hot 유입 1회 기록은 유지(조회 자체는 성공했으므로).
func TestHotUsageRecordedWhenCompanionFails(t *testing.T) {
	ctx := context.Background()
	failCompanion := func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM projects p") {
			return nil, errSilent{}
		}
		if strings.Contains(sql, "FROM hot_view") {
			return [][]any{{"pending", "토픽", "요약", "2026-10-22"}}, nil
		}
		return nil, nil
	}
	f := &instrumentedFake{fakeStore: fakeStore{queryFn: failCompanion}}
	var out bytes.Buffer
	if code := runHot(ctx, f, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot exit = %d, want 0", code)
	}
	if len(f.bumped) != 0 {
		t.Errorf("동반 질의 실패 시 BumpReads 호출 자체 금지, got %v", f.bumped)
	}
	if len(f.usage) != 1 || f.usage[0] != [2]string{"hot", ""} {
		t.Errorf("RecordUsage = %v, want [[hot ]] (유입 기록 유지)", f.usage)
	}
}

// TestHotCompanionQueryLimitsToInjected — 실DB 회귀(FLINT_DSN 설정 시에만):
// 활성 프로젝트 3건이 상위를 차지하면 주입 지식은 상한(기본 10) - 3건뿐이다.
// 무제한 동반 질의였던 버그(주입 안 된 지식까지 증분)의 엔드투엔드 방지.
func TestHotCompanionQueryLimitsToInjected(t *testing.T) {
	dsn := os.Getenv("FLINT_DSN")
	if dsn == "" {
		t.Skip("FLINT_DSN 미설정 — 실DB 회귀 건너뜀")
	}
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
			"status": "pending", "ingested_at": older,
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

	rows, err := queryOnce(ctx, real, hotKnowledgeIDsSQL(), nil)
	if err != nil {
		t.Fatalf("동반 질의: %v", err)
	}
	got := rowIDs(rows)
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
		t.Fatalf("동반 질의 id 수 = %d, want %d(상한 - 활성 프로젝트 %d): %v",
			len(got), wantN, nActive, got)
	}
	// 자격 지식(pending·결정 7일) id 전수만 반환 — 상한 밀림 항목 누출 없음.
	krows, err := queryOnce(ctx, real,
		`SELECT id FROM knowledge k WHERE k.invalid_at IS NULL
		   AND (k.status = 'pending'
		        OR (k.ingested_at >= now() - interval '7 days'
		            AND k.domain = 'decision'))`, nil)
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
