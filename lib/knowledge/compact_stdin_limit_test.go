// compact_stdin_limit_test.go — the compact stdin 8MiB cap regression.
// Over-cap input is a quiet no-op exit 0 before parsing, even if it is
// valid JSON (§13 non-interference — abolishing unbounded io.ReadAll).
// The boundary lower edge (exactly 8MiB) is processed normally as before
// — pinning the off-by-one direction of LimitReader(max+1).
package knowledge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// overLimitHookJSON — a hook JSON whose compact_summary is valid while
// the whole serialization exceeds 8MiB (the excess must be parseable to
// distinguish this from an unbounded implementation).
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

// TestCompactStdinOverLimitSilentNoOp — over-cap input: exit 0·empty
// stdout·0 accruals. Under an unbounded io.ReadAll implementation this
// JSON would be parsed and a session row accrued, so the inserted 0
// assertion catches the regression.
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

// TestCompactStdinAtLimitStillParsed — a JSON of exactly the cap size
// proceeds down the existing pipeline, not rejected (the boundary
// lower-edge direction stays invariant).
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
