package knowledge

import (
	"context"
	"strings"
	"testing"
)

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
