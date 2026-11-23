package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func fallbackRow(id int, topic, summary string) []any {
	return []any{int32(id), topic, summary, ":a:", nil, "main", 0.9, "active"}
}

func fallbackQueryFn(metaKeys *[]string) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM active_knowledge"):
			if strings.Contains(sql, " OR ") {
				return [][]any{
					fallbackRow(1, "백업 절차", "정기 절차"),
					fallbackRow(2, "flint 백업", "복구는 저장소 정지 조건"),
				}, nil
			}
			return nil, nil
		case strings.Contains(sql, "SELECT value FROM meta"):
			if len(args) > 0 {
				*metaKeys = append(*metaKeys, "get:"+argKey(args[0]))
			}
			return nil, nil
		case strings.Contains(sql, "INSERT INTO meta"):
			if len(args) > 0 {
				*metaKeys = append(*metaKeys, "set:"+argKey(args[0]))
			}
			return nil, nil
		}
		return nil, nil
	}
}

func argKey(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func TestReadFallbackOnZeroPhrase(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	var metaKeys []string
	f := &fakeStore{queryFn: fallbackQueryFn(&metaKeys)}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업 복구"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d, want 0 (출력 %q)", code, out.String())
	}

	var orIssued bool
	for _, q := range f.queries {
		if strings.Contains(q, "FROM active_knowledge") && strings.Contains(q, " OR ") {
			orIssued = true
		}
	}
	if !orIssued {
		t.Errorf("폴백 OR 질의 미발행: %v", f.queries)
	}

	got := out.String()
	if !strings.HasPrefix(got, "2|flint 백업|복구는 저장소 정지 조건\n1|백업 절차|정기 절차\n") {
		t.Errorf("정합수 내림 재랭킹 실패: %q", got)
	}

	var bumped bool
	for _, k := range metaKeys {
		if k == "set:read_fallback_calls" {
			bumped = true
		}
	}
	if !bumped {
		t.Errorf("read_fallback_calls 증분 없음: %v", metaKeys)
	}
}

func TestReadPhraseHitUnchanged(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	fn := func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM active_knowledge"):
			return [][]any{fallbackRow(3, "백업 복구 절차", "요약")}, nil
		}
		return nil, nil
	}
	f := &fakeStore{queryFn: fn}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업 복구"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d, want 0", code)
	}
	if got := out.String(); got != "3|백업 복구 절차|요약\n" {
		t.Errorf("구문 히트 출력 변형: %q", got)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "FROM active_knowledge") && strings.Contains(q, " OR ") {
			t.Errorf("구문 히트인데 폴백 OR 질의 발행: %q", q)
		}
	}
}

func TestReadFallbackSkippedOnAsOf(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{}, zeroHit: true}
	if code := runRead(ctx, readAsofStoreOrReal(t, f), []string{"--as-of", "7d", "백업 복구"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d, want 0 (출력 %q)", code, out.String())
	}
	for _, q := range f.queries {
		if strings.Contains(q, "FROM active_knowledge") && strings.Contains(q, " OR ") {
			t.Errorf("as-of 질의에 폴백 OR 발행: %q", q)
		}
	}
	if got := out.String(); !strings.Contains(got, `"as_of"`) {
		t.Errorf("as_of 첨부 누락: %q", got)
	}
}

func TestReadFallbackSkippedOnSingleToken(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	var metaKeys []string
	f := &fakeStore{queryFn: fallbackQueryFn(&metaKeys)}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
	}
	if got := out.String(); got != "" {
		t.Errorf("1-토큰 제로히트 출력 있음: %q", got)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "FROM active_knowledge") && strings.Contains(q, " OR ") {
			t.Errorf("1-토큰 질의에 폴백 OR 발행: %q", q)
		}
	}
	for _, k := range metaKeys {
		if k == "set:read_fallback_calls" {
			t.Errorf("폴백 미시도인데 read_fallback_calls 증분")
		}
	}
}
