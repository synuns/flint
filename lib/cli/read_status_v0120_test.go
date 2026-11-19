// read_status_v0120_test.go — read --status (fake Store query
// capture). With SUPERLITE_REAL=1, a delegatingFake goes through the real implementation (the read_v08 convention).
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/query"
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

// TestReadDefaultUnchanged — without --status, the existing BuildRead
// SQL as-is (FROM active_knowledge, no base-table query).
func TestReadDefaultUnchanged(t *testing.T) {
	var out bytes.Buffer
	f := &fakeStore{queryFn: statusQueryFn()}
	if code := runRead(context.Background(), asofFakeOrReal(t, f), []string{"토픽"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d, want 0(제로히트 exit 0 — v1.5.3)", code)
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

// TestReadStatusAsOfPrecedence — combined with --as-of, the as-of path takes
// precedence (ruling) — status ignored; only the past-snapshot (QueryAsOf) query is issued.
func TestReadStatusAsOfPrecedence(t *testing.T) {
	var out bytes.Buffer
	f := &readAsofFake{fakeStore: &fakeStore{queryFn: statusQueryFn()}}
	if code := runRead(context.Background(), readAsofStoreOrReal(t, f), []string{"--as-of", "7d", "--status", "pending"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit = %d (%s)", code, out.String())
	}
	if len(f.asofQueries) != 1 || f.asofQueries[0].Hash != asofHash {
		t.Errorf("as-of 경로 미진입: %+v", f.asofQueries)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "FROM knowledge") {
			t.Errorf("병용인데 base 테이블 질의: %q", q)
		}
	}
}

// TestReadStatusSanitizesOutput — fixing the spec risk "sanitization (ADR-0006) storage-inheritance
// assumption" (pinned by test): even on the direct base-table query (--status) path,
// output-boundary sanitization applies. Control characters and the [flint] marker must not appear in the output.
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
