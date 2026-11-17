// diff_test.go — diff usage 오류 타입 계약.
// 인자 수 usage 오류가 error:"usage"(wire.UsageErrorf
// → ErrLine)로 분류되는지 검증한다. 행 상세·요약 회귀는 cli/diff_test.go가
// 담당(실행 경로 동일).
package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestRunDiffUsageErrorType — protocol: 인자 수 위반(0·1·3개)
// 은 exit 3 + error:"usage" + message에 usage: flint diff 안내. 레거시 원시
// 문자열("usage: ..." 전체가 error 값)이 아니어야 한다.
func TestRunDiffUsageErrorType(t *testing.T) {
	for _, args := range [][]string{nil, {"aaaa1"}, {"aaaa1", "bbbb2", "extra"}} {
		var out bytes.Buffer
		code := RunDiff(context.Background(), nil, args, strings.NewReader(""), &out)
		if code != 3 {
			t.Fatalf("%v: exit=%d, want 3 (출력 %q)", args, code, out.String())
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &m); err != nil {
			t.Fatalf("%v: JSON 아님: %q", args, out.String())
		}
		if m["error"] != "usage" {
			t.Fatalf("%v: error=%v, want \"usage\"", args, m["error"])
		}
		msg, _ := m["message"].(string)
		if !strings.Contains(msg, "usage: flint diff <from> <to>") {
			t.Fatalf("%v: message=%q, want usage 안내 포함", args, msg)
		}
	}
}
