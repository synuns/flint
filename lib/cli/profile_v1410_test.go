package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type profileBarrierStore struct {
	*fakeStore
	check func(sql string, args []any)
}

func (b *profileBarrierStore) Query(ctx context.Context, sql string, args ...any) ([][]any, error) {
	b.check(sql, args)
	return b.fakeStore.Query(ctx, sql, args...)
}

func TestProfileMetaParallel(t *testing.T) {
	t.Setenv("FLINT_CONFIG_DIR", t.TempDir())

	const want = 2
	var mu sync.Mutex
	entered := 0
	joined := map[string]bool{}
	var barrierErr error
	release := make(chan struct{})
	join := func(pat string) {
		mu.Lock()
		if !joined[pat] {
			joined[pat] = true
			entered++
			if entered == want {
				close(release)
			}
		}
		mu.Unlock()
		select {
		case <-release:
		case <-time.After(2 * time.Second):
			barrierErr = errors.New("2-way barrier timeout — writeThresholds·metaKV 읽기가 병렬로 겹치지 않음(순차 실행)")
		}
	}
	base := profFake(0, 0)
	thCalls := 0
	barrier := &profileBarrierStore{fakeStore: base}
	barrier.check = func(sql string, args []any) {
		if strings.Contains(sql, "key IN ('pending_max'") {
			mu.Lock()
			thCalls++
			n := thCalls
			mu.Unlock()
			if n == 2 {
				join("thresholds")
			}
		} else if len(args) > 0 && args[0] == "exposure_profile" {
			join("meta")
		}
	}
	var out strings.Builder
	if code := runProfile(context.Background(), barrier, nil, nil, &out); code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	if barrierErr != nil {
		t.Fatalf("병렬 검증 실패: %v", barrierErr)
	}
	var plain strings.Builder
	if code := runProfile(context.Background(), profFake(0, 0), nil, nil, &plain); code != 0 {
		t.Fatalf("기준 exit=%d: %s", code, plain.String())
	}
	if out.String() != plain.String() {
		t.Errorf("병렬 출력과 순차 출력 불일치:\n par=%s\nseq=%s", out.String(), plain.String())
	}
}
