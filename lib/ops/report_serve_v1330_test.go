package ops

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/synuns/flint/lib/wire"
)

func serveWaitUp(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("server not reachable: %s", url)
}

func freeServeTestPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func TestServeFlagWiring(t *testing.T) {
	oldFn := serveNewStoreFn
	serveNewStoreFn = func(context.Context) (wire.Store, error) { return &fakeStore{}, nil }
	t.Cleanup(func() { serveNewStoreFn = oldFn })

	port := freeServeTestPort(t)
	ctx, cancel := context.WithCancel(context.Background())
	var out strings.Builder
	done := make(chan int, 1)
	go func() {
		done <- RunReport(ctx, &fakeStore{},
			[]string{"--serve", "--port", strconv.Itoa(port)}, nil, &out)
	}()

	addr := "127.0.0.1:" + strconv.Itoa(port)
	serveWaitUp(t, "http://"+addr+"/api/diagnose.json")
	resp, err := http.Get("http://" + addr + "/api/diagnose.json")
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("diagnose status = %d, want 200", resp.StatusCode)
	}

	resp2, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("html: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("html status = %d, want 200", resp2.StatusCode)
	}

	log := out.String()
	for _, want := range []string{"127.0.0.1", strconv.Itoa(port), "Ctrl-C"} {
		if !strings.Contains(log, want) {
			t.Errorf("startup log missing %q: %s", want, log)
		}
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("RunReport --serve exit = %d, want 0 (out %s)", code, out.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunReport --serve did not stop after cancel")
	}
}

func TestUsageUpdated(t *testing.T) {
	var out strings.Builder
	code := RunReport(context.Background(), &fakeStore{}, []string{"--nope"}, nil, &out)
	if code != 3 {
		t.Fatalf("unknown flag exit = %d, want 3", code)
	}
	if !strings.Contains(out.String(), "--serve") || !strings.Contains(out.String(), "--port") {
		t.Fatalf("usage missing --serve/--port: %s", out.String())
	}
}

func TestReportPortFlagWithoutServeExit3(t *testing.T) {
	var out strings.Builder
	code := RunReport(context.Background(), &fakeStore{},
		[]string{"--port", "17611"}, nil, &out)
	if code != 3 {
		t.Fatalf("--port without --serve exit = %d, want 3 (out %s)", code, out.String())
	}
}

func TestReportServeHtmlExclusiveExit3(t *testing.T) {
	var out strings.Builder
	code := RunReport(context.Background(), &fakeStore{},
		[]string{"--serve", "--html"}, nil, &out)
	if code != 3 {
		t.Fatalf("--serve --html exit = %d, want 3 (out %s)", code, out.String())
	}
}

func TestReportOutNoticeNamesFlag(t *testing.T) {
	var out strings.Builder
	RunReport(context.Background(), &fakeStore{queryErr: context.DeadlineExceeded},
		[]string{"--out", "report.html"}, nil, &out)
	if !strings.Contains(out.String(), DeprecationNotice("--out")) {
		t.Fatalf("--out notice missing: %s", out.String())
	}
}

func occupyServePorts(t *testing.T) (int, []net.Listener) {
	t.Helper()
	for attempt := 0; attempt < 25; attempt++ {
		probe, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("probe listen: %v", err)
		}
		base := probe.Addr().(*net.TCPAddr).Port
		probe.Close()
		lns := make([]net.Listener, 0, servePortAttempts)
		for p := base; p < base+servePortAttempts; p++ {
			ln, lerr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
			if lerr != nil {
				break
			}
			lns = append(lns, ln)
		}
		if len(lns) == servePortAttempts {
			return base, lns
		}
		for _, ln := range lns {
			ln.Close()
		}
	}
	t.Skip("no consecutive free port range available")
	return 0, nil
}

func TestServeBindFailExitCode(t *testing.T) {
	oldFn := serveNewStoreFn
	serveNewStoreFn = func(context.Context) (wire.Store, error) { return &fakeStore{}, nil }
	t.Cleanup(func() { serveNewStoreFn = oldFn })

	base, lns := occupyServePorts(t)
	defer func() {
		for _, ln := range lns {
			ln.Close()
		}
	}()

	var out strings.Builder
	code := runReportServe(context.Background(), &fakeStore{}, 30, base, &out)
	if code != 1 {
		t.Fatalf("bind failure exit = %d, want 1 (out %s)", code, out.String())
	}
	if !strings.Contains(out.String(), "no free port") {
		t.Fatalf("output %q lacks bind failure reason", out.String())
	}
}

func TestServeSignalExitZero(t *testing.T) {
	oldFn := serveNewStoreFn
	serveNewStoreFn = func(context.Context) (wire.Store, error) { return &fakeStore{}, nil }
	t.Cleanup(func() { serveNewStoreFn = oldFn })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	port := freeServeTestPort(t)
	var out strings.Builder
	code := runReportServe(ctx, &fakeStore{}, 30, port, &out)
	if code != 0 {
		t.Fatalf("signal-path exit = %d, want 0 (out %s)", code, out.String())
	}
	if !strings.Contains(out.String(), strconv.Itoa(port)) {
		t.Fatalf("output %q lacks the serve startup line", out.String())
	}
}
