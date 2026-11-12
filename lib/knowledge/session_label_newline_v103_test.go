// session_label_newline_v103_test.go — v1.0.3 R3 회귀: --session 라벨
// 개행 거부. 라벨은 세션 라벨 파일(값 1줄 계약)과 read_events·hot_view
// 저장값 양쪽으로 쓰인다 — \n·\r 포함 값은 CLI 경로(read 계약 승격)
// 무효 인자 exit 3. given=false(미지정) 통과는 기존 불변.
package knowledge

import (
	"context"
	"strings"
	"testing"
)

// TestSessionLabelNewlineRejected — 공용 가드 단위 계약: 개행(LF·CR·CRLF)
// 포함 라벨은 SchemaError, 정상 라벨·미지정은 통과.
func TestSessionLabelNewlineRejected(t *testing.T) {
	for _, bad := range []string{"two\nlines", "cr\rlabel", "\n", "a\r\nb"} {
		if err := ValidateSessionLabel(true, bad); err == nil {
			t.Errorf("ValidateSessionLabel(true, %q) = nil, want error", bad)
		}
	}
	for _, ok := range []string{"sess-a", "flint-세션-01"} {
		if err := ValidateSessionLabel(true, ok); err != nil {
			t.Errorf("ValidateSessionLabel(true, %q) = %v, want nil", ok, err)
		}
	}
	if err := ValidateSessionLabel(false, "x\ny"); err != nil {
		t.Errorf("given=false(미지정)이 거부됨: %v", err)
	}
}

// TestRunReadSessionNewlineExits3 — CLI 경로: 개행 라벨은 검증 에러
// exit 3, 조회 실행 전 차단(쿼리 0회 — 오염 경로 폐쇄).
func TestRunReadSessionNewlineExits3(t *testing.T) {
	f := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		return nil, nil
	}}
	var out strings.Builder
	code := RunRead(context.Background(), f,
		[]string{"--session", "bad\nlabel", "--json"}, strings.NewReader(""), &out)
	if code != 3 {
		t.Fatalf("exit=%d, want 3(개행 라벨 무효 인자); out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"field":"session"`) {
		t.Errorf("session 스키마 에러 아님: %s", out.String())
	}
	if len(f.queries) != 0 {
		t.Errorf("개행 라벨로 조회 실행됨(검증 후 차단 계약 위반): %d회", len(f.queries))
	}
}
