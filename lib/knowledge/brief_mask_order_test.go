// brief_mask_order_test.go — v0.37.0 T4: brief 셀 살균은 render 조크포인트
// (render.Masked — MaskSecrets→SanitizeText 순서 보존, ADR-0015)를
// 경유한다. [REDACTED:…] 흔적은 중립화를 통과해 생존한다.
// v0.39.0 T4: lib/cli에서 이동(cli/brief_mask_order_test.go 원본).
package knowledge

import (
	"context"
	"strings"
	"testing"
)

// TestBriefMaskOrder — 마스킹→살균 순서 계약: 비밀은 먼저 치환되고 그
// 흔적([REDACTED:github-token])은 이후 살균을 통과해 남는다. 마커·제어
// 문자는 여전히 중립화된다.
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
