// hot_v0132_test.go — v0.13.2 심층 리뷰 회귀: N3 헤드라인 경로 증분 스킵,
// N4 --full 접미 포함 생존 판정(실출력 행=증분 id 집합 정합).
package cli

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"flint/lib/query"
)

// hotN3QueryFn — hot_view 본질의는 hotRows, 동반 id 질의(FROM projects p)는
// idRows, 그 외(pending valid_at 동반 질의 포함)는 빈 결과.
func hotN3QueryFn(hotRows, idRows [][]any) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM hot_view"):
			return hotRows, nil
		case strings.Contains(sql, "FROM projects p"):
			return idRows, nil
		}
		return nil, nil
	}
}

// TestHotHeadlineSkipsReadBump — N3: 기본 헤드라인 경로(비--full)는 지식을
// 실제로 주입하지 않는다(카운트만 노출) — BumpReads 미호출, hot 유입 1회
// 기록만 유지. 수정 전에는 상위 지식 id가 무조건 증분돼 read_count·
// last_read_at을 왜곡하고 stale 판정을 동결시켰다.
func TestHotHeadlineSkipsReadBump(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{
		{"pending", "토픽", "요약", "2026-10-22"},
		{"decision", "토픽2", "요약2", "2026-10-21"},
	}
	f := &instrumentedFake{fakeStore: fakeStore{queryFn: hotN3QueryFn(
		hotRows, [][]any{{int32(3)}, {int32(7)}})}}
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

// TestHotFullStillBumpsInjected — N3 반대편: --full 전체 출력 경로는 실제
// 주입이 일어나므로 생존 지식 id 전수를 계속 증분한다(예산 넉넉 → 전체).
func TestHotFullStillBumpsInjected(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{
		{"pending", "토픽", "요약", "2026-10-22"},
		{"decision", "토픽2", "요약2", "2026-10-21"},
	}
	f := &instrumentedFake{fakeStore: fakeStore{queryFn: hotN3QueryFn(
		hotRows, [][]any{{int32(3)}, {int32(7)}})}}
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

// TestHotFullSuffixSurvivorMatchesOutput — N4: --full pending 접미(·+N일)가
// ClampHot 절단 대상 텍스트에 포함되므로, 생존(증분) 판정도 접미 포함
// 텍스트 기준이어야 한다. 예산을 '접미 포함 첫 행 끝+줄바꿈'으로 잡아
// 둘째 행을 경계에서 잘라면(접미 없는 옛 산정으로는 생존으로 판정되는
// 경계) 실출력에 남은 행과 증분 id 집합이 일치해야 한다.
func TestHotFullSuffixSurvivorMatchesOutput(t *testing.T) {
	ctx := context.Background()
	validAt := time.Now().UTC().Add(-72 * time.Hour) // 내림 3일 → ·+3일
	hotRows := [][]any{
		{"pending", "펜딩토픽", "요약", validAt},
		{"decision", "잘린토픽", "요약2", "2026-10-21"},
		{"decision", "더잘린토픽", "요약3", "2026-10-20"},
	}
	suffix := "·+3일"
	baseLines := strings.Split(strings.TrimSuffix(
		query.FormatRows(hotRows, hotFields, false), "\n"), "\n")
	// 접미 포함 첫 행 끝(+)줄바꿈까지가 예산 — 둘째 행 시작 오프셋==예산.
	// 접미 없는 산정이면 둘째 행 시작(첫 행 기본 포맷+1) < 예산이라 옛
	// 코드는 둘째 행을 생존으로 오판해 id 11까지 증분했다.
	budget := len([]rune(baseLines[0])) + len([]rune(suffix)) + 1
	qfn := func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM hot_view"):
			return hotRows, nil
		case strings.Contains(sql, "FROM meta") && len(args) > 0 &&
			fmt.Sprint(args[0]) == "hot_budget":
			return [][]any{{strconv.Itoa(budget)}}, nil
		case strings.Contains(sql, "FROM projects p"):
			return [][]any{{int32(10)}, {int32(11)}, {int32(12)}}, nil
		case strings.Contains(sql, "FROM knowledge"):
			return [][]any{{validAt}}, nil // pending valid_at 동반 질의
		}
		return nil, nil
	}
	f := &instrumentedFake{fakeStore: fakeStore{queryFn: qfn}}
	var out bytes.Buffer
	if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit = %d, want 0", code)
	}
	// 실출력: 접미 포함 첫 행만 노출, 둘째·셋째 행은 절단.
	if !strings.Contains(out.String(), "펜딩토픽") || !strings.Contains(out.String(), suffix) {
		t.Fatalf("첫 행(접미 포함) 미노출: %q", out.String())
	}
	if strings.Contains(out.String(), "잘린토픽") || strings.Contains(out.String(), "더잘린토픽") {
		t.Fatalf("예산 절단된 행이 출력에 남음: %q", out.String())
	}
	// 증분 집합 = 실출력 생존 집합: 첫 행(id 10)만.
	if len(f.bumped) != 1 || len(f.bumped[0]) != 1 || f.bumped[0][0] != 10 {
		t.Errorf("BumpReads ids = %v, want [10](접미 포함 생존 행만 — 잘린 id 11·12 제외)", f.bumped)
	}
	if len(f.usage) != 1 || f.usage[0] != [2]string{"hot", ""} {
		t.Errorf("RecordUsage = %v, want [[hot ]]", f.usage)
	}
}
