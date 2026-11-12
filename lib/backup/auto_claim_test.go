package backup

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAutoBackupClaimPreventsDuplicate — v1.0.10(감사 R2-A4): 병렬 done
// 세션이 동시에 만료 창을 통과해도 CAS 선점으로 덤프는 정확히 1회.
// claim은 원자 compare-exchange(실DB의 WHERE value=old와 동일 의미).
func TestAutoBackupClaimPreventsDuplicate(t *testing.T) {
	origDump := dumpFn
	defer func() { dumpFn = origDump }()
	var mu sync.Mutex
	last := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	dumps := 0
	dumpFn = func(context.Context, string, io.Writer) error {
		mu.Lock()
		dumps++
		mu.Unlock()
		return nil
	}
	metaKV := func(string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		return last, nil
	}
	claim := func(oldVal, newVal string) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		if last == oldVal {
			last = newVal
			return true, nil
		}
		return false, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := Auto{}
			_ = a.RunIfNeeded(context.Background(), metaKV, claim, &strings.Builder{})
		}()
	}
	wg.Wait()
	if dumps != 1 {
		t.Fatalf("덤프 %d회 — CAS 선점 후에도 이중 실행", dumps)
	}
}

// TestAutoBackupClaimSkipsWhenFresh — 신선 스탬프는 no-op(덤프 0회).
func TestAutoBackupClaimSkipsWhenFresh(t *testing.T) {
	origDump := dumpFn
	defer func() { dumpFn = origDump }()
	dumps := 0
	dumpFn = func(context.Context, string, io.Writer) error {
		dumps++
		return nil
	}
	metaKV := func(string) (string, error) {
		return time.Now().UTC().Format(time.RFC3339), nil
	}
	a := Auto{}
	if err := a.RunIfNeeded(context.Background(), metaKV, func(_, _ string) (bool, error) { return false, nil }, &strings.Builder{}); err != nil {
		t.Fatalf("신선 스탬프인데 에러: %v", err)
	}
	if dumps != 0 {
		t.Fatalf("신선 스탬프인데 덤프 %d회", dumps)
	}
}
