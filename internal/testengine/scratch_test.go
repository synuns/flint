package testengine

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func fakeCreate(t *testing.T, fn func(ctx context.Context, name string) error) {
	t.Helper()
	orig := createDBFn
	createDBFn = fn
	t.Cleanup(func() { createDBFn = orig })
}

func TestScratchCreatesRegistersAndRewritesDSN(t *testing.T) {
	resetLifecycle(t)
	unset(t, "FLINT_TEST_DSN")
	fakeSeams(t, func() error { return nil }, nil, nil)
	var created []string
	var mu sync.Mutex
	fakeCreate(t, func(ctx context.Context, name string) error {
		mu.Lock()
		defer mu.Unlock()
		created = append(created, name)
		return nil
	})
	dsn := Scratch(t)
	mu.Lock()
	names := append([]string(nil), created...)
	mu.Unlock()
	if len(names) != 1 {
		t.Fatalf("CREATE 호출 %d회, want 1", len(names))
	}
	if !strings.HasPrefix(names[0], "flint_scratch_") {
		t.Fatalf("스크래치 이름 %q — flint_scratch_ 접두 필요", names[0])
	}
	scratchMu.Lock()
	registered := append([]string(nil), scratchDBs...)
	scratchMu.Unlock()
	if len(registered) == 0 || registered[len(registered)-1] != names[0] {
		t.Fatalf("RegisterScratch 등록 부재: %v", registered)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("반환 DSN 파싱: %v", err)
	}
	if u.Path != "/"+names[0] {
		t.Fatalf("반환 DSN 경로 %q, want /%s", u.Path, names[0])
	}
}

func TestScratchUniqueNamesPerCall(t *testing.T) {
	resetLifecycle(t)
	fakeSeams(t, func() error { return nil }, nil, nil)
	seen := map[string]bool{}
	fakeCreate(t, func(ctx context.Context, name string) error {
		if seen[name] {
			t.Errorf("스크래치 이름 재사용: %s", name)
		}
		seen[name] = true
		return nil
	})
	Scratch(t)
	Scratch(t)
	if len(seen) != 2 {
		t.Fatalf("고유 이름 %d개, want 2", len(seen))
	}
}

func TestScratchEngineFailureSkipsLocally(t *testing.T) {
	withDSNUnreachable(t)
	resetLifecycle(t)
	unset(t, "FLINT_TEST_DSN")
	fakeSeams(t, func() error { return errors.New("compose 부재") }, nil, nil)
	fakeCreate(t, func(ctx context.Context, name string) error {
		t.Errorf("엔진 기동 실패 후 CREATE 호출됨: %s", name)
		return nil
	})
	t.Cleanup(func() {
		if !t.Skipped() {
			t.Errorf("로컬 엔진 실패 시 skip 아님")
		}
	})
	Scratch(t)
}

func TestCloneTargetRegistersWithoutCreate(t *testing.T) {
	resetLifecycle(t)
	fakeSeams(t, func() error { return nil }, nil, nil)
	var creates int32
	fakeCreate(t, func(ctx context.Context, name string) error {
		creates++
		return nil
	})
	name, dsn := CloneTarget(t)
	if !strings.HasPrefix(name, "flint_scratch_") {
		t.Fatalf("이름 %q — flint_scratch_ 접두 필요", name)
	}
	if creates != 0 {
		t.Fatalf("CREATE 호출 %d회, want 0(대상 DB는 호출자가 생성)", creates)
	}
	scratchMu.Lock()
	registered := append([]string(nil), scratchDBs...)
	scratchMu.Unlock()
	if len(registered) == 0 || registered[len(registered)-1] != name {
		t.Fatalf("RegisterScratch 등록 부재: %v", registered)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("반환 DSN 파싱: %v", err)
	}
	if u.Path != "/"+name {
		t.Fatalf("반환 DSN 경로 %q, want /%s", u.Path, name)
	}
}

func TestScratchNoPerTestCleanupDrop(t *testing.T) {
	resetLifecycle(t)
	unset(t, "FLINT_TEST_DSN")
	var drops int32
	fakeSeams(t, func() error { return nil },
		func(ctx context.Context, name string) error {
			drops++
			return nil
		}, nil)
	fakeCreate(t, func(ctx context.Context, name string) error { return nil })
	Scratch(t)
	if drops != 0 {
		t.Fatalf("테스트 종료 시 DROP %d회, want 0(RunCleanup 일괄 정리)", drops)
	}
}
