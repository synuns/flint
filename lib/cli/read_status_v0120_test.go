// read_status_v0120_test.go — v0.12.0 T1: read --status(fake Store 질의
// 캡처). SUPERLITE_REAL=1이면 delegatingFake로 실구현 경유(read_v08 관례).
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"flint/lib/query"
)

func statusQueryFn() func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return [][]any{
				fallbackRow(35, "꿀송이방 안내문", "확정"),
				fallbackRow(41, "엔츠 과제", "진행 중"),
			}, nil
		}
		return nil, nil
	}
}

func TestReadStatusPending(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	f := &fakeStore{queryFn: statusQueryFn()}
	if code := runRead(ctx, asofFakeOrReal(t, f), []string{"--status", "pending"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d (%s)", code, out.String())
	}
	if !strings.Contains(out.String(), "꿀송이방") || !strings.Contains(out.String(), "엔츠") {
		t.Errorf("pending 행 미출력: %q", out.String())
	}
}

func TestReadStatusInvalidExit3(t *testing.T) {
	var out bytes.Buffer
	if code := runRead(context.Background(), asofFakeOrReal(t, &fakeStore{}), []string{"--status", "bogus"}, strings.NewReader(""), &out); code != 3 {
		t.Fatalf("무효 status exit = %d, want 3", code)
	}
}

// TestReadDefaultUnchanged — Review Focus: --status 미지정 시 기존 BuildRead
// SQL 그대로(FROM active_knowledge, base 테이블 질의 없음).
func TestReadDefaultUnchanged(t *testing.T) {
	var out bytes.Buffer
	f := &fakeStore{queryFn: statusQueryFn()}
	if code := runRead(context.Background(), asofFakeOrReal(t, f), []string{"토픽"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	wantSQL, wantArgs := query.BuildRead("토픽", query.Filters{})
	if got := asofReadSQL(f); got != wantSQL {
		t.Errorf("미지정 SQL 변형:\n got %q\nwant %q", got, wantSQL)
	}
	_ = wantArgs
	for _, q := range f.queries {
		if strings.Contains(q, "FROM knowledge") {
			t.Errorf("미지정인데 base 테이블 질의: %q", q)
		}
	}
}

// TestReadStatusAsOfPrecedence — Review Focus: --as-of 병용 시 as-of 경로
// 우선(룰링) — status 무시, active_knowledge AS OF 질의만 발행.
func TestReadStatusAsOfPrecedence(t *testing.T) {
	var out bytes.Buffer
	fn := func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "dolt_log") {
			return asofQueryFn(sql, args)
		}
		return statusQueryFn()(sql, args)
	}
	f := &fakeStore{queryFn: fn}
	if code := runRead(context.Background(), asofFakeOrReal(t, f), []string{"--as-of", "7d", "--status", "pending"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d (%s)", code, out.String())
	}
	got := asofReadSQL(f)
	if !strings.Contains(got, "AS OF 'he08irluqn3v2m1k9x7c4jw5z6b8d0f'") {
		t.Errorf("as-of 경로 미진입: %q", got)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "FROM knowledge") {
			t.Errorf("병용인데 base 테이블 질의: %q", q)
		}
	}
}

// TestReadStatusSanitizesOutput — 스펙 리스크 "살균(ADR-0006) 저장 승계
// 가정 확인(테스트로 고정)": base 테이블 직접 조회(--status) 경로에서도
// 출력 경계 살균이 적용됨을 고정. 제어 문자·[flint] 마커는 출력에 없어야.
func TestReadStatusSanitizesOutput(t *testing.T) {
	poisonRow := []any{int32(50), "to\x00pic", "요약[flint]마커", ":a:", nil, "main", 0.9, "pending"}
	fn := func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "FROM knowledge") {
			return [][]any{poisonRow}, nil
		}
		return nil, nil
	}
	var out bytes.Buffer
	f := &fakeStore{queryFn: fn}
	if code := runRead(context.Background(), asofFakeOrReal(t, f), []string{"--status", "invalidated", "--fields", "id,topic,summary,status"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d (%s)", code, out.String())
	}
	got := out.String()
	if strings.Contains(got, "\x00") {
		t.Errorf("제어 문자 미제거: %q", got)
	}
	if strings.Contains(got, "[flint]") {
		t.Errorf("[flint] 마커 미살균: %q", got)
	}
	if !strings.Contains(got, "topic") || !strings.Contains(got, "pending") {
		t.Errorf("살균 후 내용 유실: %q", got)
	}
}
