// read_test.go — flint read 명령 계약(버전 이기 추가 — v0.20.2부터
// 신규 테스트는 이 파일에, 버전은 주석 표기: docs/TESTING.md §1).
package cli

import (
	"context"
	"github.com/synuns/flint/lib/store"
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

// TestReadRRFKInjectionBeforeReorder — v0.25.1(패치 루프 2차 직접 발견):
// rrf_k 주입이 폴백 재랭킹 '뒤'(제안 블록)에 있어 단발 CLI에서 같은
// 실행에 반영되지 않던 배선 결함 — loadThresholds 호출 순서 단얜.
func TestReadRRFKInjectionBeforeReorder(t *testing.T) {
	var order []string
	savedLT := loadThresholdsFn
	loadThresholdsFn = func(ctx context.Context, st Store) (store.Thresholds, error) {
		order = append(order, "loadThresholds")
		return store.DefaultThresholds(), nil
	}
	t.Cleanup(func() { loadThresholdsFn = savedLT })
	// 폴백 경로 read(제로히트 구문 → OR 재쿼리 히트).
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		switch {
		case strings.Contains(sql, "FROM active_knowledge"):
			if strings.Contains(sql, " OR ") {
				return [][]any{{"1", "백업 정책", "매일", ":b:", "", "", "0.9", "active", "0"}}, nil
			}
			return nil, nil
		case strings.Contains(sql, "SELECT value FROM meta"):
			return nil, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	code := runRead(context.Background(), f, []string{"백업 정책"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if len(order) == 0 {
		t.Fatal("폴백 경로에서 loadThresholds 미호출 — rrf_k 주입 누락")
	}
}
