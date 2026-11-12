// session_label_hook_newline_v103_test.go — v1.0.3 R3 회귀(훅 경로):
// 개행 포함 --session 라벨은 부재 취급. sanitizeHookSession은
// knowledge.ValidateSessionLabel 공용 가드를 쓰므로, 가드의 개행 거부가
// 훅 진입점(session-start·session-end 폴백 경로)에도 그대로 흐른다 —
// 무효 쌍이 걷어진 인자와 빈 라벨로 정리되는지 고정.
package ops

import (
	"strings"
	"testing"
)

func TestSanitizeHookSessionNewlineAbsent(t *testing.T) {
	args, label := sanitizeHookSession([]string{"hot", "--session", "bad\nlabel", "--json"})
	for _, a := range args {
		if strings.ContainsAny(a, "\n\r") {
			t.Errorf("무효 라벨이 인자에 잔존: %q", args)
		}
	}
	if strings.Contains(strings.Join(args, " "), "--session") {
		t.Errorf("--session 쌍이 걷어지지 않음(부재 취급 계약 위반): %v", args)
	}
	if label != "" {
		t.Errorf("라벨 = %q, want \"\"(개행 라벨 부재 취급)", label)
	}
}
