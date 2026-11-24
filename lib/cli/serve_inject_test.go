package cli

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/synuns/flint/lib/ops"
	"github.com/synuns/flint/lib/wire"
)

type countingServeStore struct {
	*fakeStore
	onClose func()
}

func (c *countingServeStore) Close() { c.onClose() }

func TestServeFactoryPerRequest(t *testing.T) {
	var mu sync.Mutex
	created, closed := 0, 0
	orig := cliServeNewStoreFn
	cliServeNewStoreFn = func(ctx context.Context) (wire.Store, error) {
		mu.Lock()
		created++
		mu.Unlock()
		return &countingServeStore{fakeStore: &fakeStore{}, onClose: func() {
			mu.Lock()
			closed++
			mu.Unlock()
		}}, nil
	}
	t.Cleanup(func() { cliServeNewStoreFn = orig })

	port := 17749
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out strings.Builder
	done := make(chan int, 1)
	go func() {
		done <- ops.RunReport(ctx, &fakeStore{},
			[]string{"--serve", "--port", strconv.Itoa(port)}, nil, &out)
	}()

	addr := "http://127.0.0.1:" + strconv.Itoa(port) + "/"
	up := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(addr)
		if err == nil {
			resp.Body.Close()
			up = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !up {
		t.Fatalf("serve not reachable: %s", out.String())
	}
	resp, err := http.Get(addr)
	if err != nil {
		t.Fatalf("request 2: %v", err)
	}
	resp.Body.Close()
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if created != 2 {
		t.Fatalf("stores created = %d, want 2 (per-request, no shared fallback)", created)
	}
	if closed != created {
		t.Fatalf("closed = %d, created = %d — handler must Close per request", closed, created)
	}
}
