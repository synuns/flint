// hot_full_guard_test.go — --full 접미 안전 가드: 요약에 줄바꿈이 있으면
// lines↔rows 대응이 무너지므로 접미를 전혀 붙이지 않는다(출력·행 수 불변).
package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestHotFullNewlineSummarySkipsSuffix — 개행 포함 요약은 v0.20.6 S1
// 출력 재살균으로 개행이 중립화된 뒤 절단 출력된다(프롬프트 주입 경계
// 방어 — read 경로와 동일). want는 리터럴 고정(v0.20.1 테스트 감사 T8).
// 접미 가드(lines↔rows 대응)는 재살균과 무관한 심층 방어로 유지.
func TestHotFullNewlineSummarySkipsSuffix(t *testing.T) {
	ctx := context.Background()
	validAt := time.Now().UTC().Add(-49 * time.Hour)
	hotRows := [][]any{
		{"pending", "토픽", "요약\n둘째 줄", validAt, int32(3), validAt, validAt},
	}
	// 리터럴 형식: 필드 구분 "|"·시각은 fmt.Sprint 기본형(FormatRows 비JSON
	// 계약). 입력값 자체의 표준 포맷팅은 자기복창이 아니다(생산 코드 무관).
	// 재살균된 요약(개행 제거)은 정상 접미 대상 — -49h는 ·+2일.
	want := "pending|토픽|요약둘째 줄|" + fmt.Sprint(validAt) + "·+2일\n"
	var out bytes.Buffer
	f := &fakeStore{queryFn: pendingAgeQueryFn(hotRows)}
	if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit = %d, want 0", code)
	}
	if out.String() != want {
		t.Fatalf("출력 = %q, want %q(접미 생략·기존 출력 승계)", out.String(), want)
	}

}
