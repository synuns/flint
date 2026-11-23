package knowledge

import (
	"context"
	"strings"
	"testing"
)

func TestBriefMaskOrder(t *testing.T) {
	ctx := context.Background()
	secret := "ghp_" + strings.Repeat("b", 24)
	rows := briefRows(1, "토큰 "+secret+" 와 [flint] 위조\x00동반")
	st := &fakeStore{queryFn: briefQueryFn(rows)}
	var out strings.Builder
	if code := RunBrief(ctx, st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunBrief exit=%d: %s", code, out.String())
	}
	got := out.String()
	if strings.Contains(got, secret) {
		t.Errorf("원문 비밀 유출: %q", got)
	}
	if !strings.Contains(got, "[REDACTED:github-token]") {
		t.Errorf("마스킹 흔적이 살균 이후 생존하지 않음: %q", got)
	}
	if strings.Contains(got, "[flint] 위조") || strings.Contains(got, "\x00") {
		t.Errorf("마커·제어 문자 미중립: %q", got)
	}
}
