// compact_stdin_limit_test.go — v1.0.3 R3 회귀: compact stdin 상한 8MiB.
// 상한 초과 입력은 정상 JSON이라도 파싱 전에 조용히 무동작 exit 0(§13
// 비간섭 — 무상한 io.ReadAll 폐지). 경계 하한(정확히 8MiB)은 기존대로
// 정상 처리 — LimitReader(max+1) 오프바이원 방향 고정.
package knowledge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// overLimitHookJSON — compact_summary가 유효한 채로 전체 직렬화가
// 8MiB를 초과하는 훅 JSON(초과분이 파싱 가능해야 무상한 구현과 구별됨).
func overLimitHookJSON(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"hook_event_name": "PostCompact",
		"session_id":      "",
		"compact_summary": strings.Repeat("s", compactMaxBytes+128),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(b) <= compactMaxBytes {
		t.Fatalf("픽스처가 상한 미달: %d bytes", len(b))
	}
	return string(b)
}

// TestCompactStdinOverLimitSilentNoOp — 초과 입력은 exit 0·stdout 무출력·
// 적립 0건. 무상한 io.ReadAll 구현에서는 이 JSON이 파싱돼 session 행이
// 적립되므로 inserted 0 단언이 회귀를 잡는다.
func TestCompactStdinOverLimitSilentNoOp(t *testing.T) {
	f := &fakeStore{}
	var out strings.Builder
	code := RunCompact(context.Background(), f, nil,
		strings.NewReader(overLimitHookJSON(t)), &out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0(초과 입력은 조용히 무동작)", code)
	}
	if out.Len() != 0 {
		t.Errorf("stdout 출력(비간섭 §13 위반): %s", out.String())
	}
	if len(f.inserted) != 0 {
		t.Errorf("상한 초과 입력이 적립됨: %d행", len(f.inserted))
	}
}

// TestCompactStdinAtLimitStillParsed — 정확히 상한 크기 JSON은 거부가
// 아니라 기존 파이프라인 진행(경계 하강 방향 불변).
func TestCompactStdinAtLimitStillParsed(t *testing.T) {
	head := `{"hook_event_name":"PostCompact","session_id":"","compact_summary":"본문`
	tail := `"}`
	pad := compactMaxBytes - len(head) - len(tail)
	if pad < 0 {
		t.Fatalf("상한이 헤드보다 작음: %d", compactMaxBytes)
	}
	at := head + strings.Repeat(" ", pad) + tail
	if len(at) != compactMaxBytes {
		t.Fatalf("경계 픽스처 크기 = %d, want %d", len(at), compactMaxBytes)
	}
	f := &fakeStore{}
	var out strings.Builder
	code := RunCompact(context.Background(), f, nil, strings.NewReader(at), &out)
	if code != 0 {
		t.Fatalf("exit=%d, want 0(정상 처리)", code)
	}
	if len(f.inserted) != 1 {
		t.Errorf("경계(8MiB) 입력 적립 = %d행, want 1(초과 오판)", len(f.inserted))
	}
}
