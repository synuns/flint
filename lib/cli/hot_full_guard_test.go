// hot_full_guard_test.go — the --full suffix safety guard: if a summary contains a newline,
// the lines<->rows correspondence breaks, so no suffix is appended at all (output and row count unchanged).
package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestHotFullNewlineSummarySkipsSuffix — a newline-containing summary is output
// truncated after the newline is neutralized by output re-sanitization (the prompt-injection
// boundary defense — same as the read path). want is pinned as a literal.
// The suffix guard (lines<->rows correspondence) remains a defense-in-depth independent of re-sanitization.
func TestHotFullNewlineSummarySkipsSuffix(t *testing.T) {
	ctx := context.Background()
	validAt := time.Now().UTC().Add(-49 * time.Hour)
	hotRows := [][]any{
		{"pending", "토픽", "요약\n둘째 줄", validAt, int32(3), validAt, validAt},
	}
	// the literal format: fields separated by "|", time in fmt.Sprint's default form (the FormatRows non-JSON
	// contract). Standard formatting of the input values themselves is not self-echoing (irrelevant to production code).
	// a re-sanitized summary (newline removed) is a normal suffix target — -49h is ·+2d.
	want := "pending|토픽|요약둘째 줄|" + fmt.Sprint(validAt) + "·+2d\n"
	var out bytes.Buffer
	f := &fakeStore{queryFn: pendingAgeQueryFn(hotRows)}
	if code := runHot(ctx, f, []string{"--full"}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("runHot --full exit = %d, want 0", code)
	}
	if out.String() != want {
		t.Fatalf("출력 = %q, want %q(접미 생략·기존 출력 승계)", out.String(), want)
	}

}
