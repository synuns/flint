package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/synuns/flint/lib/wire"
)

func TestWriteExitFlushesScanStats(t *testing.T) {
	calls := 0
	prev := PersistScanStatsFn
	PersistScanStatsFn = func(ctx context.Context, st wire.Store) { calls++ }
	t.Cleanup(func() { PersistScanStatsFn = prev })

	out, code := run(t, &fakeStore{}, []string{"knowledge"},
		`{"topic":"v1211 데모","summary":"합성 본문","confidence":0.9}`)
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	if calls != 1 {
		t.Fatalf("write 종료 시 플러시 %d회, want 1", calls)
	}
}

func TestReadExitFlushesScanStats(t *testing.T) {
	calls := 0
	prev := PersistScanStatsFn
	PersistScanStatsFn = func(ctx context.Context, st wire.Store) { calls++ }
	t.Cleanup(func() { PersistScanStatsFn = prev })

	var out strings.Builder
	code := RunRead(context.Background(), &fakeStore{}, []string{"데모 질의"}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("exit=%d — %s", code, out.String())
	}
	if calls != 1 {
		t.Fatalf("read 종료 시 플러시 %d회, want 1", calls)
	}
}
