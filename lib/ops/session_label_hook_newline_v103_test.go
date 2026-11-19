// session_label_hook_newline_v103_test.go — regression (hook path):
// a --session label containing a newline is treated as absent. sanitizeHookSession
// uses the shared knowledge.ValidateSessionLabel guard, so the guard's newline rejection
// flows as-is into the hook entry points (the session-start/session-end fallback paths) —
// pinning that it resolves to the arguments with the invalid pair stripped and an empty label.
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
