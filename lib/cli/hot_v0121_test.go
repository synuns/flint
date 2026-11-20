// hot_v0121_test.go — pending-arm waiting days: an empty string under 24h,
// otherwise the elapsed days floored, as a "·+Nd" suffix. Appended only to --full pending rows.
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/query"
)

func TestPendingAgeSuffix(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	if got := pendingAgeSuffix(now.Add(-30*time.Hour), now); got != "·+1d" {
		t.Errorf("30시간 = %q, want ·+1d", got)
	}
	if got := pendingAgeSuffix(now.Add(-2*time.Hour), now); got != "" {
		t.Errorf("2시간 = %q, want 빈 문자열", got)
	}
	if got := pendingAgeSuffix(now.Add(-49*time.Hour), now); got != "·+2d" {
		t.Errorf("49시간 = %q, want ·+2d", got)
	}
}

// pendingAgeQueryFn — the hot_view essential query returns hotRows (with id, valid_at, and
// ingested_at columns at the end — a single definition); other queries return empty results.
func pendingAgeQueryFn(hotRows [][]any) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM hot_view"):
			return hotRows, nil
		}
		return nil, nil
	}
}

// TestHotFullPendingAgeAppended — the waiting-days suffix at the end of --full pending rows.
// valid_at is read directly from the essential columns. 49h elapsed (floored to 2 days) leaves
// a margin for now-capture error.
func TestHotFullPendingAgeAppended(t *testing.T) {
	ctx := context.Background()
	validAt := time.Now().UTC().Add(-49 * time.Hour)
	hotRows := [][]any{
		{"pending", "토픽", "요약", validAt, int32(3), validAt, validAt},
		{"decision", "토픽2", "요약2", validAt, int32(7), validAt, validAt},
	}
	var out bytes.Buffer
	f := &fakeStore{queryFn: pendingAgeQueryFn(hotRows)}
	if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit = %d, want 0", code)
	}
	gotLines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	wantLines := strings.Split(strings.TrimSuffix(query.FormatRows(hotRows, hotFields, false), "\n"), "\n")
	if len(gotLines) != len(wantLines) {
		t.Fatalf("행 수 = %d, want %d: %q", len(gotLines), len(wantLines), out.String())
	}
	// pending rows are the existing format + the waiting-days suffix (49h -> floored 2 days).
	if gotLines[0] != wantLines[0]+"·+2d" {
		t.Errorf("pending 행 = %q, want %q+·+2d", gotLines[0], wantLines[0])
	}
	if gotLines[1] != wantLines[1] {
		t.Errorf("decision 행 변형 = %q, want %q", gotLines[1], wantLines[1])
	}
}

// TestHotFullPendingAgeOmittedWithoutValidAt — a missing essential valid_at cell
// (an old-view-style 4-column row) and non-time.Time values both omit the suffix, inheriting the
// existing full output (output and exit unchanged — the single-definition version of the 0-row companion-query and error-inheritance contract).
func TestHotFullPendingAgeOmittedWithoutValidAt(t *testing.T) {
	ctx := context.Background()
	noCell := [][]any{{"pending", "토픽", "요약", "2026-10-22"}}
	notTime := [][]any{{"pending", "토픽", "요약", "2026-10-22", int32(3), "not-time", nil}}
	want := query.FormatRows(noCell, hotFields, false)
	for name, qfn := range map[string]func(string, []any) ([][]any, error){
		"valid_at 셀 부재":  pendingAgeQueryFn(noCell),
		"time.Time 아닌 값": pendingAgeQueryFn(notTime),
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			f := &fakeStore{queryFn: qfn}
			if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
				t.Fatalf("runHot --full exit = %d, want 0", code)
			}
			if out.String() != want {
				t.Fatalf("출력 = %q, want %q(기존 전체 출력 승계)", out.String(), want)
			}
		})
	}
}
