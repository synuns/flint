// read_v0100_test.go — the zero-hit did-you-mean suggestion line and
// the phrase-hit path regression (spec §3 R1).
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// suggestQueryFn — both phrase and fallback zero-hit; the suggestion scan returns 3 rows.
func suggestQueryFn() func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "id, topic, summary FROM active_knowledge") {
			return [][]any{
				{int32(7), "flint 백업 절차", "덤프"},
				{int32(8), "환영인사", "인사"},
				{int32(9), "포트 점유 사고", "5433"},
			}, nil
		}
		return nil, nil // phrase, fallback, and meta all zero/empty
	}
}

// TestReadZeroHitSuggests — rewritten (ruling): the suggestion line is gone —
// if both phrase and fallback zero-hit, fully no output, exit 1. Even with scan candidates,
// they stay invisible to the agent (internal classification only via zero_near metering).
func TestReadZeroHitSuggests(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &fakeStore{queryFn: suggestQueryFn()}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업 복구 절차"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
	}
	if got := out.String(); got != "" {
		t.Errorf("제로히트 출력 = %q, want \"\"", got)
	}
}

// TestReadPhraseHitNoSuggest — on a phrase hit, no suggestions
// (the hit path fully unchanged — ruling).
func TestReadPhraseHitNoSuggest(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	fn := func(sql string, args []any) ([][]any, error) {
		// the phrase query selects readColumns (including tags) — distinguishing it from the suggestion scan.
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
