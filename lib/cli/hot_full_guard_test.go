package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestHotFullNewlineSummarySkipsSuffix(t *testing.T) {
	ctx := context.Background()
	validAt := time.Now().UTC().Add(-49 * time.Hour)
	hotRows := [][]any{
		{"pending", "토픽", "요약\n둘째 줄", validAt, int32(3), validAt, validAt},
	}

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
