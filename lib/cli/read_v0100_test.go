// read_v0100_test.go — v0.10.0 T1: 제로히트 did-you-mean 제안 라인·
// 구문 히트 경로 회귀(스펙 §3 R1). 인시던트 기록대로 가짜 검사식은
// 스캔 시그니처로 확장, 질의는 유사도>0이 성립하는 것으로 변경했다.
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// suggestQueryFn — 구문·폴백 모두 제로히트, 제안 스캔은 3행.
func suggestQueryFn() func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "id, topic, summary FROM active_knowledge") {
			return [][]any{
				{int32(7), "flint 백업 절차", "덤프"},
				{int32(8), "환영인사", "인사"},
				{int32(9), "포트 점유 사고", "5433"},
			}, nil
		}
		return nil, nil // 구문·폴백·meta 전부 제로/빈
	}
}

// TestReadZeroHitSuggests — Review Focus: 구문·폴백 모두 제로히트 →
// suggestions 라인에 유사도>0 후보만.
func TestReadZeroHitSuggests(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &fakeStore{queryFn: suggestQueryFn()}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	got := out.String()
	if !strings.Contains(got, `"suggestions"`) || !strings.Contains(got, `"topic":"flint 백업 절차"`) {
		t.Errorf("suggestions 라인 미출력: %q", got)
	}
	if strings.Contains(got, `"topic":"환영인사"`) {
		t.Errorf("유사도 0 항목 제안됨: %q", got)
	}
}

// TestReadPhraseHitNoSuggest — Review Focus: 구문 히트 시 suggestions
// 미발생(히트 경로 완전 불변 — 룰링).
func TestReadPhraseHitNoSuggest(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	fn := func(sql string, args []any) ([][]any, error) {
		// 구문 질의는 readColumns(tags 포함)를 선택 — 제안 스캔과 구분.
		if strings.Contains(sql, "tags") {
			return [][]any{fallbackRow(3, "백업 복구", "요약")}, nil
		}
		return nil, nil
	}
	f := &fakeStore{queryFn: fn}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if got := out.String(); strings.Contains(got, `"suggestions"`) {
		t.Errorf("구문 히트인데 suggestions 출력: %q", got)
	}
}
