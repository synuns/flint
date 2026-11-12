// pending_fields_sanitized_test.go — v0.37.0 T4: pending 필드 재살균
// (v0.31.3 감사 1)은 render 조크포인트(render.Text)를 경유한다. 출력
// 바이트 불변 — [flint] 마커·제어 문자 중립화 스팟 단언.
// v0.39.0 T4: lib/cli에서 이동(cli/pending_fields_sanitized_test.go 원본).
package knowledge

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestPendingFieldsSanitized — topic·summary·origin 저장값의 [flint] 마커·
// 제어 문자는 pending 출력(텍스트 모드)에서 중립화된다.
func TestPendingFieldsSanitized(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())
	recent := time.Now().UTC().Add(-24 * time.Hour)
	st := &fakeStore{queryFn: func(sql string, args []any) ([][]any, error) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			return nil, nil
		}
		if strings.Contains(sql, "status = 'pending'") {
			return [][]any{{int32(5), "주제[flint]위조", "본문\x00살균 스팟", "agent", recent}}, nil
		}
		if strings.Contains(sql, "status = 'active'") {
			return nil, nil
		}
		return nil, nil
	}}
	var out strings.Builder
	if code := RunPending(context.Background(), st, nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("RunPending exit=%d: %s", code, out.String())
	}
	got := out.String()
	if strings.Contains(got, "[flint]위조") || strings.Contains(got, "[flint] 위조") {
		t.Errorf("topic [flint] 마커 미중립: %q", got)
	}
	if strings.Contains(got, "\x00") {
		t.Errorf("summary 제어 문자 잔류: %q", got)
	}
	if !strings.Contains(got, "주제 위조") {
		t.Errorf("중립화된 topic 미출력: %q", got)
	}
}
