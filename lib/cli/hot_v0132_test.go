// hot_v0132_test.go — the headline path skips increments;
// --full survival judgment includes the suffix (actual-output rows = increment id-set consistency).
package cli

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/query"
)

// hotN3QueryFn — the hot_view essential query returns hotRows (including the increment id and valid_at columns);
// other queries return empty results.
func hotN3QueryFn(hotRows [][]any) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM hot_view"):
			return hotRows, nil
		}
		return nil, nil
	}
}

// TestHotHeadlineSkipsReadBump — N3: the default headline path (without --full) does not actually inject knowledge
// (counts only) — BumpReads is not called; only the single hot inbound record
// remains. Before the fix, the top knowledge ids were unconditionally incremented, distorting read_count and
// last_read_at and freezing the stale determination.
func TestHotHeadlineSkipsReadBump(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{
		{"pending", "토픽", "요약", "2026-10-22", int32(3), nil, nil},
		{"decision", "토픽2", "요약2", "2026-10-21", int32(7), nil, nil},
	}
	f := &instrumentedFake{fakeStore: fakeStore{queryFn: hotN3QueryFn(hotRows)}}
	var out bytes.Buffer
	if code := runHot(ctx, f, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot exit = %d, want 0", code)
	}
	if len(f.bumped) != 0 {
		t.Errorf("헤드라인 경로 BumpReads 호출 금지(실제 주입 없음), got %v", f.bumped)
	}
	if len(f.usage) != 1 || f.usage[0] != [2]string{"hot", ""} {
		t.Errorf("RecordUsage = %v, want [[hot ]](유입 1회 기록은 유지)", f.usage)
	}
	if !strings.Contains(out.String(), "[flint]") {
		t.Errorf("헤드라인 미출력: %q", out.String())
	}
}

// TestHotFullStillBumpsInjected — the N3 flip side: the --full full-output path does actually
// inject, so it keeps incrementing every surviving knowledge id (generous budget -> everything).
// The ids come directly from the id column of the essential hot_view query.
func TestHotFullStillBumpsInjected(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{
		{"pending", "토픽", "요약", "2026-10-22", int32(3), nil, nil},
		{"decision", "토픽2", "요약2", "2026-10-21", int32(7), nil, nil},
	}
	f := &instrumentedFake{fakeStore: fakeStore{queryFn: hotN3QueryFn(hotRows)}}
	var out bytes.Buffer
	if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit = %d, want 0", code)
	}
	if len(f.bumped) != 1 || len(f.bumped[0]) != 2 ||
		f.bumped[0][0] != 3 || f.bumped[0][1] != 7 {
		t.Errorf("BumpReads ids = %v, want [3 7](--full 주입 지식 전수)", f.bumped)
	}
	if len(f.usage) != 1 || f.usage[0] != [2]string{"hot", ""} {
		t.Errorf("RecordUsage = %v, want [[hot ]]", f.usage)
	}
}

// TestHotFullSuffixSurvivorMatchesOutput — N4: the --full pending suffix (+N days) is
// part of the text subject to ClampHot truncation, so the survival (increment) judgment must also
// use the suffix-included text. Setting the budget to 'the first row's end including the suffix + newline'
// cuts the second row exactly at the boundary (a boundary the old suffix-excluded estimate would judge as
// surviving), so the rows left in the actual output must match the increment id set.
func TestHotFullSuffixSurvivorMatchesOutput(t *testing.T) {
	ctx := context.Background()
	validAt := time.Now().UTC().Add(-72 * time.Hour) // rounded down to 3 days -> ·+3d
	hotRows := [][]any{
		{"pending", "펜딩토픽", "요약", validAt, int32(10), validAt, validAt},
		{"decision", "잘린토픽", "요약2", "2026-10-21", int32(11), nil, nil},
		{"decision", "더잘린토픽", "요약3", "2026-10-20", int32(12), nil, nil},
	}
	suffix := "·+3d"
	baseLines := strings.Split(strings.TrimSuffix(
		query.FormatRows(hotRows, hotFields, false), "\n"), "\n")
	// the budget is up to the first row's end (suffix included) + newline — the second row's start offset == the budget.
	// Under a suffix-excluded estimate, the second row's start (first row's base format + 1) < the budget, so the old
	// code misjudged the second row as surviving and incremented up to id 11.
	budget := len([]rune(baseLines[0])) + len([]rune(suffix)) + 1
	qfn := func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM hot_view"):
			return hotRows, nil
		case strings.Contains(sql, "FROM meta") && len(args) > 0 &&
			fmt.Sprint(args[0]) == "hot_budget":
			return [][]any{{strconv.Itoa(budget)}}, nil
		}
		return nil, nil
	}
	f := &instrumentedFake{fakeStore: fakeStore{queryFn: qfn}}
	var out bytes.Buffer
	if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit = %d, want 0", code)
	}
	// actual output: only the first row (suffix included) is exposed; rows 2 and 3 are cut.
	if !strings.Contains(out.String(), "펜딩토픽") || !strings.Contains(out.String(), suffix) {
		t.Fatalf("첫 행(접미 포함) 미노출: %q", out.String())
	}
	if strings.Contains(out.String(), "잘린토픽") || strings.Contains(out.String(), "더잘린토픽") {
		t.Fatalf("예산 절단된 행이 출력에 남음: %q", out.String())
	}
	// the increment set = the actual-output survivor set: only the first row (id 10).
	if len(f.bumped) != 1 || len(f.bumped[0]) != 1 || f.bumped[0][0] != 10 {
		t.Errorf("BumpReads ids = %v, want [10](접미 포함 생존 행만 — 잘린 id 11·12 제외)", f.bumped)
	}
	if len(f.usage) != 1 || f.usage[0] != [2]string{"hot", ""} {
		t.Errorf("RecordUsage = %v, want [[hot ]]", f.usage)
	}
}
