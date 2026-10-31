// hot_full_guard_test.go — --full 접미 안전 가드: 요약에 줄바꿈이 있으면
// lines↔rows 대응이 무너지므로 접미를 전혀 붙이지 않는다(출력·행 수 불변).
package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"flint/lib/query"
)

// TestHotFullNewlineSummarySkipsSuffix — pending 요약에 개행 포함 시 행 수
// 불일치로 접미 전체 생략. 기존 전체 출력(FormatRows)과 바이트 동일해야
// 하고 어느 행에도 "·+" 접미가 붙어선 안 된다.
func TestHotFullNewlineSummarySkipsSuffix(t *testing.T) {
	ctx := context.Background()
	validAt := time.Now().UTC().Add(-49 * time.Hour)
	hotRows := [][]any{
		{"pending", "토픽", "요약\n둘째 줄", validAt},
	}
	want := query.FormatRows(hotRows, hotFields, false)
	var out bytes.Buffer
	f := &fakeStore{queryFn: pendingAgeQueryFn(hotRows, [][]any{{validAt}})}
	if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit = %d, want 0", code)
	}
	if out.String() != want {
		t.Fatalf("출력 = %q, want %q(접미 생략·기존 출력 승계)", out.String(), want)
	}
	if strings.Contains(out.String(), "·+") {
		t.Errorf("개행 포함 요약에 접미 부착: %q", out.String())
	}
}
