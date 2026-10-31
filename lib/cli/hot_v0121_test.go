// hot_v0121_test.go — R2 pending 팔 대기일수(v0.12.1): 24h 미만 빈 문자열,
// 경과 일수 내림 "·+N일" 접미. --full pending 행 끝에만 붙는다.
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"flint/lib/query"
)

func TestPendingAgeSuffix(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	if got := pendingAgeSuffix(now.Add(-30*time.Hour), now); got != "·+1일" {
		t.Errorf("30시간 = %q, want ·+1일", got)
	}
	if got := pendingAgeSuffix(now.Add(-2*time.Hour), now); got != "" {
		t.Errorf("2시간 = %q, want 빈 문자열", got)
	}
	if got := pendingAgeSuffix(now.Add(-49*time.Hour), now); got != "·+2일" {
		t.Errorf("49시간 = %q, want ·+2일", got)
	}
}

// pendingAgeQueryFn — hot_view 본질의는 hotRows, pending valid_at 동반 질의는
// validAts(빈 슬라이스면 부재), 그 외 질의는 빈 결과.
func pendingAgeQueryFn(hotRows, validAts [][]any) func(string, []any) ([][]any, error) {
	return func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM hot_view"):
			return hotRows, nil
		case strings.Contains(sql, "FROM knowledge"):
			return validAts, nil
		}
		return nil, nil
	}
}

// TestHotFullPendingAgeAppended — --full pending 행 끝에 대기일수 접미.
// 49h 경과(내림 2일)는 now 캡처 오차 여유를 둔 값이다.
func TestHotFullPendingAgeAppended(t *testing.T) {
	ctx := context.Background()
	validAt := time.Now().UTC().Add(-49 * time.Hour)
	hotRows := [][]any{
		{"pending", "토픽", "요약", validAt},
		{"decision", "토픽2", "요약2", validAt},
	}
	var out bytes.Buffer
	f := &fakeStore{queryFn: pendingAgeQueryFn(hotRows, [][]any{{validAt}})}
	if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit = %d, want 0", code)
	}
	gotLines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	wantLines := strings.Split(strings.TrimSuffix(query.FormatRows(hotRows, hotFields, false), "\n"), "\n")
	if len(gotLines) != len(wantLines) {
		t.Fatalf("행 수 = %d, want %d: %q", len(gotLines), len(wantLines), out.String())
	}
	// pending 행은 기존 포맷 + 대기일수 접미(49h → 내림 2일).
	if gotLines[0] != wantLines[0]+"·+2일" {
		t.Errorf("pending 행 = %q, want %q+·+2일", gotLines[0], wantLines[0])
	}
	if gotLines[1] != wantLines[1] {
		t.Errorf("decision 행 변형 = %q, want %q", gotLines[1], wantLines[1])
	}
}

// TestHotFullPendingAgeOmittedWithoutValidAt — 동반 질의 부재(0행)·오류 모두
// 접미 생략, 기존 전체 출력 승계(출력·exit 불변).
func TestHotFullPendingAgeOmittedWithoutValidAt(t *testing.T) {
	ctx := context.Background()
	hotRows := [][]any{{"pending", "토픽", "요약", "2026-10-22"}}
	want := query.FormatRows(hotRows, hotFields, false)
	for name, qfn := range map[string]func(string, []any) ([][]any, error){
		"동반 질의 0행": pendingAgeQueryFn(hotRows, nil),
		"동반 질의 오류": func(sql string, args []any) ([][]any, error) {
			if strings.Contains(sql, "FROM knowledge") {
				return nil, errFake{}
			}
			return pendingAgeQueryFn(hotRows, nil)(sql, args)
		},
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
