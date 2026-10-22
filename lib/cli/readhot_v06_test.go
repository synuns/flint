// readhot_v06_test.go — T62 read/hot 유입·참조 계측 fast 테스트(fake).
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
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
	if strings.Contains(sql, "SELECT k.id FROM knowledge") {
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
