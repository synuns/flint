// read_v096_test.go — v0.9.6 T1: read 제로히트 토큰 폴백(fake Store 질의
// 캡처). SUPERLITE_REAL=1이면 delegatingFake로 실구현 경유(read_v08 관례).
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// fallbackRow — active_knowledge 행 픽스처(readColumns 순서).
func fallbackRow(id int, topic, summary string) []any {
	return []any{int32(id), topic, summary, ":a:", nil, "main", 0.9, "active"}
}

// fallbackQueryFn — 구문 질의는 제로히트, 폴백 OR 질의는 2행(정합수 역순
// 반환 — 재랭킹이 순서를 바로잡는지 검증). meta 읽기/쓰기 키를 기록한다.
func fallbackQueryFn(metaKeys *[]string) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM active_knowledge"):
			if strings.Contains(sql, " OR ") { // 폴백 OR 재쿼리
				return [][]any{
					fallbackRow(1, "백업 절차", "정기 절차"),            // 정합수 1
					fallbackRow(2, "flint 백업", "복구는 저장소 정지 조건"), // 정합수 2
				}, nil
			}
			return nil, nil // 구문 매칭 제로히트
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

// argKey — meta 키 인자를 문자열로(테스트 국소).
func argKey(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// TestReadFallbackOnZeroPhrase — Review Focus: 구문 제로히트 → 토큰 OR
// 재쿼리·정합수 내림 재랭킹·read_fallback_calls 증분.
func TestReadFallbackOnZeroPhrase(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	var metaKeys []string
	f := &fakeStore{queryFn: fallbackQueryFn(&metaKeys)}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업 복구"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d, want 0 (출력 %q)", code, out.String())
	}
	// 폴백 OR 질의 발행.
	var orIssued bool
	for _, q := range f.queries {
		if strings.Contains(q, "FROM active_knowledge") && strings.Contains(q, " OR ") {
			orIssued = true
		}
	}
	if !orIssued {
		t.Errorf("폴백 OR 질의 미발행: %v", f.queries)
	}
	// 재랭킹 — 정합수 2행(정합수 2)이 정합수 1행보다 먼저.
	got := out.String()
	if !strings.HasPrefix(got, "2|flint 백업|복구는 저장소 정지 조건\n1|백업 절차|정기 절차\n") {
		t.Errorf("정합수 내림 재랭킹 실패: %q", got)
	}
	// 계측 — 폴백 시도 1회 증분.
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

// TestReadPhraseHitUnchanged — Review Focus: 구문 히트 시 폴백 미발생
// (OR 질의·계측 없이 기존 출력 그대로).
func TestReadPhraseHitUnchanged(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	fn := func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM active_knowledge"):
			return [][]any{fallbackRow(3, "백업 복구 절차", "요약")}, nil // 구문 히트
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

// TestReadFallbackSkippedOnAsOf — --as-of 질의는 현재 시점 폴백이 시점
// 정합성을 깨므로 제로히트여도 폴백 미발생(룰링).
func TestReadFallbackSkippedOnAsOf(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM dolt_log WHERE date <= $1"):
			return [][]any{{"he08irluqn3v2m1k9x7c4jw5z6b8d0f"}}, nil
		case strings.Contains(sql, "WHERE commit_hash = $1"):
			return [][]any{{time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}}, nil
		case strings.Contains(sql, "FROM active_knowledge"):
			return nil, nil // 과거 시점 제로히트
		}
		return nil, nil
	}}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"--as-of", "7d", "백업 복구"}, strings.NewReader(""), &out); code != 0 {
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

// TestReadFallbackSkippedOnSingleToken — 1-토큰 질의 제로히트는 구문
// 매칭과 동질이라 폴백 없이 기존 제로히트 출력(계측 증분도 없음).
func TestReadFallbackSkippedOnSingleToken(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	var metaKeys []string
	f := &fakeStore{queryFn: fallbackQueryFn(&metaKeys)}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"백업"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runRead exit = %d, want 0", code)
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
