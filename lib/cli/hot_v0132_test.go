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

func hotN3QueryFn(hotRows [][]any) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM hot_view"):
			return hotRows, nil
		}
		return nil, nil
	}
}

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

func TestHotFullSuffixSurvivorMatchesOutput(t *testing.T) {
	ctx := context.Background()
	validAt := time.Now().UTC().Add(-72 * time.Hour)
	hotRows := [][]any{
		{"pending", "펜딩토픽", "요약", validAt, int32(10), validAt, validAt},
		{"decision", "잘린토픽", "요약2", "2026-10-21", int32(11), nil, nil},
		{"decision", "더잘린토픽", "요약3", "2026-10-20", int32(12), nil, nil},
	}
	suffix := "·+3d"
	baseLines := strings.Split(strings.TrimSuffix(
		query.FormatRows(hotRows, hotFields, false), "\n"), "\n")

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

	if !strings.Contains(out.String(), "펜딩토픽") || !strings.Contains(out.String(), suffix) {
		t.Fatalf("첫 행(접미 포함) 미노출: %q", out.String())
	}
	if strings.Contains(out.String(), "잘린토픽") || strings.Contains(out.String(), "더잘린토픽") {
		t.Fatalf("예산 절단된 행이 출력에 남음: %q", out.String())
	}

	if len(f.bumped) != 1 || len(f.bumped[0]) != 1 || f.bumped[0][0] != 10 {
		t.Errorf("BumpReads ids = %v, want [10](접미 포함 생존 행만 — 잘린 id 11·12 제외)", f.bumped)
	}
	if len(f.usage) != 1 || f.usage[0] != [2]string{"hot", ""} {
		t.Errorf("RecordUsage = %v, want [[hot ]]", f.usage)
	}
}
