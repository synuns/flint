// read_test.go — flint read 명령 계약(버전 이기 추가 — v0.20.2부터
// 신규 테스트는 이 파일에, 버전은 주석 표기: docs/TESTING.md §1).
package cli

import (
	"context"
	"strings"
	"testing"
)

// TestReadFieldsValidation — v0.20.2(패치 루프 1차 F5): --fields는 알려진
// 컬럼만 허용. 오타("sumary")가 위치 fallback으로 조용히 다른 컬럼 값을
// 채우던 왜곡을 exit 3으로 바꾼다.
func TestReadFieldsValidation(t *testing.T) {
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}
	var out strings.Builder
	code := runRead(context.Background(), st,
		[]string{"백업", "--fields", "topic,sumary"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit = %d, want 3(오타 필드): %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"fields"`) {
		t.Errorf("오류 필드에 fields 없음: %s", out.String())
	}
	// 정상 필드는 통과(회귀 — 기존 사용 형태).
	var out2 strings.Builder
	code2 := runRead(context.Background(), st,
		[]string{"백업", "--fields", "topic,confidence"}, strings.NewReader(""), &out2)
	if code2 == 3 {
		t.Fatalf("정상 필드 exit 3: %s", out2.String())
	}
}
